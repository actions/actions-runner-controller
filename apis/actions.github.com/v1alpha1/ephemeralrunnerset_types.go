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
	// +optional
	Replicas int `json:"replicas,omitempty"`
	// PatchID is the unique identifier for the patch issued by the listener app
	// +optional
	PatchID int `json:"patchID"`
	// EphemeralRunnerSpec is the spec of the ephemeral runner
	// +optional
	EphemeralRunnerSpec EphemeralRunnerSpec `json:"ephemeralRunnerSpec,omitempty"`
	// EphemeralRunnerMetadata is the metadata to be applied to all ephemeral runners created by this set.
	// If the EphemeralRunnerMetadata is updated, the update applies to new ephemeral runners created after the update,
	// but does not apply to existing ephemeral runners.
	// +optional
	EphemeralRunnerMetadata *ResourceMeta `json:"ephemeralRunnerMetadata,omitempty"`
}

// EphemeralRunnerSetStatus defines the observed state of EphemeralRunnerSet
type EphemeralRunnerSetStatus struct {
	// CurrentReplicas is the number of currently running EphemeralRunner resources being managed by this EphemeralRunnerSet.
	// +kubebuilder:validation:Minimum=0
	// +optional
	CurrentReplicas int `json:"currentReplicas"`
	// +optional
	// +kubebuilder:validation:Minimum=0
	PendingEphemeralRunners int `json:"pendingEphemeralRunners"`
	// +optional
	// +kubebuilder:validation:Minimum=0
	RunningEphemeralRunners int `json:"runningEphemeralRunners"`
	// +optional
	// +kubebuilder:validation:Minimum=0
	FailedEphemeralRunners int `json:"failedEphemeralRunners"`
	// +optional
	Phase EphemeralRunnerSetPhase `json:"phase"`
}

// EphemeralRunnerSetPhase is the phase of the ephemeral runner set resource
type EphemeralRunnerSetPhase string

const (
	EphemeralRunnerSetPhaseRunning EphemeralRunnerSetPhase = "Running"
	// EphemeralRunnerSetPhaseOutdated is set when at least one ephemeral runner
	// contains the outdated phase
	EphemeralRunnerSetPhaseOutdated EphemeralRunnerSetPhase = "Outdated"
)

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:JSONPath=".spec.replicas",name="DesiredReplicas",type="integer"
// +kubebuilder:printcolumn:JSONPath=".status.currentReplicas", name="CurrentReplicas",type="integer"
// +kubebuilder:printcolumn:JSONPath=".status.pendingEphemeralRunners",name=Pending Runners,type=integer
// +kubebuilder:printcolumn:JSONPath=".status.runningEphemeralRunners",name=Running Runners,type=integer
// +kubebuilder:printcolumn:JSONPath=".status.finishedEphemeralRunners",name=Finished Runners,type=integer
// +kubebuilder:printcolumn:JSONPath=".status.deletingEphemeralRunners",name=Deleting Runners,type=integer
// +kubebuilder:printcolumn:JSONPath=".status.phase",name=Phase,type=string

// EphemeralRunnerSet is the Schema for the ephemeralrunnersets API
type EphemeralRunnerSet struct {
	metav1.TypeMeta `json:",inline"`
	// +optional
	metav1.ObjectMeta `json:"metadata,omitempty"`

	// +optional
	Spec EphemeralRunnerSetSpec `json:"spec,omitempty"`
	// +optional
	Status EphemeralRunnerSetStatus `json:"status,omitempty"`
}

// EphemeralRunnerSpecHash computes the hash value of the EphemeralRunnerSpec and returns it as a string.
func (ers *EphemeralRunnerSet) EphemeralRunnerSpecHash() string {
	return ers.Spec.EphemeralRunnerSpec.Hash()
}

func (ers *EphemeralRunnerSet) GitHubConfigSecret() string {
	return ers.Spec.EphemeralRunnerSpec.GitHubConfigSecret
}

func (ers *EphemeralRunnerSet) GitHubConfigUrl() string {
	return ers.Spec.EphemeralRunnerSpec.GitHubConfigURL
}

func (ers *EphemeralRunnerSet) GitHubProxy() *ProxyConfig {
	return ers.Spec.EphemeralRunnerSpec.Proxy
}

func (ers *EphemeralRunnerSet) GitHubServerTLS() *TLSConfig {
	return ers.Spec.EphemeralRunnerSpec.GitHubServerTLS
}

func (ers *EphemeralRunnerSet) VaultConfig() *VaultConfig {
	return ers.Spec.EphemeralRunnerSpec.VaultConfig
}

func (ers *EphemeralRunnerSet) VaultProxy() *ProxyConfig {
	if ers.Spec.EphemeralRunnerSpec.VaultConfig != nil {
		return ers.Spec.EphemeralRunnerSpec.VaultConfig.Proxy
	}
	return nil
}

// EphemeralRunnerSetList contains a list of EphemeralRunnerSet
// +kubebuilder:object:root=true
type EphemeralRunnerSetList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []EphemeralRunnerSet `json:"items"`
}

func init() {
	SchemeBuilder.Register(&EphemeralRunnerSet{}, &EphemeralRunnerSetList{})
}
