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

//+kubebuilder:object:root=true
//+kubebuilder:subresource:status
// +kubebuilder:printcolumn:JSONPath=".spec.githubConfigUrl",name="GitHub Config URL",type=string
// +kubebuilder:printcolumn:JSONPath=".status.runnerId",name=RunnerId,type=number
// +kubebuilder:printcolumn:JSONPath=".status.phase",name=Status,type=string
// +kubebuilder:printcolumn:JSONPath=".status.jobRepositoryName",name=JobRepository,type=string
// +kubebuilder:printcolumn:JSONPath=".status.jobWorkflowRef",name=JobWorkflowRef,type=string
// +kubebuilder:printcolumn:JSONPath=".status.workflowRunId",name=WorkflowRunId,type=number
// +kubebuilder:printcolumn:JSONPath=".status.jobDisplayName",name=JobDisplayName,type=string
// +kubebuilder:printcolumn:JSONPath=".status.message",name=Message,type=string
// +kubebuilder:printcolumn:name="Age",type="date",JSONPath=".metadata.creationTimestamp"

// EphemeralRunner is the Schema for the ephemeralrunners API
type EphemeralRunner struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   EphemeralRunnerSpec   `json:"spec,omitempty"`
	Status EphemeralRunnerStatus `json:"status,omitempty"`
}

func (er *EphemeralRunner) IsDone() bool {
	return er.Status.Phase == corev1.PodSucceeded || er.Status.Phase == corev1.PodFailed
}

// EphemeralRunnerSpec defines the desired state of EphemeralRunner
type EphemeralRunnerSpec struct {
	// INSERT ADDITIONAL SPEC FIELDS - desired state of cluster
	// Important: Run "make" to regenerate code after modifying this file

	// +required
	GitHubConfigUrl string `json:"githubConfigUrl,omitempty"`

	// +required
	GitHubConfigSecret string `json:"githubConfigSecret,omitempty"`

	// +required
	RunnerScaleSetId int `json:"runnerScaleSetId,omitempty"`

	// +optional
	Proxy *ProxyConfig `json:"proxy,omitempty"`

	// +optional
	ProxySecretRef string `json:"proxySecretRef,omitempty"`

	// +optional
	GitHubServerTLS *GitHubServerTLSConfig `json:"githubServerTLS,omitempty"`

	// +required
	corev1.PodTemplateSpec `json:",inline"`
}

// EphemeralRunnerStatus defines the observed state of EphemeralRunner
type EphemeralRunnerStatus struct {
	// INSERT ADDITIONAL STATUS FIELD - define observed state of cluster
	// Important: Run "make" to regenerate code after modifying this file

	// Turns true only if the runner is online.
	// +optional
	Ready bool `json:"ready"`
	// Phase describes phases where EphemeralRunner can be in.
	// The underlying type is a PodPhase, but the meaning is more restrictive
	//
	// The PodFailed phase should be set only when EphemeralRunner fails to start
	// after multiple retries. That signals that this EphemeralRunner won't work,
	// and manual inspection is required
	//
	// The PodSucceded phase should be set only when confirmed that EphemeralRunner
	// actually executed the job and has been removed from the service.
	// +optional
	Phase corev1.PodPhase `json:"phase,omitempty"`
	// +optional
	Reason string `json:"reason,omitempty"`
	// +optional
	Message string `json:"message,omitempty"`

	// +optional
	RunnerId int `json:"runnerId,omitempty"`
	// +optional
	RunnerName string `json:"runnerName,omitempty"`
	// +optional
	RunnerJITConfig string `json:"runnerJITConfig,omitempty"`

	// +optional
	Failures map[string]bool `json:"failures,omitempty"`

	// +optional
	JobRequestId int64 `json:"jobRequestId,omitempty"`

	// +optional
	JobRepositoryName string `json:"jobRepositoryName,omitempty"`

	// +optional
	JobWorkflowRef string `json:"jobWorkflowRef,omitempty"`

	// +optional
	WorkflowRunId int64 `json:"workflowRunId,omitempty"`

	// +optional
	JobDisplayName string `json:"jobDisplayName,omitempty"`
}

//+kubebuilder:object:root=true

// EphemeralRunnerList contains a list of EphemeralRunner
type EphemeralRunnerList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []EphemeralRunner `json:"items"`
}

func init() {
	SchemeBuilder.Register(&EphemeralRunner{}, &EphemeralRunnerList{})
}
