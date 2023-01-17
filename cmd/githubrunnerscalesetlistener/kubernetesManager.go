package main

import (
	"context"
)

//go:generate mockery --inpackage --name=KubernetesManager
type KubernetesManager interface {
	ScaleEphemeralRunnerSet(ctx context.Context, namespace, resourceName string, runnerCount int) error

	UpdateEphemeralRunnerWithJobInfo(ctx context.Context, namespace, resourceName, ownerName, repositoryName, jobWorkflowRef, jobDisplayName string, jobRequestId, workflowRunId int64) error
}
