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
	"github.com/actions/actions-runner-controller/hash"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// AutoscalingListenerSpec defines the desired state of AutoscalingListener
type AutoscalingListenerSpec struct {
	// +optional
	GitHubConfigURL string `json:"githubConfigUrl,omitempty"`

	// +optional
	GitHubConfigSecret string `json:"githubConfigSecret,omitempty"`

	// +optional
	// +kubebuilder:validation:Minimum:=1
	RunnerScaleSetID int `json:"runnerScaleSetId,omitempty"`

	// +optional
	AutoscalingRunnerSetNamespace string `json:"autoscalingRunnerSetNamespace,omitempty"`

	// +optional
	AutoscalingRunnerSetName string `json:"autoscalingRunnerSetName,omitempty"`

	// +optional
	EphemeralRunnerSetName string `json:"ephemeralRunnerSetName,omitempty"`

	// +kubebuilder:validation:Minimum:=0
	// +optional
	MaxRunners int `json:"maxRunners"`

	// +kubebuilder:validation:Minimum:=0
	// +optional
	MinRunners int `json:"minRunners"`

	// +optional
	Image string `json:"image,omitempty"`

	// +optional
	ImagePullSecrets []corev1.LocalObjectReference `json:"imagePullSecrets,omitempty"`

	// +optional
	Proxy *ProxyConfig `json:"proxy,omitempty"`

	// +optional
	GitHubServerTLS *TLSConfig `json:"githubServerTLS,omitempty"`

	// +optional
	VaultConfig *VaultConfig `json:"vaultConfig,omitempty"`

	// +optional
	Metrics *MetricsConfig `json:"metrics,omitempty"`

	// +optional
	Template *corev1.PodTemplateSpec `json:"template,omitempty"`

	// +optional
	ConfigSecretMetadata *ResourceMeta `json:"configSecretMetadata,omitempty"`

	// +optional
	ServiceAccountMetadata *ResourceMeta `json:"serviceAccountMetadata,omitempty"`

	// +optional
	RoleMetadata *ResourceMeta `json:"roleMetadata,omitempty"`

	// +optional
	RoleBindingMetadata *ResourceMeta `json:"roleBindingMetadata,omitempty"`
}

func (s *AutoscalingListenerSpec) Hash() string {
	return hash.ComputeTemplateHash(s)
}

// AutoscalingListenerStatus defines the observed state of AutoscalingListener
type AutoscalingListenerStatus struct{}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:JSONPath=".spec.githubConfigUrl",name=GitHub Configure URL,type=string
// +kubebuilder:printcolumn:JSONPath=".spec.autoscalingRunnerSetNamespace",name=AutoscalingRunnerSet Namespace,type=string
// +kubebuilder:printcolumn:JSONPath=".spec.autoscalingRunnerSetName",name=AutoscalingRunnerSet Name,type=string

// AutoscalingListener is the Schema for the autoscalinglisteners API
type AutoscalingListener struct {
	metav1.TypeMeta `json:",inline"`
	// +optional
	metav1.ObjectMeta `json:"metadata,omitempty"`

	// +optional
	Spec AutoscalingListenerSpec `json:"spec,omitempty"`
	// +optional
	Status AutoscalingListenerStatus `json:"status,omitempty"`
}

// AutoscalingListenerList is a list of AutoscalingListener resources
// +kubebuilder:object:root=true
// AutoscalingListenerList contains a list of AutoscalingListener
type AutoscalingListenerList struct {
	metav1.TypeMeta `json:",inline"`
	// +optional
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []AutoscalingListener `json:"items"`
}

func init() {
	SchemeBuilder.Register(&AutoscalingListener{}, &AutoscalingListenerList{})
}
