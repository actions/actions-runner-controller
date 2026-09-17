#!/bin/bash

# Outdated runner lifecycle.
#
# A runner that exits with code 7 has rejected the runner spec it was handed.
# This test drives that end to end: it installs a scale set with minRunners=1
# and a runner image old enough for the service to reject, queues a job so a
# runner is actually created, and then asserts the parked state and the
# recovery.
#
# The parked state is deliberately not a teardown. The AutoscalingListener
# object survives with its spec and finalizer intact; only its phase moves to
# Stopped, and the listener controller removes the listener *pod* and the child
# resources. Asserting that the listener object is gone would be wrong, so the
# assertions below check the object exists, its .spec.phase is Stopped, and the
# pod named after it is absent.
#
# The phase is also sticky: it is left only when the runner spec itself changes.
# assert_sticky_to_unrelated_change pins that by upgrading minRunners, which
# bumps the AutoscalingRunnerSet generation without touching anything the
# runners objected to, and confirming nothing restarts.
#
# Switched off is not the same as frozen, though, and that is the other half of
# the same step. The minRunners edit still has to reach the parked objects: the
# listener takes the new value while its phase stays Stopped, and the
# EphemeralRunnerSet takes it while staying pinned at Replicas=0, PatchID=0. The
# invariant is "switched off but still current", so the listener's phase and its
# minRunners are read together rather than across two calls that could straddle
# a change.

set -euo pipefail

DIR="$(realpath "$(dirname "${BASH_SOURCE[0]}")")"

ROOT_DIR="$(realpath "${DIR}/../..")"

source "${DIR}/helper.sh" || {
    echo "Failed to source helper.sh"
    exit 1
}

export VERSION="$(chart_version "${ROOT_DIR}/charts/gha-runner-scale-set-controller-experimental/Chart.yaml")"

SCALE_SET_NAME="outdated-lifecycle-$(date '+%M%S')$(((RANDOM + 100) % 100 + 1))"
SCALE_SET_NAMESPACE="arc-runners"
WORKFLOW_FILE="arc-test-workflow.yaml"
ARC_NAME="arc"
ARC_NAMESPACE="arc-systems"

# The runner only reports that it is outdated if it is both old enough for the
# service to reject it and new enough to know how to say so.
#
# Exit 7 is Constants.Runner.ReturnCode.RunnerVersionDeprecated, added to
# actions/runner in #4285 and first shipped in v2.333.0. Older runners are
# rejected just the same, but they report it as TerminatedError (exit 1), which
# ARC reads as a failed runner and retries forever - the scale set churns and
# never reaches the Outdated phase. So a *newer* image is required here, not an
# older one, which is the opposite of the intuition.
#
# v2.333.0 is the floor of that range, and the floor is the durable choice: it
# is already well past the service's deprecation cutoff, and a version that is
# deprecated today stays deprecated. Do not "make this safer" by moving it
# older - below v2.333.0 the runner loses the ability to report exit 7 at all.
OUTDATED_RUNNER_IMAGE="ghcr.io/actions/actions-runner:2.333.0"
RECOVERED_RUNNER_IMAGE="ghcr.io/actions/actions-runner:latest"

# The EphemeralRunnerSet is named after the AutoscalingRunnerSet, which is named
# after the Helm release.
RUNNER_SET_NAME="${SCALE_SET_NAME}"

LISTENER_SELECTOR="actions.github.com/scale-set-name=${SCALE_SET_NAME},actions.github.com/scale-set-namespace=${SCALE_SET_NAMESPACE}"
RUNNER_POD_SELECTOR="actions.github.com/scale-set-name=${SCALE_SET_NAME}"

