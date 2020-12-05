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

	// MinReplicas is the maximum number of replicas the deployment is allowed to scale
	// +optional
	MaxReplicas *int `json:"maxReplicas,omitempty"`

	// ScaleDownDelaySecondsAfterScaleUp is the approximate delay for a scale down followed by a scale up
	// Used to prevent flapping (down->up->down->... loop)
	// +optional
	ScaleDownDelaySecondsAfterScaleUp *int `json:"scaleDownDelaySecondsAfterScaleOut,omitempty"`

	// Metrics is the collection of various metric targets to calculate desired number of runners
	// +optional
	Metrics []MetricSpec `json:"metrics,omitempty"`
}

type ScaleTargetRef struct {
	Name string `json:"name,omitempty"`
}

type MetricSpec struct {
	// Type is the type of metric to be used for autoscaling.
	// The only supported Type is TotalNumberOfQueuedAndInProgressWorkflowRuns
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
	LastSuccessfulScaleOutTime *metav1.Time `json:"lastSuccessfulScaleOutTime,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:JSONPath=".spec.minReplicas",name=Min,type=number
// +kubebuilder:printcolumn:JSONPath=".spec.maxReplicas",name=Max,type=number
// +kubebuilder:printcolumn:JSONPath=".status.desiredReplicas",name=Desired,type=number

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
