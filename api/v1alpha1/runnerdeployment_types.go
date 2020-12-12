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

const (
	AutoscalingMetricTypeTotalNumberOfQueuedAndInProgressWorkflowRuns = "TotalNumberOfQueuedAndInProgressWorkflowRuns"
	AutoscalingMetricTypePercentageRunnersBusy                        = "PercentageRunnersBusy"
)

// RunnerReplicaSetSpec defines the desired state of RunnerDeployment
type RunnerDeploymentSpec struct {
	// +optional
	// +nullable
	Replicas *int `json:"replicas,omitempty"`

	Template RunnerTemplate `json:"template"`
}

type RunnerDeploymentStatus struct {
	AvailableReplicas int `json:"availableReplicas"`
	ReadyReplicas     int `json:"readyReplicas"`

	// Replicas is the total number of desired, non-terminated and latest pods to be set for the primary RunnerSet
	// This doesn't include outdated pods while upgrading the deployment and replacing the runnerset.
	// +optional
	Replicas *int `json:"desiredReplicas,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:JSONPath=".spec.replicas",name=Desired,type=number
// +kubebuilder:printcolumn:JSONPath=".status.availableReplicas",name=Current,type=number
// +kubebuilder:printcolumn:JSONPath=".status.readyReplicas",name=Ready,type=number

// RunnerDeployment is the Schema for the runnerdeployments API
type RunnerDeployment struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   RunnerDeploymentSpec   `json:"spec,omitempty"`
	Status RunnerDeploymentStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// RunnerList contains a list of Runner
type RunnerDeploymentList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []RunnerDeployment `json:"items"`
}

func init() {
	SchemeBuilder.Register(&RunnerDeployment{}, &RunnerDeploymentList{})
}
