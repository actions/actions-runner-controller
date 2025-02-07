package actionssummerwindnet

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/actions/actions-runner-controller/github"
	"github.com/go-logr/logr"
	gogithub "github.com/google/go-github/v52/github"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// tickRunnerGracefulStop reconciles the runner and the runner pod in a way so that
// we can delete the runner pod without disrupting a workflow job.
//
// This function returns a non-nil pointer to corev1.Pod as the first return value
// if the runner is considered to have gracefully stopped, hence its pod is safe for deletion.
//
// It's a "tick" operation so a graceful stop can take multiple calls to complete.
// This function is designed to complete a lengthy graceful stop process in an unblocking way.
// When it wants to be retried later, the function returns a non-nil *ctrl.Result as the second return value, may or may not populating the error in the second return value.
// The caller is expected to return the returned ctrl.Result and error to postpone the current reconciliation loop and trigger a scheduled retry.
func tickRunnerGracefulStop(ctx context.Context, retryDelay time.Duration, log logr.Logger, ghClient *github.Client, c client.Client, enterprise, organization, repository, runner string, pod *corev1.Pod) (*corev1.Pod, *ctrl.Result, error) {
	pod, err := annotatePodOnce(ctx, c, log, pod, AnnotationKeyUnregistrationStartTimestamp, time.Now().Format(time.RFC3339))
	if err != nil {
		return nil, &ctrl.Result{}, err
	}

	if res, err := ensureRunnerUnregistration(ctx, retryDelay, log, ghClient, c, enterprise, organization, repository, runner, pod); res != nil {
		return nil, res, err
	}

	pod, err = annotatePodOnce(ctx, c, log, pod, AnnotationKeyUnregistrationCompleteTimestamp, time.Now().Format(time.RFC3339))
	if err != nil {
		return nil, &ctrl.Result{}, err
	}

	return pod, nil, nil
}

// annotatePodOnce annotates the pod if it wasn't.
// Returns the provided pod as-is if it was already annotated.
// Returns the updated pod if the pod was missing the annotation and the update to add the annotation succeeded.
func annotatePodOnce(ctx context.Context, c client.Client, log logr.Logger, pod *corev1.Pod, k, v string) (*corev1.Pod, error) {
	if pod == nil {
		return nil, nil
	}

	if _, ok := getAnnotation(pod, k); ok {
		return pod, nil
	}

	updated := pod.DeepCopy()
	setAnnotation(&updated.ObjectMeta, k, v)
	if err := c.Patch(ctx, updated, client.MergeFrom(pod)); err != nil {
		log.Error(err, fmt.Sprintf("Failed to patch pod to have %s annotation", k))
		return nil, err
	}

	log.V(2).Info("Annotated pod", "key", k, "value", v)

	return updated, nil
}

