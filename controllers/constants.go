package controllers

import "time"

const (
	LabelKeyRunnerSetName = "runnerset-name"
)

const (
	// This names requires at least one slash to work.
	// See https://github.com/google/knative-gcp/issues/378
	runnerPodFinalizerName = "actions.summerwind.dev/runner-pod"

	annotationKeyPrefix = "actions-runner/"

	AnnotationKeyLastRegistrationCheckTime = "actions-runner-controller/last-registration-check-time"

	// AnnotationKeyUnregistrationCompleteTimestamp is the annotation that is added onto the pod once the previously started unregistration process has been completed.
	AnnotationKeyUnregistrationCompleteTimestamp = annotationKeyPrefix + "unregistration-complete-timestamp"

	// unregistarionStartTimestamp is the annotation that contains the time that the requested unregistration process has been started
	AnnotationKeyUnregistrationStartTimestamp = annotationKeyPrefix + "unregistration-start-timestamp"

	// AnnotationKeyUnregistrationRequestTimestamp is the annotation that contains the time that the unregistration has been requested.
	// This doesn't immediately start the unregistration. Instead, ARC will first check if the runner has already been registered.
	// If not, ARC will hold on until the registration to complete first, and only after that it starts the unregistration process.
	// This is crucial to avoid a race between ARC marking the runner pod for deletion while the actions-runner registers itself to GitHub, leaving the assigned job
	// hang like forever.
	AnnotationKeyUnregistrationRequestTimestamp = annotationKeyPrefix + "unregistration-request-timestamp"

	AnnotationKeyRunnerID = annotationKeyPrefix + "id"

	// DefaultUnregistrationTimeout is the duration until ARC gives up retrying the combo of ListRunners API (to detect the runner ID by name)
	// and RemoveRunner API (to actually unregister the runner) calls.
	// This needs to be longer than 60 seconds because a part of the combo, the ListRunners API, seems to use the Cache-Control header of max-age=60s
	// and that instructs our cache library httpcache to cache responses for 60 seconds, which results in ARC unable to see the runner in the ListRunners response
	// up to 60 seconds (or even more depending on the situation).
	DefaultUnregistrationTimeout = 60 * time.Second

	// This can be any value but a larger value can make an unregistration timeout longer than configured in practice.
	DefaultUnregistrationRetryDelay = 30 * time.Second
)
