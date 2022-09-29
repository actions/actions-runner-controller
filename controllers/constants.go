package controllers

import "time"

const (
	LabelKeyRunnerSetName = "runnerset-name"
	LabelKeyRunner        = "actions-runner"
)

const (
	// This names requires at least one slash to work.
	// See https://github.com/google/knative-gcp/issues/378
	runnerPodFinalizerName             = "actions.summerwind.dev/runner-pod"
	runnerLinkedResourcesFinalizerName = "actions.summerwind.dev/linked-resources"

	annotationKeyPrefix = "actions-runner/"

	AnnotationKeyLastRegistrationCheckTime = "actions-runner-controller/last-registration-check-time"

	// AnnotationKeyUnregistrationFailureMessage is the annotation that is added onto the pod once it failed to be unregistered from GitHub due to e.g. 422 error
	AnnotationKeyUnregistrationFailureMessage = annotationKeyPrefix + "unregistration-failure-message"

	// AnnotationKeyUnregistrationCompleteTimestamp is the annotation that is added onto the pod once the previously started unregistration process has been completed.
	AnnotationKeyUnregistrationCompleteTimestamp = annotationKeyPrefix + "unregistration-complete-timestamp"

	// AnnotationKeyRunnerCompletionWaitStartTimestamp is the annotation that is added onto the pod when
	// ARC decided to wait until the pod to complete by itself, without the need for ARC to unregister the corresponding runner.
	AnnotationKeyRunnerCompletionWaitStartTimestamp = annotationKeyPrefix + "runner-completion-wait-start-timestamp"

	// unregistarionStartTimestamp is the annotation that contains the time that the requested unregistration process has been started
	AnnotationKeyUnregistrationStartTimestamp = annotationKeyPrefix + "unregistration-start-timestamp"

	// AnnotationKeyUnregistrationRequestTimestamp is the annotation that contains the time that the unregistration has been requested.
	// This doesn't immediately start the unregistration. Instead, ARC will first check if the runner has already been registered.
	// If not, ARC will hold on until the registration to complete first, and only after that it starts the unregistration process.
	// This is crucial to avoid a race between ARC marking the runner pod for deletion while the actions-runner registers itself to GitHub, leaving the assigned job
	// hang like forever.
	AnnotationKeyUnregistrationRequestTimestamp = annotationKeyPrefix + "unregistration-request-timestamp"

	AnnotationKeyRunnerID = annotationKeyPrefix + "id"

	// This can be any value but a larger value can make an unregistration timeout longer than configured in practice.
	DefaultUnregistrationRetryDelay = time.Minute

	// RetryDelayOnCreateRegistrationError is the delay between retry attempts for runner registration token creation.
	// Usually, a retry in this case happens when e.g. your PAT has no access to certain scope of runners, like you're using repository admin's token
	// for creating a broader scoped runner token, like organizationa or enterprise runner token.
	// Such permission issue will never fixed automatically, so we don't need to retry so often, hence this value.
	RetryDelayOnCreateRegistrationError = 3 * time.Minute

	// registrationTimeout is the duration until a pod times out after it becomes Ready and Running.
	// A pod that is timed out can be terminated if needed.
	registrationTimeout = 10 * time.Minute

	// DefaultRunnerPodRecreationDelayAfterWebhookScale is the delay until syncing the runners with the desired replicas
	// after a webhook-based scale up.
	// This is used to prevent ARC from recreating completed runner pods that are deleted soon without being used at all.
	// In other words, this is used as a timer to wait for the completed runner to emit the next `workflow_job` webhook event to decrease the desired replicas.
	// So if we set 30 seconds for this, you are basically saying that you would assume GitHub and your installation of ARC to
	// emit and propagate a workflow_job completion event down to the RunnerSet or RunnerReplicaSet, vha ARC's github webhook server and HRA, in approximately 30 seconds.
	// In case it actually took more than DefaultRunnerPodRecreationDelayAfterWebhookScale for the workflow_job completion event to arrive,
	// ARC will recreate the completed runner(s), assuming something went wrong in either GitHub, your K8s cluster, or ARC, so ARC needs to resync anyway.
	//
	// See https://github.com/actions-runner-controller/actions-runner-controller/pull/1180
	DefaultRunnerPodRecreationDelayAfterWebhookScale = 10 * time.Minute

	EnvVarRunnerName  = "RUNNER_NAME"
	EnvVarRunnerToken = "RUNNER_TOKEN"

	// defaultHookPath is path to the hook script used when the "containerMode: kubernetes" is specified
	defaultRunnerHookPath = "/runner/k8s/index.js"
)