# How long to wait for a runner to be created, reject the spec and for the
# parked state to settle. This covers pulling the runner image, registering and
# being turned away, so it is generous on purpose.
OUTDATED_TIMEOUT="${OUTDATED_TIMEOUT:-600}"
# Releasing the runners is a convergence, not an instant. The phase flips as
# soon as the rejection is seen, while the pods it released are still
# terminating and their finalizers are still being processed, so there is a
# window where the phase is Outdated and pods legitimately still exist. Wait
# the window out before treating a live pod as a scale-up.
RELEASE_TIMEOUT="${RELEASE_TIMEOUT:-180}"
RELEASE_INTERVAL="${RELEASE_INTERVAL:-5}"
# How long the parked state is sampled for before it is believed.
STICKY_WINDOW="${STICKY_WINDOW:-60}"
STICKY_INTERVAL="${STICKY_INTERVAL:-5}"
RECOVERY_TIMEOUT="${RECOVERY_TIMEOUT:-300}"
LISTENER_STOP_TIMEOUT="${LISTENER_STOP_TIMEOUT:-120}"
# How long an edit that is not a recovery signal may take to reach the parked
# objects.
PROPAGATION_TIMEOUT="${PROPAGATION_TIMEOUT:-120}"

# minRunners the scale set is installed with, and the value the unrelated-edit
# upgrade moves it to.
INITIAL_MIN_RUNNERS=1
UPGRADED_MIN_RUNNERS=2

RUN_ID=""

function load_outdated_runner_image() {
    # create_cluster preloads the latest runner image only. Preloading the old
    # one keeps the reject cycle off the critical path, but the kubelet can pull
    # it just as well, so a failure here is not a test failure.
    log "Preloading ${OUTDATED_RUNNER_IMAGE} into the cluster"
    if ! docker pull "${OUTDATED_RUNNER_IMAGE}"; then
        log "Failed to pull ${OUTDATED_RUNNER_IMAGE}, leaving it to the kubelet"
        return 0
    fi

    if ! minikube image load "${OUTDATED_RUNNER_IMAGE}"; then
        log "Failed to load ${OUTDATED_RUNNER_IMAGE} into minikube, leaving it to the kubelet"
    fi
}

function install_arc() {
    echo "Installing ARC"
    helm install "${ARC_NAME}" \
        --namespace "${ARC_NAMESPACE}" \
        --create-namespace \
        --set controller.manager.container.image="${IMAGE_NAME}:${IMAGE_TAG}" \
        "${ROOT_DIR}/charts/gha-runner-scale-set-controller-experimental" \
        --debug

    if ! NAME="${ARC_NAME}" NAMESPACE="${ARC_NAMESPACE}" wait_for_arc; then
        NAMESPACE="${ARC_NAMESPACE}" log_arc
        return 1
    fi
}

# Helm forgets any --set that is not repeated on upgrade, so every invocation
# goes through the same base values and only appends what it means to change.
# One argument per line, so the caller can read them back with mapfile.
function scale_set_values() {
    printf '%s\n' \
        "--set" "controllerServiceAccount.name=${ARC_NAME}-gha-rs-controller" \
        "--set" "controllerServiceAccount.namespace=${ARC_NAMESPACE}" \
        "--set" "auth.url=https://github.com/${TARGET_ORG}/${TARGET_REPO}" \
        "--set" "auth.githubToken=${GITHUB_TOKEN}" \
        "--set" "scaleset.name=${SCALE_SET_NAME}"
}

function install_scale_set() {
    echo "Installing scale set ${SCALE_SET_NAMESPACE}/${SCALE_SET_NAME} with runner image ${OUTDATED_RUNNER_IMAGE}"

    local values=()
    mapfile -t values < <(scale_set_values)

    helm install "${SCALE_SET_NAME}" \
        --namespace "${SCALE_SET_NAMESPACE}" \
        --create-namespace \
        "${values[@]}" \
        --set scaleset.minRunners="${INITIAL_MIN_RUNNERS}" \
        --set runner.container.image="${OUTDATED_RUNNER_IMAGE}" \
        "${ROOT_DIR}/charts/gha-runner-scale-set-experimental" \
        --version="${VERSION}" \
        --debug

    if ! NAME="${SCALE_SET_NAME}" NAMESPACE="${ARC_NAMESPACE}" wait_for_scale_set; then
        NAMESPACE="${ARC_NAMESPACE}" log_arc
        return 1
    fi
}

