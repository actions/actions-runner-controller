package scaler

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"math"
	"sync"
	"time"

	"github.com/actions/actions-runner-controller/apis/actions.github.com/v1alpha1"
	"github.com/actions/actions-runner-controller/cmd/ghalistener/metrics"
	"github.com/actions/scaleset"
	"github.com/actions/scaleset/listener"
	jsonpatch "github.com/evanphx/json-patch"
	kerrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

type Option func(*Scaler)

func WithLogger(logger *slog.Logger) Option {
	return func(w *Scaler) {
		w.logger = logger
	}
}

type Config struct {
	EphemeralRunnerSetNamespace string
	EphemeralRunnerSetName      string
	MaxRunners                  int
	MinRunners                  int
	ScalerConfig                *v1alpha1.ScalerConfig
}

const (
	defaultQPS        = 50
	defaultBurst      = 100
	defaultScaleQPS   = 10
	defaultScaleBurst = 20
	jobWorkerCount    = 10
	jobQueueSize      = 100
)

// The Scaler's role is to process the messages it receives from the listener.
// It then initiates Kubernetes API requests to carry out the necessary actions.
type Scaler struct {
	scaleClientset *kubernetes.Clientset
	jobClientset   *kubernetes.Clientset
	config         Config
	targetRunners  int
	patchSeq       int
	// dirty is set when there are any events handled before the desired count is called.
	dirty bool

	jobs       chan *scaleset.JobStarted
	jobWorkers sync.WaitGroup
	jobCtx     context.Context
	jobCancel  context.CancelFunc
	closeOnce  sync.Once

	logger *slog.Logger

	messageClient    listener.Client
	recorder         metrics.Recorder
	latestStatistics *scaleset.RunnerScaleSetStatistic
}

var _ listener.Scaler = (*Scaler)(nil)

func New(config Config, messageClient listener.Client, recorder metrics.Recorder, options ...Option) (*Scaler, error) {
	if messageClient == nil {
		return nil, fmt.Errorf("message client is required")
	}
	if recorder == nil {
		recorder = metrics.Discard
	}
	w := &Scaler{
		config:        config,
		targetRunners: -1,
		patchSeq:      -1,
		jobs:          make(chan *scaleset.JobStarted, jobQueueSize),
		messageClient: messageClient,
		recorder:      recorder,
	}
	for _, option := range options {
		option(w)
	}
	if err := w.applyDefaults(); err != nil {
		return nil, err
	}

	conf, err := rest.InClusterConfig()
	if err != nil {
		return nil, err
	}

	jobQPS, jobBurst := effectiveRateLimiterConfig(config.ScalerConfig, w.logger)
	jobConf := rest.CopyConfig(conf)
	jobConf.QPS = float32(jobQPS)
	jobConf.Burst = jobBurst
	jobClientset, err := kubernetes.NewForConfig(jobConf)
	if err != nil {
		return nil, err
	}

	// Keep job patch traffic from consuming the scale patch's rate limit.
	scaleConf := rest.CopyConfig(conf)
	scaleConf.QPS = defaultScaleQPS
	scaleConf.Burst = defaultScaleBurst
	scaleClientset, err := kubernetes.NewForConfig(scaleConf)
	if err != nil {
		return nil, err
	}

	w.jobClientset = jobClientset
	w.scaleClientset = scaleClientset

	w.startJobWorkers()

	return w, nil
}

func (w *Scaler) startJobWorkers() {
	w.jobCtx, w.jobCancel = context.WithCancel(context.Background())

	for range jobWorkerCount {
		w.jobWorkers.Add(1)
		go func() {
			defer w.jobWorkers.Done()

			for jobInfo := range w.jobs {
				backoff := wait.Backoff{Duration: time.Second, Factor: 2, Steps: 6, Cap: 30 * time.Second}
				for {
					if w.jobCtx.Err() != nil {
						return
					}
					err := w.patchJobStarted(w.jobCtx, jobInfo)
					if err == nil {
						break
					}
					if w.jobCtx.Err() != nil {
						return
					}
					delay := backoff.Step()
					w.logger.Error("Failed to patch job started event",
						"runnerName", jobInfo.RunnerName,
						"requestId", jobInfo.RunnerRequestID,
						"error", err,
						"retryAfter", delay,
					)
					timer := time.NewTimer(delay)
					select {
					case <-w.jobCtx.Done():
						timer.Stop()
						return
					case <-timer.C:
					}
				}
			}
		}()
	}
}

// Close drains accepted job events until ctx expires.
func (w *Scaler) Close(ctx context.Context) error {
	w.closeOnce.Do(func() {
		close(w.jobs)
	})

	drained := make(chan struct{})
	go func() {
		w.jobWorkers.Wait()
		close(drained)
	}()

	select {
	case <-drained:
		w.jobCancel()
		return nil
	case <-ctx.Done():
		if depth := len(w.jobs); depth > 0 {
			w.logger.Error("Abandoning queued job started events", "count", depth)
		}
		w.jobCancel()
		<-drained
		return ctx.Err()
	}
}

