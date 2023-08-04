package worker

import (
	"context"
	"encoding/json"
	"fmt"
	"math"

	"github.com/actions/actions-runner-controller/apis/actions.github.com/v1alpha1"
	"github.com/actions/actions-runner-controller/github/actions"
	"github.com/actions/actions-runner-controller/logging"
	jsonpatch "github.com/evanphx/json-patch"
	"github.com/go-logr/logr"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

// message types
const (
	messageTypeJobAvailable = "JobAvailable"
	messageTypeJobAssigned  = "JobAssigned"
	messageTypeJobStarted   = "JobStarted"
	messageTypeJobCompleted = "JobCompleted"
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
	EphemeralRunnerSetNamespace string `split_words:"true"`
	EphemeralRunnerSetName      string `split_words:"true"`
	MaxRunners                  int    `split_words:"true"`
	MinRunners                  int    `split_words:"true"`
}

type Worker struct {
	clientset *kubernetes.Clientset
	config    Config
	lastPatch int
	logger    *logr.Logger
}

func NewKubernetesWorker(config Config, options ...Option) (*Worker, error) {
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

func (w *Worker) Do(ctx context.Context, msg *actions.RunnerScaleSetMessage) error {
	w.logger.Info("Processing message", "messageId", msg.MessageId, "messageType", msg.MessageType)
	if msg.Statistics == nil {
		return fmt.Errorf("invalid message: statistics is nil")
	}

	w.logger.Info("current runner scale set statistics.", "statistics", msg.Statistics)

	if msg.MessageType != "RunnerScaleSetJobMessages" {
		w.logger.Info("Skipping message", "messageType", msg.MessageType)
		return nil
	}

	var batchedMessages []json.RawMessage
	if err := json.Unmarshal([]byte(msg.Body), &batchedMessages); err != nil {
		return fmt.Errorf("failed to unmarshal batched messages: %w", err)
	}

	var availableJobs []int64
	for _, msg := range batchedMessages {
		var messageType actions.JobMessageType
		if err := json.Unmarshal(msg, &messageType); err != nil {
			return fmt.Errorf("failed to decode job message type: %w", err)
		}

		switch messageType.MessageType {
		case messageTypeJobAvailable:
			var jobAvailable actions.JobAvailable
			if err := json.Unmarshal(msg, &jobAvailable); err != nil {
				return fmt.Errorf("failed to decode job available: %w", err)
			}

			w.logger.Info("Job available message received", "jobId", jobAvailable.RunnerRequestId)
			availableJobs = append(availableJobs, jobAvailable.RunnerRequestId)

		case messageTypeJobAssigned:
			var jobAssigned actions.JobAssigned
			if err := json.Unmarshal(msg, &jobAssigned); err != nil {
				return fmt.Errorf("failed to decode job assigned: %w", err)
			}

			w.logger.Info("Job assigned message received", "jobId", jobAssigned.RunnerRequestId)

		case messageTypeJobStarted:
			var jobStarted actions.JobStarted
			if err := json.Unmarshal(msg, &jobStarted); err != nil {
				return fmt.Errorf("could not decode job started message. %w", err)
			}
			w.logger.Info("job started message received.", "RequestId", jobStarted.RunnerRequestId, "RunnerId", jobStarted.RunnerId)
			if err := w.updateRunnerWithJobInfo(ctx, jobStarted); err != nil {
				return fmt.Errorf("failed to update runner with job info: %w", err)
			}
		case messageTypeJobCompleted:
			var jobCompleted actions.JobCompleted
			if err := json.Unmarshal(msg, &jobCompleted); err != nil {
				return fmt.Errorf("failed to decode job completed: %w", err)
			}

			w.logger.Info("job completed message received.", "RequestId", jobCompleted.RunnerRequestId, "Result", jobCompleted.Result, "RunnerId", jobCompleted.RunnerId, "RunnerName", jobCompleted.RunnerName)

		default:
			w.logger.Info("unknown job message type.", "messageType", messageType.MessageType)
		}
	}

	return nil
}

func (w *Worker) updateRunnerWithJobInfo(ctx context.Context, jobInfo actions.JobStarted) error {
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

func (w *Worker) updateDesiredRunners(ctx context.Context, count int) error {
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
		Prefix("apis", "actions.github.com", "v1alpha1").
		Namespace(w.config.EphemeralRunnerSetNamespace).
		Resource("EphemeralRunnerSets").
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
