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

// Environment variable names used to set proxy variables for containers
const (
	EnvVarHTTPProxy  = "http_proxy"
	EnvVarHTTPSProxy = "https_proxy"
	EnvVarNoProxy    = "no_proxy"
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

	// LabelKeyGitHubRunnerVariant names the runnerVariant an EphemeralRunnerSet
	// belongs to when an AutoscalingRunnerSet declares more than one variant. It
	// is not set on the single (default) variant, so existing scale sets keep
	// their labels unchanged.
	LabelKeyGitHubRunnerVariant = "actions.github.com/runner-variant"
)

// AutoscalingRunnerSetCleanupFinalizerName is a finalizer used to protect resources
// from deletion while AutoscalingRunnerSet is running
const AutoscalingRunnerSetCleanupFinalizerName = "actions.github.com/cleanup-protection"

const (
	AnnotationKeyGitHubRunnerGroupName    = "actions.github.com/runner-group-name"
	AnnotationKeyGitHubRunnerScaleSetName = "actions.github.com/runner-scale-set-name"
	AnnotationKeyPatchID                  = "actions.github.com/patch-id"

	// AnnotationKeyGitHubRunnerScaleSetIDs carries the JSON map of
	// runnerVariant name to registered GitHub runner-scale-set id, set on the
	// AutoscalingRunnerSet only when it declares more than one variant. The
	// existing scalar runner-scale-set-id annotation is kept for the single
	// (default) variant so existing objects are untouched on upgrade.
	AnnotationKeyGitHubRunnerScaleSetIDs = "actions.github.com/runner-scale-set-ids"
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

// EphemeralRunner pod creation failure reasons
const (
	ReasonTooManyPodFailures = "TooManyPodFailures"
	ReasonInvalidPodFailure  = "InvalidPod"
)