func effectiveRateLimiterConfig(config *v1alpha1.ScalerConfig, logger *slog.Logger) (int, int) {
	if config == nil {
		logger.Debug("Listener scaler configuration is missing; using defaults", "qps", defaultQPS, "burst", defaultBurst)
		return defaultQPS, defaultBurst
	}

	qps := defaultQPS
	if config.QPS == nil {
		logger.Debug("Listener scaler qps is missing; using default", "default", defaultQPS)
	} else if *config.QPS < 1 {
		logger.Warn("Listener scaler qps must be greater than 0; using default", "configured", *config.QPS, "default", defaultQPS)
	} else {
		qps = *config.QPS
	}

	burst := defaultBurst
	if config.Burst == nil {
		logger.Debug("Listener scaler burst is missing; using default", "default", defaultBurst)
	} else if *config.Burst < 1 {
		logger.Warn("Listener scaler burst must be greater than 0; using default", "configured", *config.Burst, "default", defaultBurst)
	} else {
		burst = *config.Burst
	}

	return qps, burst
}

func (w *Scaler) applyDefaults() error {
	if w.logger == nil {
		w.logger = slog.New(slog.DiscardHandler)
	}

	return nil
}

func (w *Scaler) Scale(ctx context.Context, message *scaleset.RunnerScaleSetMessage) error {
	if message != nil {
		if message.Statistics == nil {
			return fmt.Errorf("message statistics are required")
		}
		w.latestStatistics = message.Statistics
		w.recorder.RecordStatistics(message.Statistics)

		if len(message.JobStartedMessages) > 0 {
			w.dirty = true
		}
		for _, job := range message.JobCompletedMessages {
			if err := w.HandleJobCompleted(ctx, job); err != nil {
				return fmt.Errorf("failed to handle job completed: %w", err)
			}
		}
	}

	if w.latestStatistics == nil {
		return fmt.Errorf("initial message statistics are required")
	}
	desiredCount, err := w.HandleDesiredRunnerCount(ctx, w.latestStatistics.TotalAssignedJobs)
	if err != nil {
		return fmt.Errorf("failed to handle desired runner count: %w", err)
	}
	if message == nil {
		return nil
	}
	w.recorder.RecordDesiredRunners(desiredCount)

	if len(message.JobAvailableMessages) > 0 {
		ids := make([]int64, 0, len(message.JobAvailableMessages))
		for _, job := range message.JobAvailableMessages {
			ids = append(ids, job.RunnerRequestID)
		}
		w.logger.Info("Acquiring jobs", "count", len(ids))
		acquired, err := w.messageClient.AcquireJobs(ctx, ids)
		if err != nil {
			return fmt.Errorf("failed to acquire available jobs: %w", err)
		}
		w.logger.Info("Jobs acquired", "count", len(acquired))
	}

	// A full queue applies backpressure rather than growing without bound.
	for _, job := range message.JobStartedMessages {
		select {
		case w.jobs <- job:
			w.recorder.RecordJobStarted(job)
		case <-ctx.Done():
			return fmt.Errorf("failed to queue job started event: %w", ctx.Err())
		}
	}
	for _, job := range message.JobCompletedMessages {
		w.recorder.RecordJobCompleted(job)
	}
	return nil
}

// HandleJobStarted updates the job information for the ephemeral runner when a job is started.
// It takes a context and a jobInfo parameter which contains the details of the started job.
// This update marks the ephemeral runner so that the controller would have more context
// about the ephemeral runner that should not be deleted when scaling down.
// It returns an error if there is any issue with updating the job information.
func (w *Scaler) HandleJobStarted(ctx context.Context, jobInfo *scaleset.JobStarted) error {
	w.dirty = true
	return w.patchJobStarted(ctx, jobInfo)
}

func (w *Scaler) patchJobStarted(ctx context.Context, jobInfo *scaleset.JobStarted) error {
	w.logger.Info("Updating job info for the runner",
		"runnerName", jobInfo.RunnerName,
		"ownerName", jobInfo.OwnerName,
		"repoName", jobInfo.RepositoryName,
		"jobId", jobInfo.JobID,
		"workflowRef", jobInfo.JobWorkflowRef,
		"workflowRunId", jobInfo.WorkflowRunID,
		"jobDisplayName", jobInfo.JobDisplayName,
		"requestId", jobInfo.RunnerRequestID)

	original, err := json.Marshal(&v1alpha1.EphemeralRunner{})
	if err != nil {
		return fmt.Errorf("failed to marshal empty ephemeral runner: %w", err)
	}

	// Build patch with job fields
	patchRunner := &v1alpha1.EphemeralRunner{
		Status: v1alpha1.EphemeralRunnerStatus{
			JobRequestID:      jobInfo.RunnerRequestID,
			JobRepositoryName: fmt.Sprintf("%s/%s", jobInfo.OwnerName, jobInfo.RepositoryName),
			JobID:             jobInfo.JobID,
			WorkflowRunID:     jobInfo.WorkflowRunID,
			JobWorkflowRef:    jobInfo.JobWorkflowRef,
			JobDisplayName:    jobInfo.JobDisplayName,
		},
	}

	patch, err := json.Marshal(patchRunner)
	if err != nil {
		return fmt.Errorf("failed to marshal ephemeral runner patch: %w", err)
	}

	mergePatch, err := jsonpatch.CreateMergePatch(original, patch)
	if err != nil {
		return fmt.Errorf("failed to create merge patch json for ephemeral runner: %w", err)
	}

	w.logger.Info("Updating ephemeral runner with merge patch", "json", string(mergePatch))

	patchedStatus := &v1alpha1.EphemeralRunner{}
	err = w.jobClientset.RESTClient().
		Patch(types.MergePatchType).
		Prefix("apis", v1alpha1.GroupVersion.Group, v1alpha1.GroupVersion.Version).
		Namespace(w.config.EphemeralRunnerSetNamespace).
		Resource("ephemeralrunners").
		Name(jobInfo.RunnerName).
		SubResource("status").
		Body(mergePatch).
		Do(ctx).
		Into(patchedStatus)
	if err != nil {
		if kerrors.IsNotFound(err) {
			w.logger.Info("Ephemeral runner not found, skipping patching of ephemeral runner status", "runnerName", jobInfo.RunnerName)
			return nil
		}
		return fmt.Errorf("could not patch ephemeral runner status, patch JSON: %s, error: %w", string(mergePatch), err)
	}

	w.logger.Info("Ephemeral runner status updated with the merge patch successfully.")

	return nil
}

