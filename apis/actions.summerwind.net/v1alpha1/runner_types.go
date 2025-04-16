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
	"fmt"

	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/util/validation/field"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// RunnerSpec defines the desired state of Runner
type RunnerSpec struct {
	RunnerConfig  `json:",inline"`
	RunnerPodSpec `json:",inline"`
}

type RunnerConfig struct {
	// +optional
	// +kubebuilder:validation:Pattern=`^[^/]+$`
	Enterprise string `json:"enterprise,omitempty"`

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
	Ephemeral *bool `json:"ephemeral,omitempty"`

	// +optional
	Image string `json:"image"`

	// +optional
	WorkDir string `json:"workDir,omitempty"`

	// +optional
	DockerdWithinRunnerContainer *bool `json:"dockerdWithinRunnerContainer,omitempty"`
	// +optional
	DockerEnabled *bool `json:"dockerEnabled,omitempty"`
	// +optional
	DockerMTU *int64 `json:"dockerMTU,omitempty"`
	// +optional
	DockerRegistryMirror *string `json:"dockerRegistryMirror,omitempty"`
	// +optional
	DockerVarRunVolumeSizeLimit *resource.Quantity `json:"dockerVarRunVolumeSizeLimit,omitempty"`
	// +optional
	VolumeSizeLimit *resource.Quantity `json:"volumeSizeLimit,omitempty"`
	// +optional
	VolumeStorageMedium *string `json:"volumeStorageMedium,omitempty"`

	// +optional
	ContainerMode string `json:"containerMode,omitempty"`

	GitHubAPICredentialsFrom *GitHubAPICredentialsFrom `json:"githubAPICredentialsFrom,omitempty"`
}

type GitHubAPICredentialsFrom struct {
	SecretRef SecretReference `json:"secretRef,omitempty"`
}

type SecretReference struct {
	Name string `json:"name"`
}

// RunnerPodSpec defines the desired pod spec fields of the runner pod
type RunnerPodSpec struct {
	// +optional
	DockerdContainerResources corev1.ResourceRequirements `json:"dockerdContainerResources,omitempty"`

	// +optional
	DockerVolumeMounts []corev1.VolumeMount `json:"dockerVolumeMounts,omitempty"`

	// +optional
	DockerEnv []corev1.EnvVar `json:"dockerEnv,omitempty"`

	// +optional
	Containers []corev1.Container `json:"containers,omitempty"`

	// +optional
	ImagePullPolicy corev1.PullPolicy `json:"imagePullPolicy,omitempty"`

	// +optional
	Env []corev1.EnvVar `json:"env,omitempty"`

	// +optional
	EnvFrom []corev1.EnvFromSource `json:"envFrom,omitempty"`

	// +optional
	Resources corev1.ResourceRequirements `json:"resources,omitempty"`

	// +optional
	VolumeMounts []corev1.VolumeMount `json:"volumeMounts,omitempty"`

	// +optional
	Volumes []corev1.Volume `json:"volumes,omitempty"`

	// +optional
	EnableServiceLinks *bool `json:"enableServiceLinks,omitempty"`

	// +optional
	InitContainers []corev1.Container `json:"initContainers,omitempty"`

	// +optional
	NodeSelector map[string]string `json:"nodeSelector,omitempty"`

	// +optional
	ServiceAccountName string `json:"serviceAccountName,omitempty"`

	// +optional
	AutomountServiceAccountToken *bool `json:"automountServiceAccountToken,omitempty"`

	// +optional
	SidecarContainers []corev1.Container `json:"sidecarContainers,omitempty"`

	// +optional
	SecurityContext *corev1.PodSecurityContext `json:"securityContext,omitempty"`

	// +optional
	ImagePullSecrets []corev1.LocalObjectReference `json:"imagePullSecrets,omitempty"`

	// +optional
	Affinity *corev1.Affinity `json:"affinity,omitempty"`

	// +optional
	Tolerations []corev1.Toleration `json:"tolerations,omitempty"`

	// +optional
	PriorityClassName string `json:"priorityClassName,omitempty"`

	// +optional
	TerminationGracePeriodSeconds *int64 `json:"terminationGracePeriodSeconds,omitempty"`

	// +optional
	EphemeralContainers []corev1.EphemeralContainer `json:"ephemeralContainers,omitempty"`

	// +optional
	HostAliases []corev1.HostAlias `json:"hostAliases,omitempty"`

	// +optional
	TopologySpreadConstraints []corev1.TopologySpreadConstraint `json:"topologySpreadConstraints,omitempty"`

	// RuntimeClassName is the container runtime configuration that containers should run under.
	// More info: https://kubernetes.io/docs/concepts/containers/runtime-class
	// +optional
	RuntimeClassName *string `json:"runtimeClassName,omitempty"`

	// +optional
	DnsPolicy corev1.DNSPolicy `json:"dnsPolicy,omitempty"`

	// +optional
	DnsConfig *corev1.PodDNSConfig `json:"dnsConfig,omitempty"`

	// +optional
	WorkVolumeClaimTemplate *WorkVolumeClaimTemplate `json:"workVolumeClaimTemplate,omitempty"`
}

