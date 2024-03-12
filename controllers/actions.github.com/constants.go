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

	// Github labels
	LabelKeyGitHubScaleSetName      = "actions.github.com/scale-set-name"
	LabelKeyGitHubScaleSetNamespace = "actions.github.com/scale-set-namespace"
	LabelKeyGitHubEnterprise        = "actions.github.com/enterprise"
	LabelKeyGitHubOrganization      = "actions.github.com/organization"
	LabelKeyGitHubRepository        = "actions.github.com/repository"
)

// Finalizer used to protect resources from deletion while AutoscalingRunnerSet is running
const AutoscalingRunnerSetCleanupFinalizerName = "actions.github.com/cleanup-protection"

const (
	AnnotationKeyGitHubRunnerGroupName    = "actions.github.com/runner-group-name"
	AnnotationKeyGitHubRunnerScaleSetName = "actions.github.com/runner-scale-set-name"
	AnnotationKeyPatchID                  = "actions.github.com/patch-id"
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

// Rbac managing envs flags
const (
	SkipRbacSetupForController = "SKIP_RBAC_SETUP_FOR_CONTROLLER"

	RequireListenerSAProvided = "SKIP_LISTENERS_SA_CREATION"

	SkipRbacSetupForListeners = "SKIP_RBAC_SETUP_FOR_LISTENERS"
)
