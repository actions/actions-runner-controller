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

// RunnerReplicaSetSpec defines the desired state of RunnerReplicaSet
type RunnerReplicaSetSpec struct {
	// +optional
	// +nullable
	Replicas *int `json:"replicas,omitempty"`

	// EffectiveTime is the time the upstream controller requested to sync Replicas.
	// It is usually populated by the webhook-based autoscaler via HRA and RunnerDeployment.
	// The value is used to prevent runnerreplicaset controller from unnecessarily recreating ephemeral runners
	// based on potentially outdated Replicas value.
	//
	// +optional
	// +nullable
	EffectiveTime *metav1.Time `json:"effectiveTime"`

	// +optional
	// +nullable
	Selector *metav1.LabelSelector `json:"selector"`
	Template RunnerTemplate        `json:"template"`
}

type RunnerReplicaSetStatus struct {
	// See K8s replicaset controller code for reference
	// https://github.com/kubernetes/kubernetes/blob/ea0764452222146c47ec826977f49d7001b0ea8c/pkg/controller/replicaset/replica_set_utils.go#L101-L106

	// Replicas is the number of runners that are created and still being managed by this runner replica set.
	// +optional
	Replicas *int `json:"replicas"`

	// ReadyReplicas is the number of runners that are created and Running.
	ReadyReplicas *int `json:"readyReplicas"`

	// AvailableReplicas is the number of runners that are created and Running.
	// This is currently same as ReadyReplicas but preserved for future use.
	AvailableReplicas *int `json:"availableReplicas"`
}

type RunnerTemplate struct {
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec RunnerSpec `json:"spec,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:resource:shortName=rrs
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:JSONPath=".spec.replicas",name=Desired,type=number
// +kubebuilder:printcolumn:JSONPath=".status.replicas",name=Current,type=number
// +kubebuilder:printcolumn:JSONPath=".status.readyReplicas",name=Ready,type=number
// +kubebuilder:printcolumn:name="Age",type="date",JSONPath=".metadata.creationTimestamp"

// RunnerReplicaSet is the Schema for the runnerreplicasets API
type RunnerReplicaSet struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   RunnerReplicaSetSpec   `json:"spec,omitempty"`
	Status RunnerReplicaSetStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// RunnerList contains a list of Runner
type RunnerReplicaSetList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []RunnerReplicaSet `json:"items"`
}

func init() {
	SchemeBuilder.Register(&RunnerReplicaSet{}, &RunnerReplicaSetList{})
}