func (rs *RunnerSpec) Validate(rootPath *field.Path) field.ErrorList {
	var (
		errList field.ErrorList
		err     error
	)

	err = rs.validateRepository()
	if err != nil {
		errList = append(errList, field.Invalid(rootPath.Child("repository"), rs.Repository, err.Error()))
	}

	err = rs.validateWorkVolumeClaimTemplate()
	if err != nil {
		errList = append(errList, field.Invalid(rootPath.Child("workVolumeClaimTemplate"), rs.WorkVolumeClaimTemplate, err.Error()))
	}

	return errList
}

// ValidateRepository validates repository field.
func (rs *RunnerSpec) validateRepository() error {
	// Enterprise, Organization and repository are both exclusive.
	foundCount := 0
	if len(rs.Organization) > 0 {
		foundCount += 1
	}
	if len(rs.Repository) > 0 {
		foundCount += 1
	}
	if len(rs.Enterprise) > 0 {
		foundCount += 1
	}
	if foundCount == 0 {
		return errors.New("Spec needs enterprise, organization or repository")
	}
	if foundCount > 1 {
		return errors.New("Spec cannot have many fields defined enterprise, organization and repository")
	}

	return nil
}

func (rs *RunnerSpec) validateWorkVolumeClaimTemplate() error {
	if rs.ContainerMode != "kubernetes" {
		return nil
	}

	if rs.WorkVolumeClaimTemplate == nil {
		return errors.New("Spec.ContainerMode: kubernetes must have workVolumeClaimTemplate field specified")
	}

	return rs.WorkVolumeClaimTemplate.validate()
}

// RunnerStatus defines the observed state of Runner
type RunnerStatus struct {
	// Turns true only if the runner pod is ready.
	// +optional
	Ready bool `json:"ready"`
	// +optional
	Registration RunnerStatusRegistration `json:"registration"`
	// +optional
	Phase string `json:"phase,omitempty"`
	// +optional
	Reason string `json:"reason,omitempty"`
	// +optional
	Message string `json:"message,omitempty"`
	// +optional
	WorkflowStatus *WorkflowStatus `json:"workflow"`
	// +optional
	// +nullable
	LastRegistrationCheckTime *metav1.Time `json:"lastRegistrationCheckTime,omitempty"`
}