// If the first return value is nil, it's safe to delete the runner pod.
func ensureRunnerUnregistration(ctx context.Context, retryDelay time.Duration, log logr.Logger, ghClient *github.Client, c client.Client, enterprise, organization, repository, runner string, pod *corev1.Pod) (*ctrl.Result, error) {
	var runnerID *int64

	if id, ok := getAnnotation(pod, AnnotationKeyRunnerID); ok {
		v, err := strconv.ParseInt(id, 10, 64)
		if err != nil {
			return &ctrl.Result{}, err
		}

		runnerID = &v
	}

	if runnerID == nil {
		runner, err := getRunner(ctx, ghClient, enterprise, organization, repository, runner)
		if err != nil {
			return &ctrl.Result{}, err
		}

		if runner != nil && runner.ID != nil {
			runnerID = runner.ID
		}
	}

	code := runnerContainerExitCode(pod)

	if pod != nil && pod.Annotations[AnnotationKeyUnregistrationCompleteTimestamp] != "" {
		// If it's already unregistered in the previous reconciliation loop,
		// you can safely assume that it won't get registered again so it's safe to delete the runner pod.
		log.Info("Runner pod is marked as already unregistered.")
	} else if runnerID == nil && !runnerPodOrContainerIsStopped(pod) && !podConditionTransitionTimeAfter(pod, corev1.PodReady, registrationTimeout) &&
		!podIsPending(pod) {

		log.Info(
			"Unregistration started before runner obtains ID. Waiting for the registration timeout to elapse, or the runner to obtain ID, or the runner pod to stop",
			"registrationTimeout", registrationTimeout,
		)
		return &ctrl.Result{RequeueAfter: retryDelay}, nil
	} else if runnerID == nil && podIsPending(pod) {
		// Note: This logic is here to prevent a dead-lock between ARC and the PV provider.
		//
		// The author of this logic thinks that some (or all?) of CSI plugins and PV providers
		// do not support provisioning dynamic PVs for a pod that is already marked for deletion.
		// If we didn't handle this case here, ARC would end up with waiting forever until the
		// PV provider(s) provision PVs for the pod, which seems to never happen.
		//
		// For reference, the below is an example of pod.status that you might see when it happened:
		// status:
		//  conditions:
		//  - lastProbeTime: null
		//    lastTransitionTime: "2022-11-04T00:04:05Z"
		//    message: 'binding rejected: running Bind plugin "DefaultBinder": Operation cannot
		//      be fulfilled on pods/binding "org-runnerdeploy-xv2lg-pm6t2": pod org-runnerdeploy-xv2lg-pm6t2
		//      is being deleted, cannot be assigned to a host'
		//    reason: SchedulerError
		//    status: "False"
		//    type: PodScheduled
		//  phase: Pending
		//  qosClass: BestEffort
		log.Info(
			"Unregistration started before runner pod gets scheduled onto a node. "+
				"Perhaps the runner is taking a long time due to e.g. slow CSI slugin not giving us a PV in a timely manner, or your Kubernetes cluster is overloaded? "+
				"Marking unregistration as completed anyway because there's nothing ARC can do.",
			"registrationTimeout", registrationTimeout,
		)
	} else if runnerID == nil && runnerPodOrContainerIsStopped(pod) {
		log.Info(
			"Unregistration started before runner ID is assigned and the runner stopped before obtaining ID within registration timeout. "+
				"Perhaps the runner successfully ran the job and stopped normally before the runner ID becomes visible via GitHub API? "+
				"Perhaps the runner pod was terminated by anyone other than ARC? Was it OOM killed? "+
				"Marking unregistration as completed anyway because there's nothing ARC can do.",
			"registrationTimeout", registrationTimeout,
		)
	} else if runnerID == nil && podConditionTransitionTimeAfter(pod, corev1.PodReady, registrationTimeout) {
		log.Info(
			"Unregistration started before runner ID is assigned and the runner was unable to obtain ID within registration timeout. "+
				"Perhaps the runner has communication issue, or a firewall egress rule is dropping traffic to GitHub API, or GitHub API is unavailable? "+
				"Marking unregistration as completed anyway because there's nothing ARC can do. "+
				"This may result in cancelling the job depending on your terminationGracePeriodSeconds and RUNNER_GRACEFUL_STOP_TIMEOUT settings.",
			"registrationTimeout", registrationTimeout,
		)
	} else if pod != nil && runnerPodOrContainerIsStopped(pod) {
		// If it's an ephemeral runner with the actions/runner container exited with 0,
		// we can safely assume that it has unregistered itself from GitHub Actions
		// so it's natural that RemoveRunner fails due to 404.

		// If pod has ended up succeeded we need to restart it
		// Happens e.g. when dind is in runner and run completes
		log.Info("Runner pod has been stopped with a successful status.")
	} else if pod != nil && pod.Annotations[AnnotationKeyRunnerCompletionWaitStartTimestamp] != "" {
		ct := ephemeralRunnerContainerStatus(pod)
		if ct == nil {
			log.Info("Runner pod is annotated to wait for completion, and the runner container is not ephemeral")

			return &ctrl.Result{RequeueAfter: retryDelay}, nil
		}

		lts := ct.LastTerminationState.Terminated
		if lts == nil {
			log.Info("Runner pod is annotated to wait for completion, and the runner container is not restarting")

			return &ctrl.Result{RequeueAfter: retryDelay}, nil
		}

		// Prevent runner pod from stucking in Terminating.
		// See https://github.com/actions/actions-runner-controller/issues/1369
		log.Info("Deleting runner pod anyway because it has stopped prematurely. This may leave a dangling runner resource in GitHub Actions",
			"lastState.exitCode", lts.ExitCode,
			"lastState.message", lts.Message,
			"pod.phase", pod.Status.Phase,
		)
	} else if ok, err := unregisterRunner(ctx, ghClient, enterprise, organization, repository, *runnerID); err != nil {
		if errors.Is(err, &gogithub.RateLimitError{}) {
			// We log the underlying error when we failed calling GitHub API to list or unregisters,
			// or the runner is still busy.
			log.Error(
				err,
				fmt.Sprintf(
					"Failed to unregister runner due to GitHub API rate limits. Delaying retry for %s to avoid excessive GitHub API calls",
					retryDelayOnGitHubAPIRateLimitError,
				),
			)

			return &ctrl.Result{RequeueAfter: retryDelayOnGitHubAPIRateLimitError}, err
		}

		log.V(1).Info("Failed to unregister runner before deleting the pod.", "error", err)

		var (
			runnerBusy                         bool
			runnerUnregistrationFailureMessage string
		)

		errRes := &gogithub.ErrorResponse{}
		if errors.As(err, &errRes) {
			if errRes.Response.StatusCode == 403 {
				log.Error(err, "Unable to unregister due to permission error. "+
					"Perhaps you've changed the permissions of PAT or GitHub App, or you updated authentication method of ARC in a wrong way? "+
					"ARC considers it as already unregistered and continue removing the pod. "+
					"You may need to remove the runner on GitHub UI.")

				return nil, nil
			}

			runner, _ := getRunner(ctx, ghClient, enterprise, organization, repository, runner)

			var runnerID int64

			if runner != nil && runner.ID != nil {
				runnerID = *runner.ID
			}

			runnerBusy = errRes.Response.StatusCode == 422
			runnerUnregistrationFailureMessage = errRes.Message

			if runnerBusy && code != nil {
				log.V(2).Info("Runner container has already stopped but the unregistration attempt failed. "+
					"This can happen when the runner container crashed due to an unhandled error, OOM, etc. "+
					"ARC terminates the pod anyway. You'd probably need to manually delete the runner later by calling the GitHub API",
					"runnerExitCode", *code,
					"runnerID", runnerID,
				)

				return nil, nil
			}
		}

		if runnerBusy {
			_, err := annotatePodOnce(ctx, c, log, pod, AnnotationKeyUnregistrationFailureMessage, runnerUnregistrationFailureMessage)
			if err != nil {
				return &ctrl.Result{}, err
			}

			// We want to prevent spamming the deletion attemps but returning ctrl.Result with RequeueAfter doesn't
			// work as the reconciliation can happen earlier due to pod status update.
			// For ephemeral runners, we can expect it to stop and unregister itself on completion.
			// So we can just wait for the completion without actively retrying unregistration.
			ephemeral := getRunnerEnv(pod, EnvVarEphemeral)
			if ephemeral == "true" {
				_, err = annotatePodOnce(ctx, c, log, pod, AnnotationKeyRunnerCompletionWaitStartTimestamp, time.Now().Format(time.RFC3339))
				if err != nil {
					return &ctrl.Result{}, err
				}

				return &ctrl.Result{}, nil
			}

			log.V(2).Info("Retrying runner unregistration because the static runner is still busy")
			// Otherwise we may end up spamming 422 errors,
			// each call consuming GitHub API rate limit
			// https://github.com/actions/actions-runner-controller/pull/1167#issuecomment-1064213271
			return &ctrl.Result{RequeueAfter: retryDelay}, nil
		}

		return &ctrl.Result{}, err
	} else if ok {
		log.Info("Runner has just been unregistered.")
	} else if pod == nil {
		// `r.unregisterRunner()` will return `false, nil` if the runner is not found on GitHub.
		// However, that doesn't always mean the pod can be safely removed.
		//
		// If the pod does not exist for the runner,
		// it may be due to that the runner pod has never been created.
		// In that case we can safely assume that the runner will never be registered.

		log.Info("Runner was not found on GitHub and the runner pod was not found on Kuberntes.")
	} else if ts := pod.Annotations[AnnotationKeyUnregistrationStartTimestamp]; ts != "" {
		log.Info("Runner unregistration is in-progress. It can take forever to complete if it's a static runner constantly running jobs."+
			" It can also take very long time if it's an ephemeral runner that is running a log-running job.", "error", err)

		return &ctrl.Result{RequeueAfter: retryDelay}, nil
	} else {
		// A runner and a runner pod that is created by this version of ARC should match
		// any of the above branches.
		//
		// But we leave this match all branch for potential backward-compatibility.
		// The caller is expected to take appropriate actions, like annotating the pod as started the unregistration process,
		// and retry later.
		log.V(1).Info("Runner unregistration is being retried later.")

		return &ctrl.Result{RequeueAfter: retryDelay}, nil
	}

	return nil, nil
}

