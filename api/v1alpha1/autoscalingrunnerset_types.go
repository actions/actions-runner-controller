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

// NOTE: json tags are required.  Any new fields you add must have json tags for the fields to be serialized.

// AutoscalingRunnerSetSpec defines the desired state of AutoscalingRunnerSet
type AutoscalingRunnerSetSpec struct {
	//+kubebuilder:validation:MinLength=0
	// A container image providing our autoscaler app.
	//
	// Temporary so we can test different versions by simply changing the YAML.
	AutoscalerImage string `json:"autoscalerImage,omitempty"`

	//+kubebuilder:validation:MinLength=0
	// Sets the envvar of GITHUB_RUNNER_ORG in the Autoscaler container.
	RunnerOrg string `json:"githubRunnerOrg,omitempty"`

	//+kubebuilder:validation:MinLength=0
	// Sets the envvar of GITHUB_RUNNER_REPOSITORY in the Autoscaler container.
	RunnerRepo string `json:"githubRunnerRepository,omitempty"`

	//+kubebuilder:validation:MinLength=0
	// Sets the envvar of GITHUB_RUNNER_SCALE_SET_NAME in the Autoscaler container.
	RunnerScaleSet string `json:"githubRunnerScaleSet,omitempty"`
}

// AutoscalingRunnerSetStatus defines the observed state of AutoscalingRunnerSet
type AutoscalingRunnerSetStatus struct {
	ActiveAutoscaler corev1.ObjectReference `json:"active,omitempty"`
}

//+kubebuilder:object:root=true
//+kubebuilder:subresource:status

// AutoscalingRunnerSet is the Schema for the autoscalingrunnersets API
type AutoscalingRunnerSet struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   AutoscalingRunnerSetSpec   `json:"spec,omitempty"`
	Status AutoscalingRunnerSetStatus `json:"status,omitempty"`
}

//+kubebuilder:object:root=true

// AutoscalingRunnerSetList contains a list of AutoscalingRunnerSet
type AutoscalingRunnerSetList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []AutoscalingRunnerSet `json:"items"`
}

func init() {
	SchemeBuilder.Register(&AutoscalingRunnerSet{}, &AutoscalingRunnerSetList{})
}
