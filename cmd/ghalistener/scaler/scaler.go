package scaler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"sync"

	"github.com/actions/actions-runner-controller/apis/actions.github.com/v1alpha1"
	"github.com/actions/actions-runner-controller/cmd/ghalistener/metrics"
	"github.com/actions/scaleset"
	"github.com/actions/scaleset/listener"
	jsonpatch "github.com/evanphx/json-patch"
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
	// defaultScaleQPS and defaultScaleBurst budget the client that publishes the
	// desired runner count. That is one patch per message, so a small budget is
	// enough for it to never wait on a token. It is separate from the job client
	// rather than carved out of it: sharing one bucket is what let a batch of job
	// patches delay the scale patch in the first place.
	defaultScaleQPS   = 10
	defaultScaleBurst = 20
	// defaultWorkers bounds how many job started events are patched at once.
	// Each event costs at most a GET and a PATCH, and the rate limiter rather
	// than this number is what bounds sustained throughput, so this only has to
	// be large enough to keep the job client's tokens spoken for.
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
	// scaleClientset publishes the desired runner count and nothing else, so its
	// rate limiter is never drained by job event traffic.
	scaleClientset *kubernetes.Clientset
	// jobClientset patches job started events. It is used only by the background
	// workers, never by Scale.
	jobClientset  *kubernetes.Clientset
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

	// jobs holds job started events accepted from a message but not yet patched.
	jobs *jobQueue
	// jobWorkers tracks the pool draining jobs.
	jobWorkers sync.WaitGroup
	// jobCtx scopes the background patches. It is rooted at context.Background()
	// rather than at any message's context: the patches outlive the message that
	// produced them, so cancelling that message must not abandon them.
	jobCtx    context.Context
	jobCancel context.CancelFunc
	closeOnce sync.Once

	logger *slog.Logger
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
		jobs:          newJobQueue(),
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

	w.workers = effectiveWorkerCount(config.ScalerConfig, w.logger)

	jobQPS, jobBurst := effectiveRateLimiterConfig(config.ScalerConfig, w.logger)
	jobConf := rest.CopyConfig(conf)
	jobConf.QPS = float32(jobQPS)
	jobConf.Burst = jobBurst
	jobClientset, err := kubernetes.NewForConfig(jobConf)
	if err != nil {
		return nil, err
	}

	scaleQPS, scaleBurst := effectiveScaleRateLimiterConfig(config.ScalerConfig, w.logger)
	scaleConf := rest.CopyConfig(conf)
	scaleConf.QPS = float32(scaleQPS)
	scaleConf.Burst = scaleBurst
	scaleClientset, err := kubernetes.NewForConfig(scaleConf)
	if err != nil {
		return nil, err
	}

	w.jobClientset = jobClientset
	w.scaleClientset = scaleClientset

	w.startJobWorkers()

	return w, nil
}

// startJobWorkers brings up the pool that drains the job started queue.
func (w *Scaler) startJobWorkers() {
	w.jobCtx, w.jobCancel = context.WithCancel(context.Background())

	for range w.workers {
		w.jobWorkers.Add(1)
		go func() {
			defer w.jobWorkers.Done()

			for {
				jobInfo, ok := w.jobs.pop()
				if !ok {
					return
				}

				// Recorded here rather than at enqueue time so the metric and the
				// patch describe the same moment.
				w.metrics.RecordJobStarted(jobInfo)

				if err := w.HandleJobStarted(w.jobCtx, jobInfo); err != nil {
					// The message this event arrived on has already been acked, so
					// there is nobody left to return the error to. Losing the patch
					// costs a stale Status.JobID: the runner set may try to delete
					// the runner as idle, and the Actions service rejects that while
					// the job is still running, so the job itself is not at risk.
					w.logger.Error("Failed to patch job started event",
						"runnerName", jobInfo.RunnerName,
						"requestId", jobInfo.RunnerRequestID,
						"error", err.Error(),
					)
				}
			}
		}()
	}
}

