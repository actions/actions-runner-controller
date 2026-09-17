package scaler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math"

	"github.com/actions/actions-runner-controller/apis/actions.github.com/v1alpha1"
	"github.com/actions/actions-runner-controller/cmd/ghalistener/metrics"
	"github.com/actions/scaleset"
	"github.com/actions/scaleset/listener"
	jsonpatch "github.com/evanphx/json-patch"
	"golang.org/x/sync/errgroup"
	kerrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/util/retry"
)

type Option func(*Scaler)

func WithLogger(logger *slog.Logger) Option {
	return func(w *Scaler) {
		w.logger = logger
	}
}

// WithMetrics sets the recorder the scaler publishes listener metrics to. The
// listener used to own this, but it no longer inspects the messages it hands
// over, so recording moved to the only component that still reads them.
// Passing nil keeps the existing recorder.
func WithMetrics(recorder metrics.Recorder) Option {
	return func(w *Scaler) {
		if recorder == nil {
			return
		}
		w.metrics = recorder
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
	defaultQPS   = 50
	defaultBurst = 100
	// defaultWorkers bounds how many job events are patched at once. Each event
	// costs at most a GET and a PATCH, so the default stays well inside the
	// default QPS budget while still collapsing a batch of events into a few
	// round trips worth of latency.
	defaultWorkers = 10
)

// JobAcquirer acquires jobs from the Actions service. The listener no longer
// acquires on the scaler's behalf, so every job the scaler wants has to be
// passed to AcquireJobs or it stays unassigned. listener.Client satisfies it.
type JobAcquirer interface {
	AcquireJobs(ctx context.Context, requestIDs []int64) ([]int64, error)
}

// The Scaler's role is to process the messages it receives from the listener.
// It then initiates Kubernetes API requests to carry out the necessary actions.
type Scaler struct {
	clientset     *kubernetes.Clientset
	client        JobAcquirer
	config        Config
	metrics       metrics.Recorder
	workers       int
	targetRunners int
	patchSeq      int
	// dirty is set when there are any events handled before the desired count is called.
	dirty bool
	// lastStatistics is the most recent statistics the service published. The
	// listener stopped caching them, and a long poll that times out carries no
	// message at all, so the scaler keeps them to stay able to converge on an
	// otherwise idle scale set.
	lastStatistics *scaleset.RunnerScaleSetStatistic
	logger         *slog.Logger
}

var _ listener.Scaler = (*Scaler)(nil)

func New(client JobAcquirer, config Config, options ...Option) (*Scaler, error) {
	if client == nil {
		return nil, errors.New("client is required")
	}

	w := &Scaler{
		client:        client,
		config:        config,
		targetRunners: -1,
		patchSeq:      -1,
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

	qps, burst := effectiveRateLimiterConfig(config.ScalerConfig, w.logger)
	conf.QPS = float32(qps)
	conf.Burst = burst
	w.workers = effectiveWorkerCount(config.ScalerConfig, w.logger)

	clientset, err := kubernetes.NewForConfig(conf)
	if err != nil {
		return nil, err
	}

	w.clientset = clientset

	return w, nil
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

func effectiveWorkerCount(config *v1alpha1.ScalerConfig, logger *slog.Logger) int {
	if config == nil || config.Workers == nil {
		logger.Debug("Listener scaler workers is missing; using default", "default", defaultWorkers)
		return defaultWorkers
	}

	if *config.Workers < 1 {
		logger.Warn("Listener scaler workers must be greater than 0; using default", "configured", *config.Workers, "default", defaultWorkers)
		return defaultWorkers
	}

	return *config.Workers
}

func (w *Scaler) applyDefaults() error {
	if w.logger == nil {
		w.logger = slog.New(slog.DiscardHandler)
	}

	if w.metrics == nil {
		w.metrics = metrics.Discard
	}

	if w.workers < 1 {
		w.workers = defaultWorkers
	}

	return nil
}

// Scale handles a single scale set message.
//
// The listener owns session management, polling and acking only; everything the
// message asks for is done here. It acks the message once Scale returns nil and
// redelivers it otherwise, so every step below is idempotent and safe to repeat
// after a partially applied message.
//
// The work is split across workers: one patches the EphemeralRunnerSet with the
// desired replica count, the rest patch the EphemeralRunner behind each job
// started or job completed event. The events touch distinct resources and carry
// no ordering between them, so they run concurrently instead of being replayed
// one API call at a time.
func (w *Scaler) Scale(ctx context.Context, msg *scaleset.RunnerScaleSetMessage) error {
	if msg == nil {
		// The long poll timed out without any activity. There is nothing to
		// handle, but the desired count is still republished so a scale set that
		// went quiet mid-scale keeps converging.
		if w.lastStatistics == nil {
			return nil
		}
		return w.patchDesiredRunnerCount(ctx, w.setDesiredWorkerState(w.lastStatistics.TotalAssignedJobs))
	}

	if msg.Statistics != nil {
		w.lastStatistics = msg.Statistics
		w.metrics.RecordStatistics(msg.Statistics)
	}

	// Acquire first so the jobs are assigned as early as possible. Acquiring a
	// job that is already acquired is a no-op, so a redelivered message repeats
	// this safely.
	if err := w.acquireAvailableJobs(ctx, msg.JobAvailableMessages); err != nil {
		return err
	}

	if len(msg.JobStartedMessages) > 0 || len(msg.JobCompletedMessages) > 0 {
		w.dirty = true
	}

	// The scale decision is computed up front, on the goroutine that owns the
	// scaler state, so the scaling worker never races the event workers for it.
	scaleRequested := msg.Statistics != nil
	var patchID int
	var scalesDown bool
	if scaleRequested {
		previousTarget := w.targetRunners
		patchID = w.setDesiredWorkerState(msg.Statistics.TotalAssignedJobs)
		scalesDown = previousTarget >= 0 && w.targetRunners < previousTarget
	}

	// A patch that lowers the replica count can make the runner set controller
	// delete idle runners, and it only skips a runner that already carries a job
	// request ID. Publishing it before the job started patches land could
	// therefore offer up a runner that just picked up a job, so the scaling
	// worker waits for them in that case. A patch that scales up or holds cannot
	// delete anything, so it runs alongside the event workers.
	scaleConcurrently := scaleRequested && !scalesDown

	g, gctx := errgroup.WithContext(ctx)
	limit := w.workers
	if scaleConcurrently {
		limit++ // the scaling worker gets a slot of its own
	}
	g.SetLimit(limit)

	if scaleConcurrently {
		g.Go(func() error {
			return w.patchDesiredRunnerCount(gctx, patchID)
		})
	}

	for _, jobStarted := range msg.JobStartedMessages {
		g.Go(func() error {
			w.metrics.RecordJobStarted(jobStarted)
			return w.HandleJobStarted(gctx, jobStarted)
		})
	}

	for _, jobCompleted := range msg.JobCompletedMessages {
		g.Go(func() error {
			w.metrics.RecordJobCompleted(jobCompleted)
			return w.HandleJobCompleted(gctx, jobCompleted)
		})
	}

	if err := g.Wait(); err != nil {
		return err
	}

	if scaleRequested && !scaleConcurrently {
		return w.patchDesiredRunnerCount(ctx, patchID)
	}

	return nil
}

// acquireAvailableJobs assigns every available job to this scale set. A job that
// is not acquired is never assigned, and the service hands the same job to this
// scale set again, so acquiring one twice is a no-op.
func (w *Scaler) acquireAvailableJobs(ctx context.Context, jobsAvailable []*scaleset.JobAvailable) error {
	if len(jobsAvailable) == 0 {
		return nil
	}

	ids := make([]int64, 0, len(jobsAvailable))
	for _, job := range jobsAvailable {
		ids = append(ids, job.RunnerRequestID)
	}

	w.logger.Info("Acquiring jobs", "count", len(ids))

	acquired, err := w.client.AcquireJobs(ctx, ids)
	if err != nil {
		return fmt.Errorf("failed to acquire jobs: %w", err)
	}

	w.logger.Info("Jobs acquired", "count", len(acquired), "requested", len(ids))
	return nil
}

// HandleJobStarted updates the job information for the ephemeral runner when a job is started.
// It takes a context and a jobInfo parameter which contains the details of the started job.
// This update marks the ephemeral runner so that the controller would have more context
// about the ephemeral runner that should not be deleted when scaling down.
// It also transitions the phase to Running if the runner is not in a terminal state.
// It returns an error if there is any issue with updating the job information.
//
// It is called from a worker goroutine, once per job started event in a message,
// and only ever touches the runner named by its own event.
func (w *Scaler) HandleJobStarted(ctx context.Context, jobInfo *scaleset.JobStarted) error {
	w.logger.Info("Updating job info for the runner",
		"runnerName", jobInfo.RunnerName,
		"ownerName", jobInfo.OwnerName,
		"repoName", jobInfo.RepositoryName,
		"jobId", jobInfo.JobID,
		"workflowRef", jobInfo.JobWorkflowRef,
		"workflowRunId", jobInfo.WorkflowRunID,
		"jobDisplayName", jobInfo.JobDisplayName,
		"requestId", jobInfo.RunnerRequestID)

	// The promotion to Running is guarded by an optimistic lock on the resource version
	// observed by the GET below, so a terminal phase written between the read and the
	// patch is never clobbered. Conflicts are retried against freshly read state.
	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		return w.patchJobStarted(ctx, jobInfo)
	})
}

func (w *Scaler) patchJobStarted(ctx context.Context, jobInfo *scaleset.JobStarted) error {
	// Fetch current EphemeralRunner to check phase and deletion status
	currentRunner := &v1alpha1.EphemeralRunner{}
	err := w.clientset.RESTClient().
		Get().
		Prefix("apis", v1alpha1.GroupVersion.Group, v1alpha1.GroupVersion.Version).
		Namespace(w.config.EphemeralRunnerSetNamespace).
		Resource("ephemeralrunners").
		Name(jobInfo.RunnerName).
		Do(ctx).
		Into(currentRunner)
	if err != nil {
		if kerrors.IsNotFound(err) {
			w.logger.Info("Ephemeral runner not found, skipping job info update", "runnerName", jobInfo.RunnerName)
			return nil
		}
		return fmt.Errorf("failed to get ephemeral runner: %w", err)
	}

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

	// Only set Running phase if current phase is not terminal/failure and deletion is not in progress.
	//
	// The phase is the only field derived from the state read above, so the observed
	// resourceVersion is attached to the patch as a precondition. Without it, a terminal
	// phase written between the read and the patch would be silently overwritten with
	// Running, resurrecting a runner that already finished. The job fields carry no such
	// precondition: they are write-once metadata that the runner set only consults for
	// runners that are neither done nor being deleted, so patching them unconditionally
	// cannot change any scaling decision.
	if currentRunner.DeletionTimestamp == nil &&
		currentRunner.Status.Phase != v1alpha1.EphemeralRunnerPhaseFailed &&
		currentRunner.Status.Phase != v1alpha1.EphemeralRunnerPhaseSucceeded &&
		currentRunner.Status.Phase != v1alpha1.EphemeralRunnerPhaseOutdated {
		patchRunner.Status.Phase = v1alpha1.EphemeralRunnerPhaseRunning
		// Optimistic lock: reject the promotion if the runner changed since the GET.
		patchRunner.ResourceVersion = currentRunner.ResourceVersion
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
	err = w.clientset.RESTClient().
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
		if kerrors.IsConflict(err) {
			w.logger.Info("Ephemeral runner changed while patching job info, retrying", "runnerName", jobInfo.RunnerName)
			return err
		}
		return fmt.Errorf("could not patch ephemeral runner status, patch JSON: %s, error: %w", string(mergePatch), err)
	}

	w.logger.Info("Ephemeral runner status updated with the merge patch successfully.")

	return nil
}

// HandleJobCompleted records that a runner finished its job. The runner itself
// is torn down by the ephemeral runner controller once the pod exits, so there
// is nothing to patch here; the completion only has to be reflected in the
// desired count, which Scale already derives from the message.
//
// It is called from a worker goroutine, once per job completed event in a message.
func (w *Scaler) HandleJobCompleted(ctx context.Context, msg *scaleset.JobCompleted) error {
	w.logger.Info("Job completed",
		"runnerName", msg.RunnerName,
		"ownerName", msg.OwnerName,
		"repoName", msg.RepositoryName,
		"jobId", msg.JobID,
		"workflowRunId", msg.WorkflowRunID,
		"result", msg.Result,
		"requestId", msg.RunnerRequestID)
	return nil
}

// patchDesiredRunnerCount publishes the desired runner count computed by
// setDesiredWorkerState by patching the ephemeral runner set.
// The function creates a merge patch JSON for updating the ephemeral runner set with the desired count,
// then scales the ephemeral runner set by applying the merge patch.
// Finally, it logs the scaled ephemeral runner set details and returns nil if successful.
// If any error occurs during the process, it returns an error with a descriptive message.
func (w *Scaler) patchDesiredRunnerCount(ctx context.Context, patchID int) error {
	original, err := json.Marshal(
		&v1alpha1.EphemeralRunnerSet{
			Spec: v1alpha1.EphemeralRunnerSetSpec{
				Replicas: -1,
				PatchID:  -1,
			},
		},
	)
	if err != nil {
		return fmt.Errorf("failed to marshal empty ephemeral runner set: %w", err)
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
		return err
	}

	w.logger.Info("Compare", "original", string(original), "patch", string(patch))
	mergePatch, err := jsonpatch.CreateMergePatch(original, patch)
	if err != nil {
		return fmt.Errorf("failed to create merge patch json for ephemeral runner set: %w", err)
	}

	w.logger.Info("Preparing EphemeralRunnerSet update", "json", string(mergePatch))

	patchedEphemeralRunnerSet := &v1alpha1.EphemeralRunnerSet{}
	err = w.clientset.RESTClient().
		Patch(types.MergePatchType).
		Prefix("apis", v1alpha1.GroupVersion.Group, v1alpha1.GroupVersion.Version).
		Namespace(w.config.EphemeralRunnerSetNamespace).
		Resource("ephemeralrunnersets").
		Name(w.config.EphemeralRunnerSetName).
		Body([]byte(mergePatch)).
		Do(ctx).
		Into(patchedEphemeralRunnerSet)
	if err != nil {
		return fmt.Errorf("could not patch ephemeral runner set, patch JSON: %s, error: %w", string(mergePatch), err)
	}

	w.metrics.RecordDesiredRunners(w.targetRunners)

	w.logger.Info(
		"Ephemeral runner set scaled.",
		"namespace", w.config.EphemeralRunnerSetNamespace,
		"name", w.config.EphemeralRunnerSetName,
		"replicas", patchedEphemeralRunnerSet.Spec.Replicas,
	)
	return nil
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
