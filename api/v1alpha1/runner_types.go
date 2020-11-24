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
	"errors"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// RunnerSpec defines the desired state of Runner
type RunnerSpec struct {
	// +optional
	// +kubebuilder:validation:Pattern=`^[^/]+$`
	Organization string `json:"organization,omitempty"`

	// +optional
	// +kubebuilder:validation:Pattern=`^[^/]+/[^/]+$`
	Repository string `json:"repository,omitempty"`

	// +optional
	Labels []string `json:"labels,omitempty"`

	// +optional
	Group string `json:"group,omitempty"`

	// +optional
	Containers []corev1.Container `json:"containers,omitempty"`
	// +optional
	DockerdContainerResources corev1.ResourceRequirements `json:"dockerdContainerResources,omitempty"`
	// +optional
	Resources corev1.ResourceRequirements `json:"resources,omitempty"`
	// +optional
	VolumeMounts []corev1.VolumeMount `json:"volumeMounts,omitempty"`
	// +optional
	EnvFrom []corev1.EnvFromSource `json:"envFrom,omitempty"`

	// +optional
	Image string `json:"image"`
	// +optional
	ImagePullPolicy corev1.PullPolicy `json:"imagePullPolicy,omitempty"`
	// +optional
	Env []corev1.EnvVar `json:"env,omitempty"`

	// +optional
	Volumes []corev1.Volume `json:"volumes,omitempty"`
	// +optional
	WorkDir string `json:"workDir,omitempty"`

	// +optional
	InitContainers []corev1.Container `json:"initContainers,omitempty"`
	// +optional
	SidecarContainers []corev1.Container `json:"sidecarContainers,omitempty"`
	// +optional
	NodeSelector map[string]string `json:"nodeSelector,omitempty"`
	// +optional
	ServiceAccountName string `json:"serviceAccountName,omitempty"`
	// +optional
	AutomountServiceAccountToken *bool `json:"automountServiceAccountToken,omitempty"`
	// +optional
	SecurityContext *corev1.PodSecurityContext `json:"securityContext,omitempty"`
	// +optional
	ImagePullSecrets []corev1.LocalObjectReference `json:"imagePullSecrets,omitempty"`
	// +optional
	Affinity *corev1.Affinity `json:"affinity,omitempty"`
	// +optional
	Tolerations []corev1.Toleration `json:"tolerations,omitempty"`
	// +optional
	EphemeralContainers []corev1.EphemeralContainer `json:"ephemeralContainers,omitempty"`
	// +optional
	TerminationGracePeriodSeconds *int64 `json:"terminationGracePeriodSeconds,omitempty"`
	// +optional
	DockerdWithinRunnerContainer *bool `json:"dockerdWithinRunnerContainer,omitempty"`
	// +optional
	DockerEnabled *bool `json:"dockerEnabled,omitempty"`
}

// ValidateRepository validates repository field.
func (rs *RunnerSpec) ValidateRepository() error {
	// Organization and repository are both exclusive.
	if len(rs.Organization) == 0 && len(rs.Repository) == 0 {
		return errors.New("Spec needs organization or repository")
	}
	if len(rs.Organization) > 0 && len(rs.Repository) > 0 {
		return errors.New("Spec cannot have both organization and repository")
	}

	return nil
}

// RunnerStatus defines the observed state of Runner
type RunnerStatus struct {
	Registration RunnerStatusRegistration `json:"registration"`
	Phase        string                   `json:"phase"`
	Reason       string                   `json:"reason"`
	Message      string                   `json:"message"`
}

// RunnerStatusRegistration contains runner registration status
type RunnerStatusRegistration struct {
	Organization string      `json:"organization,omitempty"`
	Repository   string      `json:"repository,omitempty"`
	Labels       []string    `json:"labels,omitempty"`
	Token        string      `json:"token"`
	ExpiresAt    metav1.Time `json:"expiresAt"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:JSONPath=".spec.organization",name=Organization,type=string
// +kubebuilder:printcolumn:JSONPath=".spec.repository",name=Repository,type=string
// +kubebuilder:printcolumn:JSONPath=".spec.labels",name=Labels,type=string
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

func init() {
	SchemeBuilder.Register(&Runner{}, &RunnerList{})
}