func ensureRunnerPodRegistered(ctx context.Context, log logr.Logger, ghClient *github.Client, c client.Client, enterprise, organization, repository, runner string, pod *corev1.Pod) (*corev1.Pod, *ctrl.Result, error) {
	_, hasRunnerID := getAnnotation(pod, AnnotationKeyRunnerID)
	if runnerPodOrContainerIsStopped(pod) || hasRunnerID {
		return pod, nil, nil
	}

	r, err := getRunner(ctx, ghClient, enterprise, organization, repository, runner)
	if err != nil {
		return nil, &ctrl.Result{RequeueAfter: 10 * time.Second}, err
	}

	if r == nil || r.ID == nil {
		return nil, &ctrl.Result{RequeueAfter: 10 * time.Second}, err
	}

	id := *r.ID

	updated, err := annotatePodOnce(ctx, c, log, pod, AnnotationKeyRunnerID, fmt.Sprintf("%d", id))
	if err != nil {
		return nil, &ctrl.Result{RequeueAfter: 10 * time.Second}, err
	}

	return updated, nil, nil
}

func getAnnotation(obj client.Object, key string) (string, bool) {
	if obj.GetAnnotations() == nil {
		return "", false
	}

	v, ok := obj.GetAnnotations()[key]

	return v, ok
}