// WorkflowStatus contains various information that is propagated
// from GitHub Actions workflow run environment variables to
// ease monitoring workflow run/job/steps that are triggerred on the runner.
type WorkflowStatus struct {
	// +optional
	// Name is the name of the workflow
	// that is triggerred within the runner.
	// It corresponds to GITHUB_WORKFLOW defined in
	// https://docs.github.com/en/actions/learn-github-actions/environment-variables
	Name string `json:"name,omitempty"`
	// +optional
	// Repository is the owner and repository name of the workflow
	// that is triggerred within the runner.
	// It corresponds to GITHUB_REPOSITORY defined in
	// https://docs.github.com/en/actions/learn-github-actions/environment-variables
	Repository string `json:"repository,omitempty"`
	// +optional
	// ReositoryOwner is the repository owner's name for the workflow
	// that is triggerred within the runner.
	// It corresponds to GITHUB_REPOSITORY_OWNER defined in
	// https://docs.github.com/en/actions/learn-github-actions/environment-variables
	RepositoryOwner string `json:"repositoryOwner,omitempty"`
	// +optional
	// GITHUB_RUN_NUMBER is the unique number for the current workflow run
	// that is triggerred within the runner.
	// It corresponds to GITHUB_RUN_ID defined in
	// https://docs.github.com/en/actions/learn-github-actions/environment-variables
	RunNumber string `json:"runNumber,omitempty"`
	// +optional
	// RunID is the unique number for the current workflow run
	// that is triggerred within the runner.
	// It corresponds to GITHUB_RUN_ID defined in
	// https://docs.github.com/en/actions/learn-github-actions/environment-variables
	RunID string `json:"runID,omitempty"`
	// +optional
	// Job is the name of the current job
	// that is triggerred within the runner.
	// It corresponds to GITHUB_JOB defined in
	// https://docs.github.com/en/actions/learn-github-actions/environment-variables
	Job string `json:"job,omitempty"`
	// +optional
	// Action is the name of the current action or the step ID of the current step
	// that is triggerred within the runner.
	// It corresponds to GITHUB_ACTION defined in
	// https://docs.github.com/en/actions/learn-github-actions/environment-variables
	Action string `json:"action,omitempty"`
}

// RunnerStatusRegistration contains runner registration status
type RunnerStatusRegistration struct {
	Enterprise   string      `json:"enterprise,omitempty"`
	Organization string      `json:"organization,omitempty"`
	Repository   string      `json:"repository,omitempty"`
	Labels       []string    `json:"labels,omitempty"`
	Token        string      `json:"token"`
	ExpiresAt    metav1.Time `json:"expiresAt"`
}

type WorkVolumeClaimTemplate struct {
	StorageClassName string                              `json:"storageClassName"`
	AccessModes      []corev1.PersistentVolumeAccessMode `json:"accessModes"`
	Resources        corev1.VolumeResourceRequirements   `json:"resources"`
}

func (w *WorkVolumeClaimTemplate) validate() error {
	if len(w.AccessModes) == 0 {
		return errors.New("access mode should have at least one mode specified")
	}

	for _, accessMode := range w.AccessModes {
		switch accessMode {
		case corev1.ReadWriteOnce, corev1.ReadWriteMany:
		default:
			return fmt.Errorf("access mode %v is not supported", accessMode)
		}
	}
	return nil
}

func (w *WorkVolumeClaimTemplate) V1Volume() corev1.Volume {
	return corev1.Volume{
		Name: "work",
		VolumeSource: corev1.VolumeSource{
			Ephemeral: &corev1.EphemeralVolumeSource{
				VolumeClaimTemplate: &corev1.PersistentVolumeClaimTemplate{
					Spec: corev1.PersistentVolumeClaimSpec{
						AccessModes:      w.AccessModes,
						StorageClassName: &w.StorageClassName,
						Resources:        w.Resources,
					},
				},
			},
		},
	}
}

func (w *WorkVolumeClaimTemplate) V1VolumeMount(mountPath string) corev1.VolumeMount {
	return corev1.VolumeMount{
		MountPath: mountPath,
		Name:      "work",
	}
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:JSONPath=".spec.enterprise",name=Enterprise,type=string
// +kubebuilder:printcolumn:JSONPath=".spec.organization",name=Organization,type=string
// +kubebuilder:printcolumn:JSONPath=".spec.repository",name=Repository,type=string
// +kubebuilder:printcolumn:JSONPath=".spec.group",name=Group,type=string
// +kubebuilder:printcolumn:JSONPath=".spec.labels",name=Labels,type=string
// +kubebuilder:printcolumn:JSONPath=".status.phase",name=Status,type=string
// +kubebuilder:printcolumn:JSONPath=".status.message",name=Message,type=string
// +kubebuilder:printcolumn:JSONPath=".status.workflow.repository",name=WF Repo,type=string
// +kubebuilder:printcolumn:JSONPath=".status.workflow.runID",name=WF Run,type=string
// +kubebuilder:printcolumn:name="Age",type="date",JSONPath=".metadata.creationTimestamp"

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
	return !r.Status.Registration.ExpiresAt.Before(&now)
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
