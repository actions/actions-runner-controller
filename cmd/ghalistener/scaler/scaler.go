package scaler

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"math"

	"github.com/actions/actions-runner-controller/apis/actions.github.com/v1alpha1"
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
)

// The Scaler's role is to process the messages it receives from the listener.
// It then initiates Kubernetes API requests to carry out the necessary actions.
type Scaler struct {
	clientset     *kubernetes.Clientset
	config        Config
	targetRunners int
	patchSeq      int
	// dirty is set when there are any events handled before the desired count is called.
	dirty  bool
	logger *slog.Logger
}

var _ listener.Scaler = (*Scaler)(nil)

func New(config Config, options ...Option) (*Scaler, error) {
	w := &Scaler{
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

func (w *Scaler) applyDefaults() error {
	if w.logger == nil {
		w.logger = slog.New(slog.DiscardHandler)
	}

	return nil
}

// HandleJobStarted updates the job information for the ephemeral runner when a job is started.
// It takes a context and a jobInfo parameter which contains the details of the started job.
// This update marks the ephemeral runner so that the controller would have more context
// about the ephemeral runner that should not be deleted when scaling down.
// It also transitions the phase to Running if the runner is not in a terminal state.
// It returns an error if there is any issue with updating the job information.
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

	w.dirty = true

	// The phase transition below is decided from the runner state observed by a
	// read, so the write has to be guarded against a concurrent update by the
	// ephemeral runner controller. On conflict the runner is re-read and the
	// decision is made again against the fresh state.
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
		Resource("EphemeralRunners").
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
		Resource("EphemeralRunners").
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

// HandleDesiredRunnerCount handles the desired runner count by scaling the ephemeral runner set.
// The function calculates the target runner count based on the minimum and maximum runner count configuration.
// If the target runner count is the same as the last patched count, it skips patching and returns nil.
// Otherwise, it creates a merge patch JSON for updating the ephemeral runner set with the desired count.
// The function then scales the ephemeral runner set by applying the merge patch.
// Finally, it logs the scaled ephemeral runner set details and returns nil if successful.
// If any error occurs during the process, it returns an error with a descriptive message.
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
