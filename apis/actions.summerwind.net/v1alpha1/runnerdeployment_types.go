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

// RunnerDeploymentSpec defines the desired state of RunnerDeployment
type RunnerDeploymentSpec struct {
	// +optional
	// +nullable
	Replicas *int `json:"replicas,omitempty"`

	// EffectiveTime is the time the upstream controller requested to sync Replicas.
	// It is usually populated by the webhook-based autoscaler via HRA.
	// The value is inherited to RunnerReplicaSet(s) and used to prevent ephemeral runners from unnecessarily recreated.
	//
	// +optional
	// +nullable
	EffectiveTime *metav1.Time `json:"effectiveTime"`

	// +optional
	// +nullable
	Selector *metav1.LabelSelector `json:"selector"`
	Template RunnerTemplate        `json:"template"`
}

type RunnerDeploymentStatus struct {
	// See K8s deployment controller code for reference
	// https://github.com/kubernetes/kubernetes/blob/ea0764452222146c47ec826977f49d7001b0ea8c/pkg/controller/deployment/sync.go#L487-L505

	// AvailableReplicas is the total number of available runners which have been successfully registered to GitHub and still running.
	// This corresponds to the sum of status.availableReplicas of all the runner replica sets.
	// +optional
	AvailableReplicas *int `json:"availableReplicas"`

	// ReadyReplicas is the total number of available runners which have been successfully registered to GitHub and still running.
	// This corresponds to the sum of status.readyReplicas of all the runner replica sets.
	// +optional
	ReadyReplicas *int `json:"readyReplicas"`

	// ReadyReplicas is the total number of available runners which have been successfully registered to GitHub and still running.
	// This corresponds to status.replicas of the runner replica set that has the desired template hash.
	// +optional
	UpdatedReplicas *int `json:"updatedReplicas"`

	// DesiredReplicas is the total number of desired, non-terminated and latest pods to be set for the primary RunnerSet
	// This doesn't include outdated pods while upgrading the deployment and replacing the runnerset.
	// +optional
	DesiredReplicas *int `json:"desiredReplicas"`

	// Replicas is the total number of replicas
	// +optional
	Replicas *int `json:"replicas"`

	// Selector is the string form of the pod selector
	// +optional
	Selector string `json:"selector"`
}

// +kubebuilder:object:root=true
// +kubebuilder:resource:shortName=rdeploy
// +kubebuilder:subresource:status
// +kubebuilder:subresource:scale:specpath=.spec.replicas,statuspath=.status.replicas,selectorpath=.status.selector
// +kubebuilder:printcolumn:JSONPath=".spec.template.spec.enterprise",name=Enterprise,type=string
// +kubebuilder:printcolumn:JSONPath=".spec.template.spec.organization",name=Organization,type=string
// +kubebuilder:printcolumn:JSONPath=".spec.template.spec.repository",name=Repository,type=string
// +kubebuilder:printcolumn:JSONPath=".spec.template.spec.group",name=Group,type=string
// +kubebuilder:printcolumn:JSONPath=".spec.template.spec.labels",name=Labels,type=string
// +kubebuilder:printcolumn:JSONPath=".spec.replicas",name=Desired,type=number
// +kubebuilder:printcolumn:JSONPath=".status.replicas",name=Current,type=number
// +kubebuilder:printcolumn:JSONPath=".status.updatedReplicas",name=Up-To-Date,type=number
// +kubebuilder:printcolumn:JSONPath=".status.availableReplicas",name=Available,type=number
// +kubebuilder:printcolumn:name="Age",type="date",JSONPath=".metadata.creationTimestamp"

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
