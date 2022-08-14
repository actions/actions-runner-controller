/*
Copyright 2020 The actions-runner-controller authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// HorizontalRunnerAutoscalerSpec defines the desired state of HorizontalRunnerAutoscaler
type HorizontalRunnerAutoscalerSpec struct {
	// ScaleTargetRef sis the reference to scaled resource like RunnerDeployment
	ScaleTargetRef ScaleTargetRef `json:"scaleTargetRef,omitempty"`

	// MinReplicas is the minimum number of replicas the deployment is allowed to scale
	// +optional
	MinReplicas *int `json:"minReplicas,omitempty"`

	// MaxReplicas is the maximum number of replicas the deployment is allowed to scale
	// +optional
	MaxReplicas *int `json:"maxReplicas,omitempty"`

	// ScaleDownDelaySecondsAfterScaleUp is the approximate delay for a scale down followed by a scale up
	// Used to prevent flapping (down->up->down->... loop)
	// +optional
	ScaleDownDelaySecondsAfterScaleUp *int `json:"scaleDownDelaySecondsAfterScaleOut,omitempty"`

	// Metrics is the collection of various metric targets to calculate desired number of runners
	// +optional
	Metrics []MetricSpec `json:"metrics,omitempty"`

	// ScaleUpTriggers is an experimental feature to increase the desired replicas by 1
	// on each webhook requested received by the webhookBasedAutoscaler.
	//
	// This feature requires you to also enable and deploy the webhookBasedAutoscaler onto your cluster.
	//
	// Note that the added runners remain until the next sync period at least,
	// and they may or may not be used by GitHub Actions depending on the timing.
	// They are intended to be used to gain "resource slack" immediately after you
	// receive a webhook from GitHub, so that you can loosely expect MinReplicas runners to be always available.
	ScaleUpTriggers []ScaleUpTrigger `json:"scaleUpTriggers,omitempty"`

	CapacityReservations []CapacityReservation `json:"capacityReservations,omitempty" patchStrategy:"merge" patchMergeKey:"name"`

	// ScheduledOverrides is the list of ScheduledOverride.
	// It can be used to override a few fields of HorizontalRunnerAutoscalerSpec on schedule.
	// The earlier a scheduled override is, the higher it is prioritized.
	// +optional
	ScheduledOverrides []ScheduledOverride `json:"scheduledOverrides,omitempty"`

	// +optional
	GitHubAPICredentialsFrom *GitHubAPICredentialsFrom `json:"githubAPICredentialsFrom,omitempty"`
}

type ScaleUpTrigger struct {
	GitHubEvent *GitHubEventScaleUpTriggerSpec `json:"githubEvent,omitempty"`
	Amount      int                            `json:"amount,omitempty"`
	Duration    metav1.Duration                `json:"duration,omitempty"`
}

type GitHubEventScaleUpTriggerSpec struct {
	CheckRun    *CheckRunSpec    `json:"checkRun,omitempty"`
	PullRequest *PullRequestSpec `json:"pullRequest,omitempty"`
	Push        *PushSpec        `json:"push,omitempty"`
	WorkflowJob *WorkflowJobSpec `json:"workflowJob,omitempty"`
}

// https://docs.github.com/en/actions/reference/events-that-trigger-workflows#check_run
type CheckRunSpec struct {
	// One of: created, rerequested, or completed
	Types  []string `json:"types,omitempty"`
	Status string   `json:"status,omitempty"`

	// Names is a list of GitHub Actions glob patterns.
	// Any check_run event whose name matches one of patterns in the list can trigger autoscaling.
	// Note that check_run name seem to equal to the job name you've defined in your actions workflow yaml file.
	// So it is very likely that you can utilize this to trigger depending on the job.
	Names []string `json:"names,omitempty"`

	// Repositories is a list of GitHub repositories.
	// Any check_run event whose repository matches one of repositories in the list can trigger autoscaling.
	Repositories []string `json:"repositories,omitempty"`
}

// https://docs.github.com/en/developers/webhooks-and-events/webhooks/webhook-events-and-payloads#workflow_job
type WorkflowJobSpec struct {
}

// https://docs.github.com/en/actions/reference/events-that-trigger-workflows#pull_request
type PullRequestSpec struct {
	Types    []string `json:"types,omitempty"`
	Branches []string `json:"branches,omitempty"`
}

// PushSpec is the condition for triggering scale-up on push event
// Also see https://docs.github.com/en/actions/reference/events-that-trigger-workflows#push
type PushSpec struct {
}

// CapacityReservation specifies the number of replicas temporarily added
// to the scale target until ExpirationTime.
type CapacityReservation struct {
	Name           string      `json:"name,omitempty"`
	ExpirationTime metav1.Time `json:"expirationTime,omitempty"`
	Replicas       int         `json:"replicas,omitempty"`

	// +optional
	EffectiveTime metav1.Time `json:"effectiveTime,omitempty"`
}

type ScaleTargetRef struct {
	// Kind is the type of resource being referenced
	// +optional
	// +kubebuilder:validation:Enum=RunnerDeployment;RunnerSet
	Kind string `json:"kind,omitempty"`

	// Name is the name of resource being referenced
	Name string `json:"name,omitempty"`
}

type MetricSpec struct {
	// Type is the type of metric to be used for autoscaling.
	// It can be TotalNumberOfQueuedAndInProgressWorkflowRuns or PercentageRunnersBusy.
	Type string `json:"type,omitempty"`

	// RepositoryNames is the list of repository names to be used for calculating the metric.
	// For example, a repository name is the REPO part of `github.com/USER/REPO`.
	// +optional
	RepositoryNames []string `json:"repositoryNames,omitempty"`

	// ScaleUpThreshold is the percentage of busy runners greater than which will
	// trigger the hpa to scale runners up.
	// +optional
	ScaleUpThreshold string `json:"scaleUpThreshold,omitempty"`

	// ScaleDownThreshold is the percentage of busy runners less than which will
	// trigger the hpa to scale the runners down.
	// +optional
	ScaleDownThreshold string `json:"scaleDownThreshold,omitempty"`

	// ScaleUpFactor is the multiplicative factor applied to the current number of runners used
	// to determine how many pods should be added.
	// +optional
	ScaleUpFactor string `json:"scaleUpFactor,omitempty"`

	// ScaleDownFactor is the multiplicative factor applied to the current number of runners used
	// to determine how many pods should be removed.
	// +optional
	ScaleDownFactor string `json:"scaleDownFactor,omitempty"`

	// ScaleUpAdjustment is the number of runners added on scale-up.
	// You can only specify either ScaleUpFactor or ScaleUpAdjustment.
	// +optional
	ScaleUpAdjustment int `json:"scaleUpAdjustment,omitempty"`

	// ScaleDownAdjustment is the number of runners removed on scale-down.
	// You can only specify either ScaleDownFactor or ScaleDownAdjustment.
	// +optional
	ScaleDownAdjustment int `json:"scaleDownAdjustment,omitempty"`
}

// ScheduledOverride can be used to override a few fields of HorizontalRunnerAutoscalerSpec on schedule.
// A schedule can optionally be recurring, so that the corresponding override happens every day, week, month, or year.
type ScheduledOverride struct {
	// StartTime is the time at which the first override starts.
	StartTime metav1.Time `json:"startTime"`

	// EndTime is the time at which the first override ends.
	EndTime metav1.Time `json:"endTime"`

	// MinReplicas is the number of runners while overriding.
	// If omitted, it doesn't override minReplicas.
	// +optional
	// +nullable
	// +kubebuilder:validation:Minimum=0
	MinReplicas *int `json:"minReplicas,omitempty"`

	// +optional
	RecurrenceRule RecurrenceRule `json:"recurrenceRule,omitempty"`
}

type RecurrenceRule struct {
	// Frequency is the name of a predefined interval of each recurrence.
	// The valid values are "Daily", "Weekly", "Monthly", and "Yearly".
	// If empty, the corresponding override happens only once.
	// +optional
	// +kubebuilder:validation:Enum=Daily;Weekly;Monthly;Yearly
	Frequency string `json:"frequency,omitempty"`

	// UntilTime is the time of the final recurrence.
	// If empty, the schedule recurs forever.
	// +optional
	UntilTime metav1.Time `json:"untilTime,omitempty"`
}

type HorizontalRunnerAutoscalerStatus struct {
	// ObservedGeneration is the most recent generation observed for the target. It corresponds to e.g.
	// RunnerDeployment's generation, which is updated on mutation by the API Server.
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`

	// DesiredReplicas is the total number of desired, non-terminated and latest pods to be set for the primary RunnerSet
	// This doesn't include outdated pods while upgrading the deployment and replacing the runnerset.
	// +optional
	DesiredReplicas *int `json:"desiredReplicas,omitempty"`

	// +optional
	// +nullable
	LastSuccessfulScaleOutTime *metav1.Time `json:"lastSuccessfulScaleOutTime,omitempty"`

	// +optional
	CacheEntries []CacheEntry `json:"cacheEntries,omitempty"`

	// ScheduledOverridesSummary is the summary of active and upcoming scheduled overrides to be shown in e.g. a column of a `kubectl get hra` output
	// for observability.
	// +optional
	ScheduledOverridesSummary *string `json:"scheduledOverridesSummary,omitempty"`
}

const CacheEntryKeyDesiredReplicas = "desiredReplicas"

type CacheEntry struct {
	Key            string      `json:"key,omitempty"`
	Value          int         `json:"value,omitempty"`
	ExpirationTime metav1.Time `json:"expirationTime,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:resource:shortName=hra
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:JSONPath=".spec.minReplicas",name=Min,type=number
// +kubebuilder:printcolumn:JSONPath=".spec.maxReplicas",name=Max,type=number
// +kubebuilder:printcolumn:JSONPath=".status.desiredReplicas",name=Desired,type=number
// +kubebuilder:printcolumn:JSONPath=".status.scheduledOverridesSummary",name=Schedule,type=string

// HorizontalRunnerAutoscaler is the Schema for the horizontalrunnerautoscaler API
type HorizontalRunnerAutoscaler struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   HorizontalRunnerAutoscalerSpec   `json:"spec,omitempty"`
	Status HorizontalRunnerAutoscalerStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// HorizontalRunnerAutoscalerList contains a list of HorizontalRunnerAutoscaler
type HorizontalRunnerAutoscalerList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []HorizontalRunnerAutoscaler `json:"items"`
}

func init() {
	SchemeBuilder.Register(&HorizontalRunnerAutoscaler{}, &HorizontalRunnerAutoscalerList{})
}
