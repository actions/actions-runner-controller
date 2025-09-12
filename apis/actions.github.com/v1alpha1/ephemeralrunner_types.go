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

// EphemeralRunnerContainerName is the name of the runner container.
// It represents the name of the container running the self-hosted runner image.
const EphemeralRunnerContainerName = "runner"

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:JSONPath=".spec.githubConfigUrl",name="GitHub Config URL",type=string
// +kubebuilder:printcolumn:JSONPath=".status.runnerId",name=RunnerId,type=number
// +kubebuilder:printcolumn:JSONPath=".status.phase",name=Status,type=string
// +kubebuilder:printcolumn:JSONPath=".status.jobRepositoryName",name=JobRepository,type=string
// +kubebuilder:printcolumn:JSONPath=".status.jobWorkflowRef",name=JobWorkflowRef,type=string
// +kubebuilder:printcolumn:JSONPath=".status.workflowRunId",name=WorkflowRunId,type=number
// +kubebuilder:printcolumn:JSONPath=".status.jobDisplayName",name=JobDisplayName,type=string
// +kubebuilder:printcolumn:JSONPath=".status.jobId",name=JobId,type=string
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

func (er *EphemeralRunner) HasJob() bool {
	return len(er.Status.JobID) > 0
}

func (er *EphemeralRunner) HasContainerHookConfigured() bool {
	for i := range er.Spec.Spec.Containers {
		if er.Spec.Spec.Containers[i].Name != EphemeralRunnerContainerName {
			continue
		}

		for _, env := range er.Spec.Spec.Containers[i].Env {
			if env.Name == "ACTIONS_RUNNER_CONTAINER_HOOKS" {
				return true
			}
		}

		return false
	}
	return false
}

func (er *EphemeralRunner) GitHubConfigSecret() string {
	return er.Spec.GitHubConfigSecret
}

func (er *EphemeralRunner) GitHubConfigUrl() string {
	return er.Spec.GitHubConfigUrl
}

func (er *EphemeralRunner) GitHubProxy() *ProxyConfig {
	return er.Spec.Proxy
}

func (er *EphemeralRunner) GitHubServerTLS() *TLSConfig {
	return er.Spec.GitHubServerTLS
}

func (er *EphemeralRunner) VaultConfig() *VaultConfig {
	return er.Spec.VaultConfig
}

func (er *EphemeralRunner) VaultProxy() *ProxyConfig {
	if er.Spec.VaultConfig != nil {
		return er.Spec.VaultConfig.Proxy
	}
	return nil
}

// EphemeralRunnerSpec defines the desired state of EphemeralRunner
type EphemeralRunnerSpec struct {
	// +required
	GitHubConfigUrl string `json:"githubConfigUrl,omitempty"`

	// +required
	GitHubConfigSecret string `json:"githubConfigSecret,omitempty"`

	// +optional
	GitHubServerTLS *TLSConfig `json:"githubServerTLS,omitempty"`

	// +required
	RunnerScaleSetId int `json:"runnerScaleSetId,omitempty"`

	// +optional
	Proxy *ProxyConfig `json:"proxy,omitempty"`

	// +optional
	ProxySecretRef string `json:"proxySecretRef,omitempty"`

	// +optional
	VaultConfig *VaultConfig `json:"vaultConfig,omitempty"`

	corev1.PodTemplateSpec `json:",inline"`
}

// EphemeralRunnerStatus defines the observed state of EphemeralRunner
type EphemeralRunnerStatus struct {
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
	Failures map[string]metav1.Time `json:"failures,omitempty"`

	// +optional
	JobRequestId int64 `json:"jobRequestId,omitempty"`

	// +optional
	JobID string `json:"jobId,omitempty"`

	// +optional
	JobRepositoryName string `json:"jobRepositoryName,omitempty"`

	// +optional
	JobWorkflowRef string `json:"jobWorkflowRef,omitempty"`

	// +optional
	WorkflowRunId int64 `json:"workflowRunId,omitempty"`

	// +optional
	JobDisplayName string `json:"jobDisplayName,omitempty"`
}

func (s *EphemeralRunnerStatus) LastFailure() metav1.Time {
	var maxTime metav1.Time
	if len(s.Failures) == 0 {
		return maxTime
	}

	for _, ts := range s.Failures {
		if ts.After(maxTime.Time) {
			maxTime = ts
		}
	}
	return maxTime
}

// +kubebuilder:object:root=true

// EphemeralRunnerList contains a list of EphemeralRunner
type EphemeralRunnerList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []EphemeralRunner `json:"items"`
}

func init() {
	SchemeBuilder.Register(&EphemeralRunner{}, &EphemeralRunnerList{})
}