function upgrade_min_runners() {
    echo "Upgrading scale set ${SCALE_SET_NAMESPACE}/${SCALE_SET_NAME} to minRunners=${UPGRADED_MIN_RUNNERS}, leaving the runner spec alone"

    local values=()
    mapfile -t values < <(scale_set_values)

    helm upgrade "${SCALE_SET_NAME}" \
        --namespace "${SCALE_SET_NAMESPACE}" \
        "${values[@]}" \
        --set scaleset.minRunners="${UPGRADED_MIN_RUNNERS}" \
        --set runner.container.image="${OUTDATED_RUNNER_IMAGE}" \
        "${ROOT_DIR}/charts/gha-runner-scale-set-experimental" \
        --version="${VERSION}" \
        --debug
}

function upgrade_runner_image() {
    echo "Upgrading scale set ${SCALE_SET_NAMESPACE}/${SCALE_SET_NAME} to runner image ${RECOVERED_RUNNER_IMAGE}"

    local values=()
    mapfile -t values < <(scale_set_values)

    helm upgrade "${SCALE_SET_NAME}" \
        --namespace "${SCALE_SET_NAMESPACE}" \
        "${values[@]}" \
        --set scaleset.minRunners="${INITIAL_MIN_RUNNERS}" \
        --set runner.container.image="${RECOVERED_RUNNER_IMAGE}" \
        "${ROOT_DIR}/charts/gha-runner-scale-set-experimental" \
        --version="${VERSION}" \
        --debug
}

function trigger_workflow() {
    echo "Queueing a job so the scale set has a reason to create a runner"
    if ! RUN_ID="$(WORKFLOW_FILE="${WORKFLOW_FILE}" SCALE_SET_NAME="${SCALE_SET_NAME}" start_workflow)"; then
        echo "Failed to start workflow"
        return 1
    fi
    echo "Started run ${RUN_ID}"
}

function cancel_workflow() {
    if [[ -z "${RUN_ID}" ]]; then
        return 0
    fi

    # The job is queued against a scale set that spends most of this test
    # refusing to run it, so it is cancelled rather than left hanging.
    echo "Cancelling run ${RUN_ID}"
    gh run cancel "${RUN_ID}" -R "${TARGET_ORG}/${TARGET_REPO}" || true
}

# Everything below prints what it actually observed. These assertions are only
# ever read when they are red.

function autoscaling_runner_set_phase() {
    kubectl get autoscalingrunnerset "${SCALE_SET_NAME}" \
        -n "${SCALE_SET_NAMESPACE}" \
        -o jsonpath='{.status.phase}' 2>/dev/null || true
}

function ephemeral_runner_set_phase() {
    kubectl get ephemeralrunnerset "${RUNNER_SET_NAME}" \
        -n "${SCALE_SET_NAMESPACE}" \
        -o jsonpath='{.status.phase}' 2>/dev/null || true
}

function listener_name() {
    kubectl get autoscalinglisteners -n "${ARC_NAMESPACE}" \
        -l "${LISTENER_SELECTOR}" \
        -o jsonpath='{.items[0].metadata.name}' 2>/dev/null || true
}

function listener_phase() {
    local name="$1"
    kubectl get autoscalinglistener "${name}" \
        -n "${ARC_NAMESPACE}" \
        -o jsonpath='{.spec.phase}' 2>/dev/null || true
}

# Phase and minRunners in a single read, so "took the edit" and "is still
# switched off" are observed on one version of the object rather than across two
# calls that a reconcile could land between.
#
# minRunners is omitempty, so a zero reads back as an empty string rather than
# "0". Callers assert a non-zero value, which is unambiguous; an assertion
# against zero would have to treat empty as zero the way
# assert_runner_set_pinned does for replicas.
function listener_phase_and_min_runners() {
    local name="$1"
    kubectl get autoscalinglistener "${name}" \
        -n "${ARC_NAMESPACE}" \
        -o jsonpath='{.spec.phase}|{.spec.minRunners}' 2>/dev/null || true
}

