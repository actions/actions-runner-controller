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
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// RunnerSpec defines the desired state of Runner
type RunnerSpec struct {
	// +kubebuilder:validation:MinLength=3
	// +kubebuilder:validation:Pattern=`^[^/]+/[^/]+$`
	Repository string `json:"repository"`

	// +optional
	Image string `json:"image"`

	// +optional
	Env []corev1.EnvVar `json:"env,omitempty"`
}

// RunnerStatus defines the observed state of Runner
type RunnerStatus struct {
	Registration RunnerStatusRegistration `json:"registration"`
	Phase        string                   `json:"phase"`
	Reason       string                   `json:"reason"`
	Message      string                   `json:"message"`
}

type RunnerStatusRegistration struct {
	Repository string      `json:"repository"`
	Token      string      `json:"token"`
	ExpiresAt  metav1.Time `json:"expiresAt"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:JSONPath=".spec.repository",name=Repository,type=string
// +kubebuilder:printcolumn:JSONPath=".status.phase",name=Status,type=string

// Runner is the Schema for the runners API
type Runner struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   RunnerSpec   `json:"spec,omitempty"`
	Status RunnerStatus `json:"status,omitempty"`
}

func (r Runner) IsRegisterable() bool {
	if r.Status.Registration.Repository != r.Spec.Repository {
		return false
	}

	if r.Status.Registration.Token == "" {
		return false
	}

	now := metav1.Now()
	if r.Status.Registration.ExpiresAt.Before(&now) {
		return false
	}

	return true
}

// +kubebuilder:object:root=true

// RunnerList contains a list of Runner
type RunnerList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Runner `json:"items"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:JSONPath=".spec.replicas",name=Desired,type=number
// +kubebuilder:printcolumn:JSONPath=".status.availableReplicas",name=Current,type=number
// +kubebuilder:printcolumn:JSONPath=".status.readyReplicas",name=Ready,type=number

// RunnerReplicaSet is the Schema for the runnerreplicasets API
type RunnerReplicaSet struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   RunnerReplicaSetSpec   `json:"spec,omitempty"`
	Status RunnerReplicaSetStatus `json:"status,omitempty"`
}

// RunnerReplicaSetSpec defines the desired state of RunnerReplicaSet
type RunnerReplicaSetSpec struct {
	Replicas *int `json:"replicas"`

	Template RunnerTemplate `json:"template"`
}

type RunnerReplicaSetStatus struct {
	AvailableReplicas int `json:"availableReplicas"`
	ReadyReplicas     int `json:"readyReplicas"`
}

type RunnerTemplate struct {
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec RunnerSpec `json:"spec,omitempty"`
}

// +kubebuilder:object:root=true

// RunnerList contains a list of Runner
type RunnerReplicaSetList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []RunnerReplicaSet `json:"items"`
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

// RunnerReplicaSetSpec defines the desired state of RunnerDeployment
type RunnerDeploymentSpec struct {
	Replicas *int `json:"replicas"`

	Template RunnerTemplate `json:"template"`
}

type RunnerDeploymentStatus struct {
	AvailableReplicas int `json:"availableReplicas"`
	ReadyReplicas     int `json:"readyReplicas"`
}

// +kubebuilder:object:root=true

// RunnerList contains a list of Runner
type RunnerDeploymentList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []RunnerDeployment `json:"items"`
}

func init() {
	SchemeBuilder.Register(&Runner{}, &RunnerList{}, &RunnerReplicaSet{}, &RunnerReplicaSetList{}, &RunnerDeployment{}, &RunnerDeploymentList{})
}
