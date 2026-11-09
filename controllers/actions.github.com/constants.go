package actionsgithubcom

import (
	"github.com/actions/actions-runner-controller/logging"
)

const (
	LabelKeyRunnerTemplateHash = "runner-template-hash"
	LabelKeyPodTemplateHash    = "pod-template-hash"
)

const (
	EnvVarRunnerJITConfig      = "ACTIONS_RUNNER_INPUT_JITCONFIG"
	EnvVarRunnerExtraUserAgent = "GITHUB_ACTIONS_RUNNER_EXTRA_USER_AGENT"
	// Environment variable setting the exit code to return when the runner version is deprecated.
	// This is used by the runner to signal to the controller that it should switch off the scaleset.
	EnvVarRunnerDeprecatedExitCode = "ACTIONS_RUNNER_RETURN_VERSION_DEPRECATED_EXIT_CODE"
)

// Labels applied to resources
const (
	// Kubernetes labels
	LabelKeyKubernetesPartOf    = "app.kubernetes.io/part-of"
	LabelKeyKubernetesComponent = "app.kubernetes.io/component"
	LabelKeyKubernetesVersion   = "app.kubernetes.io/version"

	// Well-known Kubernetes node labels
	LabelKeyKubernetesOS = "kubernetes.io/os"

	// Github labels
	LabelKeyGitHubScaleSetName      = "actions.github.com/scale-set-name"
	LabelKeyGitHubScaleSetNamespace = "actions.github.com/scale-set-namespace"
	LabelKeyGitHubEnterprise        = "actions.github.com/enterprise"
	LabelKeyGitHubOrganization      = "actions.github.com/organization"
	LabelKeyGitHubRepository        = "actions.github.com/repository"
)

// AutoscalingRunnerSetCleanupFinalizerName is a finalizer used to protect resources
// from deletion while AutoscalingRunnerSet is running
const AutoscalingRunnerSetCleanupFinalizerName = "actions.github.com/cleanup-protection"

const (
	AnnotationKeyGitHubRunnerGroupName    = "actions.github.com/runner-group-name"
	AnnotationKeyGitHubRunnerScaleSetName = "actions.github.com/runner-scale-set-name"
	AnnotationKeyPatchID                  = "actions.github.com/patch-id"
	// AnnotationKeyAutoscalingRunnerSetGeneration records the AutoscalingRunnerSet
	// generation the current EphemeralRunnerSet spec was derived from. It is
	// informational: it makes it possible to tell, by looking at the set alone,
	// how far behind the AutoscalingRunnerSet it is. Nothing keys behaviour off
	// it - in particular, recovery from the outdated phase is decided by
	// comparing the runner spec itself, not generations.
	AnnotationKeyAutoscalingRunnerSetGeneration = "actions.github.com/autoscaling-runner-set-generation"
	// AnnotationKeyActionableRevision records the EphemeralRunnerSet
	// Spec.ActionableRevision that was in effect when the runner was created. It
	// lets the set tell apart a runner that reported Outdated against the current
	// runner spec from one that reported it against a spec that has since been
	// updated.
	AnnotationKeyActionableRevision = "actions.github.com/actionable-revision"
	// AnnotationKeyListenerConfigResourceVersion records the resource version of
	// the listener config secret the listener pod was created from. The pod
	// mounts that secret and parses it once at startup, so a change to its
	// contents only takes effect after a restart. Nothing about the change is
	// visible in the pod spec, which references the secret by name, so the
	// resource version is carried on the pod to make the drift observable.
	AnnotationKeyListenerConfigResourceVersion = "actions.github.com/listener-config-resource-version"
)

// Labels applied to listener roles
const (
	labelKeyListenerName      = "auto-scaling-listener-name"
	labelKeyListenerNamespace = "auto-scaling-listener-namespace"
)

// Annotations applied for later cleanup of resources
const (
	AnnotationKeyManagerRoleBindingName           = "actions.github.com/cleanup-manager-role-binding"
	AnnotationKeyManagerRoleName                  = "actions.github.com/cleanup-manager-role-name"
	AnnotationKeyKubernetesModeRoleName           = "actions.github.com/cleanup-kubernetes-mode-role-name"
	AnnotationKeyKubernetesModeRoleBindingName    = "actions.github.com/cleanup-kubernetes-mode-role-binding-name"
	AnnotationKeyKubernetesModeServiceAccountName = "actions.github.com/cleanup-kubernetes-mode-service-account-name"
	AnnotationKeyGitHubSecretName                 = "actions.github.com/cleanup-github-secret-name"
	AnnotationKeyNoPermissionServiceAccountName   = "actions.github.com/cleanup-no-permission-service-account-name"
)

// DefaultScaleSetListenerLogLevel is the default log level applied
const DefaultScaleSetListenerLogLevel = string(logging.LogLevelDebug)

// DefaultScaleSetListenerLogFormat is the default log format applied
const DefaultScaleSetListenerLogFormat = string(logging.LogFormatText)

// ownerKey is field selector matching the owner name of a particular resource
const resourceOwnerKey = ".metadata.controller"

// autoscalingRunnerSetOwnerKey indexes an AutoscalingListener by the scale set
// it names as its own. Listeners live in the controller namespace while the
// scale set lives in its own, so they cannot carry an owner reference across
// that boundary and the resourceOwnerKey index does not apply to them.
const autoscalingRunnerSetOwnerKey = ".spec.autoscalingRunnerSet"

// EphemeralRunner pod creation failure reasons
const ReasonInvalidPodFailure = "InvalidPod"
