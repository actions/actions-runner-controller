package main

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/actions/actions-runner-controller/apis/actions.github.com/v1alpha1"
	jsonpatch "github.com/evanphx/json-patch"
	"github.com/go-logr/logr"
	"go.opentelemetry.io/otel"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

type AutoScalerKubernetesManager struct {
	*kubernetes.Clientset

	logger logr.Logger
}

func NewKubernetesManager(logger *logr.Logger) (*AutoScalerKubernetesManager, error) {
	conf, err := rest.InClusterConfig()
	if err != nil {
		return nil, err
	}

	kubeClient, err := kubernetes.NewForConfig(conf)
	if err != nil {
		return nil, err
	}

	var manager = &AutoScalerKubernetesManager{
		Clientset: kubeClient,
		logger:    logger.WithName("KubernetesManager"),
	}
	return manager, nil
}

func (k *AutoScalerKubernetesManager) ScaleEphemeralRunnerSet(ctx context.Context, namespace, resourceName string, runnerCount int) error {
	ctx, span := otel.Tracer("arc").Start(ctx, "AutoScalerKubernetesManager.ScaleEphemeralRunnerSet")
	defer span.End()

	original := &v1alpha1.EphemeralRunnerSet{
		Spec: v1alpha1.EphemeralRunnerSetSpec{
			Replicas: -1,
		},
	}
	originalJson, err := json.Marshal(original)
	if err != nil {
		k.logger.Error(err, "could not marshal empty ephemeral runner set")
	}

	patch := &v1alpha1.EphemeralRunnerSet{
		Spec: v1alpha1.EphemeralRunnerSetSpec{
			Replicas: runnerCount,
		},
	}
	patchJson, err := json.Marshal(patch)
	if err != nil {
		k.logger.Error(err, "could not marshal patch ephemeral runner set")
	}
	mergePatch, err := jsonpatch.CreateMergePatch(originalJson, patchJson)
	if err != nil {
		k.logger.Error(err, "could not create merge patch json for ephemeral runner set")
	}

	k.logger.Info("Created merge patch json for EphemeralRunnerSet update", "json", string(mergePatch))

	patchedEphemeralRunnerSet := &v1alpha1.EphemeralRunnerSet{}
	err = k.RESTClient().
		Patch(types.MergePatchType).
		Prefix("apis", "actions.github.com", "v1alpha1").
		Namespace(namespace).
		Resource("EphemeralRunnerSets").
		Name(resourceName).
		Body([]byte(mergePatch)).
		Do(ctx).
		Into(patchedEphemeralRunnerSet)
	if err != nil {
		return fmt.Errorf("could not patch ephemeral runner set , patch JSON: %s, error: %w", string(mergePatch), err)
	}

	k.logger.Info("Ephemeral runner set scaled.", "namespace", namespace, "name", resourceName, "replicas", patchedEphemeralRunnerSet.Spec.Replicas)
	return nil
}

func (k *AutoScalerKubernetesManager) UpdateEphemeralRunnerWithJobInfo(ctx context.Context, namespace, resourceName, ownerName, repositoryName, jobWorkflowRef, jobDisplayName string, workflowRunId, jobRequestId int64) error {
	ctx, span := otel.Tracer("arc").Start(ctx, "AutoScalerKubernetesManager.UpdateEphemeralRunnerWithJobInfo")
	defer span.End()

	original := &v1alpha1.EphemeralRunner{}
	originalJson, err := json.Marshal(original)
	if err != nil {
		return fmt.Errorf("could not marshal empty ephemeral runner, error: %w", err)
	}

	patch := &v1alpha1.EphemeralRunner{
		Status: v1alpha1.EphemeralRunnerStatus{
			JobRequestId:      jobRequestId,
			JobRepositoryName: fmt.Sprintf("%s/%s", ownerName, repositoryName),
			WorkflowRunId:     workflowRunId,
			JobWorkflowRef:    jobWorkflowRef,
			JobDisplayName:    jobDisplayName,
		},
	}
	patchedJson, err := json.Marshal(patch)
	if err != nil {
		return fmt.Errorf("could not marshal patched ephemeral runner, error: %w", err)
	}

	mergePatch, err := jsonpatch.CreateMergePatch(originalJson, patchedJson)
	if err != nil {
		k.logger.Error(err, "could not create merge patch json for ephemeral runner")
	}

	k.logger.Info("Created merge patch json for EphemeralRunner status update", "json", string(mergePatch))

	patchedStatus := &v1alpha1.EphemeralRunner{}
	err = k.RESTClient().
		Patch(types.MergePatchType).
		Prefix("apis", "actions.github.com", "v1alpha1").
		Namespace(namespace).
		Resource("EphemeralRunners").
		Name(resourceName).
		SubResource("status").
		Body(mergePatch).
		Do(ctx).
		Into(patchedStatus)
	if err != nil {
		return fmt.Errorf("could not patch ephemeral runner status, patch JSON: %s, error: %w", string(mergePatch), err)
	}

	return nil
}