func setAnnotation(meta *metav1.ObjectMeta, key, value string) {
	if meta.Annotations == nil {
		meta.Annotations = map[string]string{}
	}

	meta.Annotations[key] = value
}

func podConditionTransitionTime(pod *corev1.Pod, tpe corev1.PodConditionType, v corev1.ConditionStatus) *metav1.Time {
	for _, c := range pod.Status.Conditions {
		if c.Type == tpe && c.Status == v {
			return &c.LastTransitionTime
		}
	}

	return nil
}

func podConditionTransitionTimeAfter(pod *corev1.Pod, tpe corev1.PodConditionType, d time.Duration) bool {
	c := podConditionTransitionTime(pod, tpe, corev1.ConditionTrue)
	if c == nil {
		return false
	}

	return c.Add(d).Before(time.Now())
}

func podIsPending(pod *corev1.Pod) bool {
	return pod.Status.Phase == corev1.PodPending
}

func podRunnerID(pod *corev1.Pod) string {
	id, _ := getAnnotation(pod, AnnotationKeyRunnerID)
	return id
}

func getRunnerEnv(pod *corev1.Pod, key string) string {
	for _, c := range pod.Spec.Containers {
		if c.Name == containerName {
			for _, e := range c.Env {
				if e.Name == key {
					return e.Value
				}
			}
		}
	}
	return ""
}

func setRunnerEnv(pod *corev1.Pod, key, value string) {
	for i := range pod.Spec.Containers {
		c := pod.Spec.Containers[i]
		if c.Name == containerName {
			for j, env := range c.Env {
				if env.Name == key {
					pod.Spec.Containers[i].Env[j].Value = value
					return
				}
			}
			pod.Spec.Containers[i].Env = append(c.Env, corev1.EnvVar{Name: key, Value: value})
		}
	}
}

