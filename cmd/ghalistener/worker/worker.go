package worker

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/actions/actions-runner-controller/apis/actions.github.com/v1alpha1"
	"github.com/actions/actions-runner-controller/cmd/ghalistener/listener"
	"github.com/actions/actions-runner-controller/github/actions"
	"github.com/actions/actions-runner-controller/logging"
	jsonpatch "github.com/evanphx/json-patch"
	"github.com/go-logr/logr"
	kerrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

const workerName = "kubernetesworker"

type Option func(*Worker)

func WithLogger(logger logr.Logger) Option {
	return func(w *Worker) {
		logger = logger.WithName(workerName)
		w.logger = &logger
	}
}

type Config struct {
	EphemeralRunnerSetNamespace string
	EphemeralRunnerSetName      string
	MaxRunners                  int
	MinRunners                  int
}

// The Worker's role is to process the messages it receives from the listener.
// It then initiates Kubernetes API requests to carry out the necessary actions.
type Worker struct {
	clientset   *kubernetes.Clientset
	config      Config
	lastPatch   int
	lastPatchID int
	logger      *logr.Logger
}

var _ listener.Handler = (*Worker)(nil)

func New(config Config, options ...Option) (*Worker, error) {
	w := &Worker{
		config:      config,
		lastPatch:   -1,
		lastPatchID: -1,
	}

	conf, err := rest.InClusterConfig()
	if err != nil {
		return nil, err
	}

	clientset, err := kubernetes.NewForConfig(conf)
	if err != nil {
		return nil, err
	}

	w.clientset = clientset

	for _, option := range options {
		option(w)
	}

	if err := w.applyDefaults(); err != nil {
		return nil, err
	}

	return w, nil
}

func (w *Worker) applyDefaults() error {
	if w.logger == nil {
		logger, err := logging.NewLogger(logging.LogLevelDebug, logging.LogFormatJSON)
		if err != nil {
			return fmt.Errorf("NewLogger failed: %w", err)
		}
		logger = logger.WithName(workerName)
		w.logger = &logger
	}

	return nil
}

// HandleJobStarted updates the job information for the ephemeral runner when a job is started.
// It takes a context and a jobInfo parameter which contains the details of the started job.
// This update marks the ephemeral runner so that the controller would have more context
// about the ephemeral runner that should not be deleted when scaling down.
// It returns an error if there is any issue with updating the job information.
func (w *Worker) HandleJobStarted(ctx context.Context, jobInfo *actions.JobStarted) error {
	w.logger.Info("Updating job info for the runner",
		"runnerName", jobInfo.RunnerName,
		"ownerName", jobInfo.OwnerName,
		"repoName", jobInfo.RepositoryName,
		"workflowRef", jobInfo.JobWorkflowRef,
		"workflowRunId", jobInfo.WorkflowRunId,
		"jobDisplayName", jobInfo.JobDisplayName,
		"requestId", jobInfo.RunnerRequestId)

	original, err := json.Marshal(&v1alpha1.EphemeralRunner{})
	if err != nil {
		return fmt.Errorf("failed to marshal empty ephemeral runner: %w", err)
	}

	patch, err := json.Marshal(
		&v1alpha1.EphemeralRunner{
			Status: v1alpha1.EphemeralRunnerStatus{
				JobRequestId:      jobInfo.RunnerRequestId,
				JobRepositoryName: fmt.Sprintf("%s/%s", jobInfo.OwnerName, jobInfo.RepositoryName),
				WorkflowRunId:     jobInfo.WorkflowRunId,
				JobWorkflowRef:    jobInfo.JobWorkflowRef,
				JobDisplayName:    jobInfo.JobDisplayName,
			},
		},
	)
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

// HandleDesiredRunnerCount handles the desired runner count by scaling the ephemeral runner set.
// The function calculates the target runner count based on the minimum and maximum runner count configuration.
// If the target runner count is the same as the last patched count, it skips patching and returns nil.
// Otherwise, it creates a merge patch JSON for updating the ephemeral runner set with the desired count.
// The function then scales the ephemeral runner set by applying the merge patch.
// Finally, it logs the scaled ephemeral runner set details and returns nil if successful.
// If any error occurs during the process, it returns an error with a descriptive message.
func (w *Worker) HandleDesiredRunnerCount(ctx context.Context, count int, jobsCompleted int) (int, error) {
	// Max runners should always be set by the resource builder either to the configured value,
	// or the maximum int32 (resourcebuilder.newAutoScalingListener()).
	targetRunnerCount := min(w.config.MinRunners+count, w.config.MaxRunners)

	logValues := []any{
		"assigned job", count,
		"decision", targetRunnerCount,
		"min", w.config.MinRunners,
		"max", w.config.MaxRunners,
		"currentRunnerCount", w.lastPatch,
		"jobsCompleted", jobsCompleted,
	}

	if w.lastPatch == targetRunnerCount && jobsCompleted == 0 {
		w.logger.Info("Skipping patch", logValues...)
		return targetRunnerCount, nil
	}

	w.lastPatchID++
	w.lastPatch = targetRunnerCount

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
				Replicas: targetRunnerCount,
				PatchID:  w.lastPatchID,
			},
		},
	)
	if err != nil {
		w.logger.Error(err, "could not marshal patch ephemeral runner set")
		return 0, err
	}

	mergePatch, err := jsonpatch.CreateMergePatch(original, patch)
	if err != nil {
		return 0, fmt.Errorf("failed to create merge patch json for ephemeral runner set: %w", err)
	}

	w.logger.Info("Created merge patch json for EphemeralRunnerSet update", "json", string(mergePatch))

	w.logger.Info("Scaling ephemeral runner set", logValues...)

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
		return 0, fmt.Errorf("could not patch ephemeral runner set , patch JSON: %s, error: %w", string(mergePatch), err)
	}

	w.logger.Info("Ephemeral runner set scaled.",
		"namespace", w.config.EphemeralRunnerSetNamespace,
		"name", w.config.EphemeralRunnerSetName,
		"replicas", patchedEphemeralRunnerSet.Spec.Replicas,
	)
	return targetRunnerCount, nil
}
