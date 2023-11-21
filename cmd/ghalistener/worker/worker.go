package worker

import (
	"context"
	"encoding/json"
	"fmt"
	"math"

	"github.com/actions/actions-runner-controller/apis/actions.github.com/v1alpha1"
	"github.com/actions/actions-runner-controller/cmd/ghalistener/listener"
	"github.com/actions/actions-runner-controller/github/actions"
	"github.com/actions/actions-runner-controller/logging"
	jsonpatch "github.com/evanphx/json-patch"
	"github.com/go-logr/logr"
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

type Worker struct {
	clientset *kubernetes.Clientset
	config    Config
	lastPatch int
	logger    *logr.Logger
}

var _ listener.Handler = (*Worker)(nil)

func New(config Config, options ...Option) (*Worker, error) {
	w := &Worker{
		config:    config,
		lastPatch: -1,
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
		return fmt.Errorf("could not patch ephemeral runner status, patch JSON: %s, error: %w", string(mergePatch), err)
	}

	w.logger.Info("Ephemeral runner status updated with the merge patch successfully.")

	return nil
}

func (w *Worker) HandleDesiredRunnerCount(ctx context.Context, count int) error {
	targetRunnerCount := int(math.Max(math.Min(float64(w.config.MaxRunners), float64(count)), float64(w.config.MinRunners)))

	logValues := []any{
		"assigned job", count,
		"decision", targetRunnerCount,
		"min", w.config.MinRunners,
		"max", w.config.MaxRunners,
		"currentRunnerCount", w.lastPatch,
	}

	if targetRunnerCount == w.lastPatch {
		w.logger.Info("Skipping patching of EphemeralRunnerSet as the desired count has not changed", logValues...)
		return nil
	}

	original, err := json.Marshal(
		&v1alpha1.EphemeralRunnerSet{
			Spec: v1alpha1.EphemeralRunnerSetSpec{
				Replicas: -1,
			},
		},
	)
	if err != nil {
		return fmt.Errorf("failed to marshal empty ephemeral runner set: %w", err)
	}

	patch, err := json.Marshal(
		&v1alpha1.EphemeralRunnerSet{
			Spec: v1alpha1.EphemeralRunnerSetSpec{
				Replicas: count,
			},
		},
	)
	if err != nil {
		w.logger.Error(err, "could not marshal patch ephemeral runner set")
		return err
	}

	mergePatch, err := jsonpatch.CreateMergePatch(original, patch)
	if err != nil {
		return fmt.Errorf("failed to create merge patch json for ephemeral runner set: %w", err)
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
		return fmt.Errorf("could not patch ephemeral runner set , patch JSON: %s, error: %w", string(mergePatch), err)
	}

	w.logger.Info("Ephemeral runner set scaled.",
		"namespace", w.config.EphemeralRunnerSetNamespace,
		"name", w.config.EphemeralRunnerSetName,
		"replicas", patchedEphemeralRunnerSet.Spec.Replicas,
	)
	return nil
}