// Close drains the job started queue and stops the workers. Everything already
// accepted from an acked message is patched first, so a clean shutdown does not
// strand job information the service believes was recorded.
//
// If ctx expires before the queue drains, the remaining patches are abandoned
// and any in-flight request is cancelled.
func (w *Scaler) Close(ctx context.Context) error {
	w.closeOnce.Do(func() {
		w.jobs.close()
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
		if depth := w.jobs.depth(); depth > 0 {
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

// effectiveScaleRateLimiterConfig resolves the budget for the client that
// publishes the desired runner count.
func effectiveScaleRateLimiterConfig(config *v1alpha1.ScalerConfig, logger *slog.Logger) (int, int) {
	if config == nil {
		logger.Debug("Listener scaler configuration is missing; using defaults", "scaleQPS", defaultScaleQPS, "scaleBurst", defaultScaleBurst)
		return defaultScaleQPS, defaultScaleBurst
	}

	qps := defaultScaleQPS
	if config.ScaleQPS == nil {
		logger.Debug("Listener scaler scaleQPS is missing; using default", "default", defaultScaleQPS)
	} else if *config.ScaleQPS < 1 {
		logger.Warn("Listener scaler scaleQPS must be greater than 0; using default", "configured", *config.ScaleQPS, "default", defaultScaleQPS)
	} else {
		qps = *config.ScaleQPS
	}

	burst := defaultScaleBurst
	if config.ScaleBurst == nil {
		logger.Debug("Listener scaler scaleBurst is missing; using default", "default", defaultScaleBurst)
	} else if *config.ScaleBurst < 1 {
		logger.Warn("Listener scaler scaleBurst must be greater than 0; using default", "configured", *config.ScaleBurst, "default", defaultScaleBurst)
	} else {
		burst = *config.ScaleBurst
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
// The order is chosen so that nothing the runner set is waiting on sits behind
// work it is not waiting on:
//
//  1. The desired runner count is published. It is the only patch that creates
//     runners, so it goes out on its own client before anything else can consume
//     a rate limit token.
//  2. The job started events are handed to the background pool. They are
//     bookkeeping rather than something new jobs wait on, and at two API calls
//     per event they are what a large batch would otherwise spend the whole
//     message on.
//  3. The available jobs are acquired. This is a single call to the Actions
//     service, and it stays here, ahead of the ack, because a job that is never
//     acquired is never assigned; unlike the patches above, losing it is not
//     something a later message repairs.
//
// Acquiring last rather than first costs the round trip of the scale patch in
// acquisition delay and saves the entire job patch batch in scale latency. The
// scale decision itself is unaffected either way: it is derived from
// msg.Statistics, a snapshot taken by the service when the message was built,
// and jobs acquired now are reported as assigned in a later message.
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

	if len(msg.JobStartedMessages) > 0 || len(msg.JobCompletedMessages) > 0 {
		w.dirty = true
	}

	if msg.Statistics != nil {
		if err := w.patchDesiredRunnerCount(ctx, w.setDesiredWorkerState(msg.Statistics.TotalAssignedJobs)); err != nil {
			return err
		}
	}

	// A completed job has nothing to patch: the runner is torn down by the
	// ephemeral runner controller once its pod exits, and the completion is
	// already reflected in the desired count published above. It is handled
	// inline because it costs no API call.
	for _, jobCompleted := range msg.JobCompletedMessages {
		w.metrics.RecordJobCompleted(jobCompleted)
		if err := w.HandleJobCompleted(ctx, jobCompleted); err != nil {
			return err
		}
	}

	// Queued rather than awaited. The listener acks as soon as this returns, so
	// these patches outlive the message, which is why the workers run on their
	// own context and the queue is drained by Close rather than here.
	w.jobs.push(msg.JobStartedMessages...)

	if depth := w.jobs.depth(); depth > 0 {
		w.logger.Info("Job started events queued for patching", "depth", depth)
	}

	return w.acquireAvailableJobs(ctx, msg.JobAvailableMessages)
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
// It is called from a background worker rather than from Scale, once per job
// started event, and only ever touches the runner named by its own event.
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
	err := w.jobClientset.RESTClient().
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
// It is called inline from Scale, once per job completed event.
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
