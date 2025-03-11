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

// AutoscalingListenerSpec defines the desired state of AutoscalingListener
type AutoscalingListenerSpec struct {
	// Required
	GitHubConfigUrl string `json:"githubConfigUrl,omitempty"`

	// Required
	GitHubConfigSecret string `json:"githubConfigSecret,omitempty"`

	// Required
	RunnerScaleSetId int `json:"runnerScaleSetId,omitempty"`

	// Required
	AutoscalingRunnerSetNamespace string `json:"autoscalingRunnerSetNamespace,omitempty"`

	// Required
	AutoscalingRunnerSetName string `json:"autoscalingRunnerSetName,omitempty"`

	// Required
	EphemeralRunnerSetName string `json:"ephemeralRunnerSetName,omitempty"`

	// Required
	// +kubebuilder:validation:Minimum:=0
	MaxRunners int `json:"maxRunners,omitempty"`

	// Required
	// +kubebuilder:validation:Minimum:=0
	MinRunners int `json:"minRunners,omitempty"`

	// Required
	Image string `json:"image,omitempty"`

	// Required
	ImagePullSecrets []corev1.LocalObjectReference `json:"imagePullSecrets,omitempty"`

	// +optional
	Proxy *ProxyConfig `json:"proxy,omitempty"`

	// +optional
	GitHubServerTLS *GitHubServerTLSConfig `json:"githubServerTLS,omitempty"`

	// +optional
	Template *corev1.PodTemplateSpec `json:"template,omitempty"`
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
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   AutoscalingListenerSpec   `json:"spec,omitempty"`
	Status AutoscalingListenerStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// AutoscalingListenerList contains a list of AutoscalingListener
type AutoscalingListenerList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []AutoscalingListener `json:"items"`
}

func init() {
	SchemeBuilder.Register(&AutoscalingListener{}, &AutoscalingListenerList{})
}
