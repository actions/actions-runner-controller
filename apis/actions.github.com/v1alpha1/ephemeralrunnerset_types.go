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

// EphemeralRunnerSetSpec defines the desired state of EphemeralRunnerSet
type EphemeralRunnerSetSpec struct {
	// Replicas is the number of desired EphemeralRunner resources in the k8s namespace.
	Replicas int `json:"replicas,omitempty"`
	// PatchID is the unique identifier for the patch issued by the listener app
	PatchID int `json:"patchID"`

	EphemeralRunnerSpec EphemeralRunnerSpec `json:"ephemeralRunnerSpec,omitempty"`
}

// EphemeralRunnerSetStatus defines the observed state of EphemeralRunnerSet
type EphemeralRunnerSetStatus struct {
	// CurrentReplicas is the number of currently running EphemeralRunner resources being managed by this EphemeralRunnerSet.
	CurrentReplicas int `json:"currentReplicas"`

	// EphemeralRunner counts separated by the stage ephemeral runners are in

	// +optional
	PendingEphemeralRunners int `json:"pendingEphemeralRunners"`
	// +optional
	RunningEphemeralRunners int `json:"runningEphemeralRunners"`
	// +optional
	FailedEphemeralRunners int `json:"failedEphemeralRunners"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:JSONPath=".spec.replicas",name="DesiredReplicas",type="integer"
// +kubebuilder:printcolumn:JSONPath=".status.currentReplicas", name="CurrentReplicas",type="integer"
//+kubebuilder:printcolumn:JSONPath=".status.pendingEphemeralRunners",name=Pending Runners,type=integer
//+kubebuilder:printcolumn:JSONPath=".status.runningEphemeralRunners",name=Running Runners,type=integer
//+kubebuilder:printcolumn:JSONPath=".status.finishedEphemeralRunners",name=Finished Runners,type=integer
//+kubebuilder:printcolumn:JSONPath=".status.deletingEphemeralRunners",name=Deleting Runners,type=integer

// EphemeralRunnerSet is the Schema for the ephemeralrunnersets API
type EphemeralRunnerSet struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   EphemeralRunnerSetSpec   `json:"spec,omitempty"`
	Status EphemeralRunnerSetStatus `json:"status,omitempty"`
}

//+kubebuilder:object:root=true

// EphemeralRunnerSetList contains a list of EphemeralRunnerSet
type EphemeralRunnerSetList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []EphemeralRunnerSet `json:"items"`
}

func init() {
	SchemeBuilder.Register(&EphemeralRunnerSet{}, &EphemeralRunnerSetList{})
}
