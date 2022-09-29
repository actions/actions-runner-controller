package controllers

import corev1 "k8s.io/api/core/v1"

// Force the runner pod managed by either RunnerDeployment and RunnerSet to have restartPolicy=Never.
// See https://github.com/actions-runner-controller/actions-runner-controller/issues/1369 for more context.
//
// This is to prevent runner pods from stucking in Terminating when a K8s node disappeared along with the runnr pod and the runner container within it.
//
// Previously, we used restartPolicy of OnFailure, it turned wrong later, and therefore we now set Never.
//
// When the restartPolicy is OnFailure and the node disappeared, runner pods on the node seem to stuck in state.terminated==nil, state.waiting!=nil, and state.lastTerminationState!=nil,
// and will ever become Running.
// It's probably due to that the node onto which the pods have been scheduled will ever come back, hence the container restart attempt swill ever succeed,
// the pods stuck waiting for successful restarts forever.
//
// By forcing runner pods to never restart, we hope there will be no chances of pods being stuck waiting.
func forceRunnerPodRestartPolicyNever(pod *corev1.Pod) {
	if pod.Spec.RestartPolicy != corev1.RestartPolicyNever {
		pod.Spec.RestartPolicy = corev1.RestartPolicyNever
	}
}