# Replicas is omitempty, so a pinned 0 comes back as an empty string. PatchID is
# not, so it is always serialized and 0 comes back as "0". Both are read
# together and normalized by the caller.
function ephemeral_runner_set_pinned_state() {
    kubectl get ephemeralrunnerset "${RUNNER_SET_NAME}" \
        -n "${SCALE_SET_NAMESPACE}" \
        -o jsonpath='{.spec.replicas}|{.spec.patchID}' 2>/dev/null || true
}

function listener_pod_names() {
    kubectl get pods -n "${ARC_NAMESPACE}" \
        -l "${LISTENER_SELECTOR}" \
        -o jsonpath='{.items[*].metadata.name}' 2>/dev/null || true
}

function runner_pod_names() {
    kubectl get pods -n "${SCALE_SET_NAMESPACE}" \
        -l "${RUNNER_POD_SELECTOR}" \
        -o jsonpath='{.items[*].metadata.name}' 2>/dev/null || true
}

function dump_state() {
    local reason="$1"
    echo "[!] ${reason}. Current state:"
    kubectl get autoscalingrunnerset,ephemeralrunnerset,ephemeralrunner,pods -n "${SCALE_SET_NAMESPACE}" -o wide || true
    kubectl get autoscalinglisteners,pods -n "${ARC_NAMESPACE}" -o wide || true
    NAMESPACE="${ARC_NAMESPACE}" log_arc || true
}

function assert_scale_set_outdated() {
    echo "[*] Waiting up to ${OUTDATED_TIMEOUT}s for the scale set to report the Outdated phase"

    local deadline=$((SECONDS + OUTDATED_TIMEOUT))
    local ars_phase="" ers_phase=""
    while ((SECONDS < deadline)); do
        ars_phase="$(autoscaling_runner_set_phase)"
        ers_phase="$(ephemeral_runner_set_phase)"

        if [[ "${ars_phase}" == "Outdated" && "${ers_phase}" == "Outdated" ]]; then
            echo "[*] AutoscalingRunnerSet and EphemeralRunnerSet both report Outdated"
            return 0
        fi

        echo "    autoscalingrunnerset=${ars_phase:-<empty>} ephemeralrunnerset=${ers_phase:-<empty>}, waiting"
        sleep 10
    done

    dump_state "Timed out waiting for the Outdated phase, last seen autoscalingrunnerset=${ars_phase:-<empty>} ephemeralrunnerset=${ers_phase:-<empty>}"
    return 1
}

# Every runner the set released has to actually go away. None of them can be
# executing a job here, because a runner that rejected its spec never got one,
# so the whole set is expected to drain to zero.
function assert_runners_released() {
    echo "[*] Waiting up to ${RELEASE_TIMEOUT}s for the released runner pods to go away"

    local deadline=$((SECONDS + RELEASE_TIMEOUT))
    local pods=""
    while ((SECONDS < deadline)); do
        pods="$(runner_pod_names)"
        if [[ -z "${pods}" ]]; then
            echo "[*] All runner pods released"
            return 0
        fi

        echo "    still terminating: ${pods}"
        sleep "${RELEASE_INTERVAL}"
    done

    dump_state "Timed out waiting for runner pods to be released, still present: ${pods}"
    return 1
}

# The Outdated phase has to hold, not just appear. minRunners is 1, so a set
# that trusted the listener's target instead of the rejected spec would scale
# back up inside this window. The runners have already drained by this point,
# so any pod seen here is a fresh one, which is exactly the regression.
function assert_stays_outdated() {
    echo "[*] Sampling the parked state for ${STICKY_WINDOW}s to confirm it holds"

    local deadline=$((SECONDS + STICKY_WINDOW))
    while ((SECONDS < deadline)); do
        local ars_phase ers_phase pods
        ars_phase="$(autoscaling_runner_set_phase)"
        ers_phase="$(ephemeral_runner_set_phase)"
        pods="$(runner_pod_names)"

        if [[ "${ers_phase}" != "Outdated" ]]; then
            dump_state "EphemeralRunnerSet left the Outdated phase, saw '${ers_phase:-<empty>}'"
            return 1
        fi

        if [[ "${ars_phase}" != "Outdated" ]]; then
            dump_state "AutoscalingRunnerSet left the Outdated phase, saw '${ars_phase:-<empty>}'"
            return 1
        fi

        if [[ -n "${pods}" ]]; then
            dump_state "Runner pods reappeared while the scale set is Outdated: ${pods}"
            return 1
        fi

        sleep "${STICKY_INTERVAL}"
    done

    echo "[*] Parked state held for ${STICKY_WINDOW}s with no runner pods"
}