// unregisterRunner unregisters the runner from GitHub Actions by name.
//
// This function returns:
//
// Case 1. (true, nil) when it has successfully unregistered the runner.
// Case 2. (false, nil) when (2-1.) the runner has been already unregistered OR (2-2.) the runner will never be created OR (2-3.) the runner is not created yet and it is about to be registered(hence we couldn't see it's existence from GitHub Actions API yet)
// Case 3. (false, err) when it postponed unregistration due to the runner being busy, or it tried to unregister the runner but failed due to
//
//	an error returned by GitHub API.
//
// When the returned values is "Case 2. (false, nil)", the caller must handle the three possible sub-cases appropriately.
// In other words, all those three sub-cases cannot be distinguished by this function alone.
//
//   - Case "2-1." can happen when e.g. ARC has successfully unregistered in a previous reconciliation loop or it was an ephemeral runner that finished its job run(an ephemeral runner is designed to stop after a job run).
//     You'd need to maintain the runner state(i.e. if it's already unregistered or not) somewhere,
//     so that you can either not call this function at all if the runner state says it's already unregistered, or determine that it's case "2-1." when you got (false, nil).
//
//   - Case "2-2." can happen when e.g. the runner registration token was somehow broken so that `config.sh` within the runner container was never meant to succeed.
//     Waiting and retrying forever on this case is not a solution, because `config.sh` won't succeed with a wrong token hence the runner gets stuck in this state forever.
//     There isn't a perfect solution to this, but a practical workaround would be implement a "grace period" in the caller side.
//
//   - Case "2-3." can happen when e.g. ARC recreated an ephemeral runner pod in a previous reconciliation loop and then it was requested to delete the runner before the runner comes up.
//     If handled inappropriately, this can cause a race condition between a deletion of the runner pod and GitHub scheduling a workflow job onto the runner.
//
// Once successfully detected case "2-1." or "2-2.", you can safely delete the runner pod because you know that the runner won't come back
// as long as you recreate the runner pod.
//
// If it was "2-3.", you need a workaround to avoid the race condition.
//
// You shall introduce a "grace period" mechanism, similar or equal to that is required for "Case 2-2.", so that you ever
// start the runner pod deletion only after it's more and more likely that the runner pod is not coming up.
//
// Beware though, you need extra care to set an appropriate grace period depending on your environment.
// There isn't a single right grace period that works for everyone.
// The longer the grace period is, the earlier a cluster resource shortage can occur due to throttled runner pod deletions,
// while the shorter the grace period is, the more likely you may encounter the race issue.
func unregisterRunner(ctx context.Context, client *github.Client, enterprise, org, repo string, id int64) (bool, error) {
	// For the record, historically ARC did not try to call RemoveRunner on a busy runner, but it's no longer true.
	// The reason ARC did so was to let a runner running a job to not stop prematurely.
	//
	// However, we learned that RemoveRunner already has an ability to prevent stopping a busy runner,
	// so ARC doesn't need to do anything special for a graceful runner stop.
	// It can just call RemoveRunner, and if it returned 200 you're guaranteed that the runner will not automatically come back and
	// the runner pod is safe for deletion.
	//
	// Trying to remove a busy runner can result in errors like the following:
	//    failed to remove runner: DELETE https://api.github.com/repos/actions-runner-controller/mumoshu-actions-test/actions/runners/47: 422 Bad request - Runner \"example-runnerset-0\" is still running a job\" []
	//
	// # NOTES
	//
	// - It can be "status=offline" at the same time but that's another story.
	// - After https://github.com/actions/actions-runner-controller/pull/1127, ListRunners responses that are used to
	//   determine if the runner is busy can be more outdated than before, as those responses are now cached for 60 seconds.
	// - Note that 60 seconds is controlled by the Cache-Control response header provided by GitHub so we don't have a strict control on it but we assume it won't
	//   change from 60 seconds.
	//
	// TODO: Probably we can just remove the runner by ID without seeing if the runner is busy, by treating it as busy when a remove-runner call failed with 422?
	if err := client.RemoveRunner(ctx, enterprise, org, repo, id); err != nil {
		return false, err
	}

	return true, nil
}

func getRunner(ctx context.Context, client *github.Client, enterprise, org, repo, name string) (*gogithub.Runner, error) {
	runners, err := client.ListRunners(ctx, enterprise, org, repo)
	if err != nil {
		return nil, err
	}

	for _, runner := range runners {
		if runner.GetName() == name {
			return runner, nil
		}
	}

	return nil, nil
}