func (w *Scaler) HandleJobCompleted(ctx context.Context, msg *scaleset.JobCompleted) error {
	w.dirty = true
	return nil
}

// HandleDesiredRunnerCount publishes the desired runner count to the ephemeral runner set.
func (w *Scaler) HandleDesiredRunnerCount(ctx context.Context, count int) (int, error) {
	patchID := w.setDesiredWorkerState(count)

	original, err := json.Marshal(
		&v1alpha1.EphemeralRunnerSet{
			Spec: v1alpha1.EphemeralRunnerSetSpec{
				Replicas: -1,
				PatchID:  -1,
			},
		},
	)
	if err != nil {
		return 0, fmt.Errorf("failed to marshal empty ephemeral runner set: %w", err)
	}

	patch, err := json.Marshal(
		&v1alpha1.EphemeralRunnerSet{
			Spec: v1alpha1.EphemeralRunnerSetSpec{
				Replicas: w.targetRunners,
				PatchID:  patchID,
			},
		},
	)
	if err != nil {
		w.logger.Error("could not marshal patch ephemeral runner set", "error", err.Error())
		return 0, err
	}

	w.logger.Info("Compare", "original", string(original), "patch", string(patch))
	mergePatch, err := jsonpatch.CreateMergePatch(original, patch)
	if err != nil {
		return 0, fmt.Errorf("failed to create merge patch json for ephemeral runner set: %w", err)
	}

	w.logger.Info("Preparing EphemeralRunnerSet update", "json", string(mergePatch))

	patchedEphemeralRunnerSet := &v1alpha1.EphemeralRunnerSet{}
	err = w.scaleClientset.RESTClient().
		Patch(types.MergePatchType).
		Prefix("apis", v1alpha1.GroupVersion.Group, v1alpha1.GroupVersion.Version).
		Namespace(w.config.EphemeralRunnerSetNamespace).
		Resource("ephemeralrunnersets").
		Name(w.config.EphemeralRunnerSetName).
		Body([]byte(mergePatch)).
		Do(ctx).
		Into(patchedEphemeralRunnerSet)
	if err != nil {
		return 0, fmt.Errorf("could not patch ephemeral runner set, patch JSON: %s, error: %w", string(mergePatch), err)
	}

	w.logger.Info(
		"Ephemeral runner set scaled.",
		"namespace", w.config.EphemeralRunnerSetNamespace,
		"name", w.config.EphemeralRunnerSetName,
		"replicas", patchedEphemeralRunnerSet.Spec.Replicas,
	)
	return w.targetRunners, nil
}

// calculateDesiredState calculates the desired state of the worker based on the desired count and the the number of jobs completed.
func (w *Scaler) setDesiredWorkerState(count int) int {
	dirty := w.dirty
	w.dirty = false

	if w.patchSeq == math.MaxInt32 {
		w.patchSeq = 0
	}
	w.patchSeq++

	targetRunnerCount := min(w.config.MinRunners+count, w.config.MaxRunners)
	oldTargetRunners := w.targetRunners
	w.targetRunners = targetRunnerCount

	desiredPatchID := w.patchSeq
	if !dirty && targetRunnerCount == oldTargetRunners && targetRunnerCount == w.config.MinRunners {
		// If there were no events sent, and the target runner count
		// is the same as the last patched count, we can force the state.
		//
		// TODO: see to remove w.config.MinRunenrs from the equation, as it is not relevant to the decision of whether to patch or not.
		desiredPatchID = 0
	}

	w.logger.Info(
		"Calculated target runner count",
		"assigned job", count,
		"decision", targetRunnerCount,
		"min", w.config.MinRunners,
		"max", w.config.MaxRunners,
		"currentRunnerCount", w.targetRunners,
	)

	return desiredPatchID
}