function assert_no_runner_pods() {
    echo "[*] Asserting no runner pod exists"

    local pods
    pods="$(runner_pod_names)"
    if [[ -n "${pods}" ]]; then
        dump_state "Expected no runner pods, found: ${pods}"
        return 1
    fi

    echo "[*] No runner pods, as expected"
}

# The listener is switched off, not deleted: the object stays as the record of a
# scale set that is meant to come back, and only the pod goes away.
function assert_listener_stopped() {
    echo "[*] Asserting the listener object survives with .spec.phase=Stopped and no pod"

    local deadline=$((SECONDS + LISTENER_STOP_TIMEOUT))
    local name="" phase="" pods=""
    while ((SECONDS < deadline)); do
        name="$(listener_name)"
        if [[ -z "${name}" ]]; then
            dump_state "AutoscalingListener object is gone, but a parked scale set must keep it"
            return 1
        fi

        phase="$(listener_phase "${name}")"
        pods="$(listener_pod_names)"

        if [[ "${phase}" == "Stopped" && -z "${pods}" ]]; then
            echo "[*] Listener ${name} is Stopped and its pod is gone"
            return 0
        fi

        echo "    listener=${name} phase=${phase:-<empty>} pods=${pods:-<none>}, waiting"
        sleep 5
    done

    dump_state "Listener did not reach the stopped state, last seen listener=${name:-<none>} phase=${phase:-<empty>} pods=${pods:-<none>}"
    return 1
}

# A parked EphemeralRunnerSet is held at zero. This is what stops it scaling
# back up while it carries edits that are not a recovery signal.
function assert_runner_set_pinned() {
    echo "[*] Asserting the EphemeralRunnerSet is pinned at Replicas=0, PatchID=0"

    local state replicas patch_id
    state="$(ephemeral_runner_set_pinned_state)"
    replicas="${state%%|*}"
    patch_id="${state##*|}"

    if [[ "${replicas:-0}" != "0" || "${patch_id:-0}" != "0" ]]; then
        dump_state "EphemeralRunnerSet is not pinned, saw replicas='${replicas:-<empty>}' patchID='${patch_id:-<empty>}'"
        return 1
    fi

    echo "[*] EphemeralRunnerSet is pinned (replicas='${replicas:-<empty, means 0>}' patchID='${patch_id}')"
}

# Switched off is not frozen. An edit that is not a recovery signal still has to
# land on the parked objects, and it has to land without switching anything back
# on: the listener takes the new minRunners while its phase stays Stopped, and
# the EphemeralRunnerSet takes it while staying pinned at zero.
function assert_parked_objects_updated() {
    local want_min_runners="$1"

    echo "[*] Waiting up to ${PROPAGATION_TIMEOUT}s for minRunners=${want_min_runners} to reach the parked objects"

    local deadline=$((SECONDS + PROPAGATION_TIMEOUT))
    local name="" state="" phase="" min_runners=""
    while ((SECONDS < deadline)); do
        name="$(listener_name)"
        if [[ -z "${name}" ]]; then
            dump_state "AutoscalingListener object is gone, but a parked scale set must keep it"
            return 1
        fi

        state="$(listener_phase_and_min_runners "${name}")"
        phase="${state%%|*}"
        min_runners="${state##*|}"

        # Leaving Stopped is a failure at any point, not something to wait out:
        # the edit must never be what starts the listener again.
        if [[ "${phase}" != "Stopped" ]]; then
            dump_state "Listener ${name} left the stopped phase while the scale set is parked, saw '${phase:-<empty>}' with minRunners='${min_runners:-<empty>}'"
            return 1
        fi

        if [[ "${min_runners}" == "${want_min_runners}" ]]; then
            echo "[*] Listener ${name} is Stopped and carries .spec.minRunners=${min_runners}"

            assert_runner_set_pinned || return 1
            assert_no_runner_pods || return 1

            return 0
        fi

        echo "    listener=${name} phase=${phase} minRunners=${min_runners:-<empty>}, waiting for ${want_min_runners}"
        sleep 5
    done

    dump_state "Timed out waiting for the parked listener to take minRunners=${want_min_runners}, last seen listener=${name:-<none>} phase=${phase:-<empty>} minRunners=${min_runners:-<empty>}"
    return 1
}

# minRunners is not part of the runner spec, so it must not un-park the scale
# set no matter how much it bumps the generation. It must still reach the parked
# objects, though, which is what assert_parked_objects_updated covers.
function assert_sticky_to_unrelated_change() {
    echo "[*] Asserting an edit outside the runner spec lands without recovering the scale set"

    upgrade_min_runners || return 1

    assert_parked_objects_updated "${UPGRADED_MIN_RUNNERS}" || return 1
    assert_stays_outdated || return 1
    assert_listener_stopped || return 1

    echo "[*] Scale set stayed Outdated across a minRunners change and took the edit anyway"
}

function assert_recovered() {
    echo "[*] Waiting up to ${RECOVERY_TIMEOUT}s for the scale set to recover"

    local deadline=$((SECONDS + RECOVERY_TIMEOUT))
    local ars_phase="" name="" phase="" listener_pods="" runner_pods=""
    while ((SECONDS < deadline)); do
        ars_phase="$(autoscaling_runner_set_phase)"
        name="$(listener_name)"
        phase=""
        if [[ -n "${name}" ]]; then
            phase="$(listener_phase "${name}")"
        fi
        listener_pods="$(listener_pod_names)"
        runner_pods="$(runner_pod_names)"

        # The listener is re-created with the phase unset when its spec drifted
        # while parked, so anything other than Stopped counts as running.
        if [[ "${ars_phase}" != "Outdated" && "${phase}" != "Stopped" && -n "${listener_pods}" && -n "${runner_pods}" ]]; then
            echo "[*] Recovered: autoscalingrunnerset=${ars_phase} listener=${name} phase=${phase:-<empty>} listener pods=${listener_pods} runner pods=${runner_pods}"
            return 0
        fi

        echo "    autoscalingrunnerset=${ars_phase:-<empty>} listener=${name:-<none>} phase=${phase:-<empty>} listener pods=${listener_pods:-<none>} runner pods=${runner_pods:-<none>}, waiting"
        sleep 10
    done

    dump_state "Timed out waiting for recovery, last seen autoscalingrunnerset=${ars_phase:-<empty>} listener=${name:-<none>} phase=${phase:-<empty>} listener pods=${listener_pods:-<none>} runner pods=${runner_pods:-<none>}"
    return 1
}

function main() {
    local failed=()

    build_image
    create_cluster
    load_outdated_runner_image

    install_arc
    install_scale_set

    trigger_workflow || failed+=("trigger_workflow")

    if assert_scale_set_outdated; then
        assert_runners_released || failed+=("assert_runners_released")
        assert_stays_outdated || failed+=("assert_stays_outdated")
        assert_no_runner_pods || failed+=("assert_no_runner_pods")
        assert_runner_set_pinned || failed+=("assert_runner_set_pinned")
        assert_listener_stopped || failed+=("assert_listener_stopped")
        assert_sticky_to_unrelated_change || failed+=("assert_sticky_to_unrelated_change")

        upgrade_runner_image || failed+=("upgrade_runner_image")
        assert_recovered || failed+=("assert_recovered")
    else
        failed+=("assert_scale_set_outdated")
    fi

    cancel_workflow

    INSTALLATION_NAME="${SCALE_SET_NAME}" NAMESPACE="${SCALE_SET_NAMESPACE}" cleanup_scale_set || failed+=("cleanup_scale_set")

    NAMESPACE="${ARC_NAMESPACE}" log_arc || failed+=("log_arc")

    delete_cluster

    print_results "${failed[@]}"
}

main
