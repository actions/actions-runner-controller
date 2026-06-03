#!/bin/bash

set -euo pipefail

DIR="$(realpath "$(dirname "${BASH_SOURCE[0]}")")"

ROOT_DIR="$(realpath "${DIR}/../..")"

source "${DIR}/helper.sh" || {
    echo "Failed to source helper.sh"
    exit 1
}

export VERSION="$(chart_version "${ROOT_DIR}/charts/gha-runner-scale-set-controller-experimental/Chart.yaml")"

SCALE_SET_NAME="update-strategy-$(date '+%M%S')$(((RANDOM + 100) % 100 + 1))"
SCALE_SET_NAMESPACE="arc-runners"
WORKFLOW_FILE="arc-test-sleepy-matrix.yaml"
ARC_NAME="arc"
ARC_NAMESPACE="arc-systems"

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

function install_scale_set() {
    echo "Installing scale set ${SCALE_SET_NAME}/${SCALE_SET_NAMESPACE}"
    helm install "${SCALE_SET_NAME}" \
        --namespace "${SCALE_SET_NAMESPACE}" \
        --create-namespace \
        --set controllerServiceAccount.name="${ARC_NAME}-gha-rs-controller" \
        --set controllerServiceAccount.namespace="${ARC_NAMESPACE}" \
        --set auth.url="https://github.com/${TARGET_ORG}/${TARGET_REPO}" \
        --set auth.githubToken="${GITHUB_TOKEN}" \
        "${ROOT_DIR}/charts/gha-runner-scale-set-experimental" \
        --version="${VERSION}" \
        --debug

    if ! NAME="${SCALE_SET_NAME}" NAMESPACE="${ARC_NAMESPACE}" wait_for_scale_set; then
        NAMESPACE="${ARC_NAMESPACE}" log_arc
        return 1
    fi
}

function upgrade_scale_set() {
    echo "Upgrading scale set ${SCALE_SET_NAME}/${SCALE_SET_NAMESPACE}"
    
    UPGRADE_MARKER="e2e-upgrade-${SCALE_SET_NAME}-$(date +%s)"
    echo "Generated upgrade marker: ${UPGRADE_MARKER}"
    
    PATCH_APPLIED_TIME=$(date -u +"%Y-%m-%dT%H:%M:%SZ")
    echo "Captured PATCH_APPLIED_TIME: ${PATCH_APPLIED_TIME}"
    
    helm upgrade "${SCALE_SET_NAME}" \
        --namespace "${SCALE_SET_NAMESPACE}" \
        --set controllerServiceAccount.name="${ARC_NAME}-gha-rs-controller" \
        --set controllerServiceAccount.namespace="${ARC_NAMESPACE}" \
        --set auth.url="https://github.com/${TARGET_ORG}/${TARGET_REPO}" \
        --set auth.githubToken="${GITHUB_TOKEN}" \
        --set runner.container.image="ghcr.io/actions/actions-runner:latest" \
        --set runner.container.command={"/home/runner/run.sh"} \
        --set runner.env[0].name="TEST" \
        --set runner.env[0].value="E2E TESTS" \
        --set runner.pod.metadata.labels.e2e\.arc\/upgrade-marker="${UPGRADE_MARKER}" \
        "${ROOT_DIR}/charts/gha-runner-scale-set-experimental" \
        --version="${VERSION}" \
        --debug

}

function capture_pre_upgrade_state() {
    echo "Capturing pre-upgrade state for scale set ${SCALE_SET_NAME}"
    
    # Capture listener pod UID
    local listener_json
    listener_json=$(kubectl get pods -n "${ARC_NAMESPACE}" \
        -l "actions.github.com/scale-set-name=${SCALE_SET_NAME}" \
        --field-selector=status.phase=Running \
        -o json) || {
        echo "ERROR: kubectl failed to fetch listener pods"
        echo "Namespace: ${ARC_NAMESPACE}"
        echo "Selector: actions.github.com/scale-set-name=${SCALE_SET_NAME}"
        return 1
    }
    
    local listener_count
    listener_count=$(echo "${listener_json}" | jq '.items | length')
    
    if [ "${listener_count}" -ne 1 ]; then
        echo "ERROR: Expected exactly 1 running listener pod, found ${listener_count}"
        echo "Selector: actions.github.com/scale-set-name=${SCALE_SET_NAME}"
        echo "Namespace: ${ARC_NAMESPACE}"
        echo "Observed pods: ${listener_json}"
        return 1
    fi
    
    PRE_UPGRADE_LISTENER_UID=$(echo "${listener_json}" | jq -r '.items[0].metadata.uid')
    
    # Capture EphemeralRunnerSet UID
    local ers_json
    ers_json=$(kubectl get autoscalingrunnersets -n "${SCALE_SET_NAMESPACE}" \
        -l "app.kubernetes.io/instance=${SCALE_SET_NAME}" \
        -o json) || {
        echo "ERROR: kubectl failed to fetch EphemeralRunnerSet"
        echo "Namespace: ${SCALE_SET_NAMESPACE}"
        echo "Selector: app.kubernetes.io/instance=${SCALE_SET_NAME}"
        return 1
    }
    
    local ers_count
    ers_count=$(echo "${ers_json}" | jq '.items | length')
    
    if [ "${ers_count}" -ne 1 ]; then
        echo "ERROR: Expected exactly 1 EphemeralRunnerSet, found ${ers_count}"
        echo "Selector: app.kubernetes.io/instance=${SCALE_SET_NAME}"
        echo "Namespace: ${SCALE_SET_NAMESPACE}"
        echo "Observed resources: ${ers_json}"
        return 1
    fi
    
    PRE_UPGRADE_ERS_UID=$(echo "${ers_json}" | jq -r '.items[0].metadata.uid')
    
    # Capture timestamp
    PRE_UPGRADE_TIME=$(date -u +"%Y-%m-%dT%H:%M:%SZ")
    
    echo "PRE_UPGRADE_LISTENER_UID=${PRE_UPGRADE_LISTENER_UID}"
    echo "PRE_UPGRADE_ERS_UID=${PRE_UPGRADE_ERS_UID}"
    echo "PRE_UPGRADE_TIME=${PRE_UPGRADE_TIME}"
    
    return 0
}

function assert_listener_stays_up() {
    echo "Asserting listener remains continuously running with unchanged UID"
    
    if [ -z "${PRE_UPGRADE_LISTENER_UID:-}" ]; then
        echo "ERROR: PRE_UPGRADE_LISTENER_UID not set. Call capture_pre_upgrade_state first."
        return 1
    fi
    
    local count=0
    local max_iterations=120
    local sleep_interval=1
    
    echo "Starting continuous verification for ${max_iterations}s (interval: ${sleep_interval}s)"
    echo "Expected listener UID: ${PRE_UPGRADE_LISTENER_UID}"
    echo "Selector: actions.github.com/scale-set-name=${SCALE_SET_NAME}"
    echo "Namespace: ${ARC_NAMESPACE}"
    
    while [ "${count}" -lt "${max_iterations}" ]; do
        # Fetch current listener pods
        local listener_json
        listener_json=$(kubectl get pods -n "${ARC_NAMESPACE}" \
            -l "actions.github.com/scale-set-name=${SCALE_SET_NAME}" \
            --field-selector=status.phase=Running \
            -o json) || {
            echo "ERROR: kubectl failed to fetch listener pods during verification"
            echo "Namespace: ${ARC_NAMESPACE}"
            echo "Selector: actions.github.com/scale-set-name=${SCALE_SET_NAME}"
            echo "Time elapsed: ${count}s"
            return 1
        }
        
        local listener_count
        listener_count=$(echo "${listener_json}" | jq '.items | length')
        
        # Check listener count is exactly 1
        if [ "${listener_count}" -ne 1 ]; then
            echo "FAIL: Expected exactly 1 running listener, found ${listener_count}"
            echo "Time elapsed: ${count}s"
            echo "Expected UID: ${PRE_UPGRADE_LISTENER_UID}"
            echo "Selector: actions.github.com/scale-set-name=${SCALE_SET_NAME}"
            echo "Namespace: ${ARC_NAMESPACE}"
            echo ""
            echo "Listener pods detail:"
            kubectl get pods -n "${ARC_NAMESPACE}" \
                -l "actions.github.com/scale-set-name=${SCALE_SET_NAME}" \
                -o json | jq '.items[] | {name: .metadata.name, uid: .metadata.uid, phase: .status.phase, creationTimestamp: .metadata.creationTimestamp}' || echo "WARNING: kubectl diagnostic command failed"
            echo ""
            echo "All pods in all namespaces:"
            kubectl get pods -A || echo "WARNING: kubectl diagnostic command failed"
            return 1
        fi
        
        # Extract current listener UID
        local current_uid
        current_uid=$(echo "${listener_json}" | jq -r '.items[0].metadata.uid')
        
        # Check UID has not changed
        if [ "${current_uid}" != "${PRE_UPGRADE_LISTENER_UID}" ]; then
            echo "FAIL: Listener UID changed from ${PRE_UPGRADE_LISTENER_UID} to ${current_uid}"
            echo "Time elapsed: ${count}s"
            echo "Expected UID: ${PRE_UPGRADE_LISTENER_UID}"
            echo "Observed UID: ${current_uid}"
            echo "Selector: actions.github.com/scale-set-name=${SCALE_SET_NAME}"
            echo "Namespace: ${ARC_NAMESPACE}"
            echo ""
            echo "Current listener pod detail:"
            kubectl get pods -n "${ARC_NAMESPACE}" \
                -l "actions.github.com/scale-set-name=${SCALE_SET_NAME}" \
                -o json | jq '.items[0] | {name: .metadata.name, uid: .metadata.uid, phase: .status.phase, creationTimestamp: .metadata.creationTimestamp}' || echo "WARNING: kubectl diagnostic command failed"
            echo ""
            echo "All pods in all namespaces:"
            kubectl get pods -A || echo "WARNING: kubectl diagnostic command failed"
            return 1
        fi
        
        # Progress indicator every 10 seconds
        if [ $((count % 10)) -eq 0 ]; then
            echo "Verification ongoing: ${count}s elapsed | Listener count: ${listener_count} | UID: ${current_uid}"
        fi
        
        sleep "${sleep_interval}"
        count=$((count + sleep_interval))
    done
    
    echo "SUCCESS: Listener remained continuously running for ${max_iterations}s with unchanged UID"
    echo "Final listener UID: ${PRE_UPGRADE_LISTENER_UID}"
    echo "Selector: actions.github.com/scale-set-name=${SCALE_SET_NAME}"
    echo "Namespace: ${ARC_NAMESPACE}"
    echo ""
    echo "Final listener pod detail:"
    kubectl get pods -n "${ARC_NAMESPACE}" \
        -l "actions.github.com/scale-set-name=${SCALE_SET_NAME}" \
        -o json | jq '.items[0] | {name: .metadata.name, uid: .metadata.uid, phase: .status.phase, creationTimestamp: .metadata.creationTimestamp}' || echo "WARNING: kubectl diagnostic command failed"
    return 0
}

function assert_marked_runner_spawned_after_upgrade() {
    echo "Asserting at least one runner pod with upgrade marker spawned after patch application"
    
    if [ -z "${UPGRADE_MARKER:-}" ]; then
        echo "ERROR: UPGRADE_MARKER not set. Call upgrade_scale_set first."
        return 1
    fi
    
    if [ -z "${PATCH_APPLIED_TIME:-}" ]; then
        echo "ERROR: PATCH_APPLIED_TIME not set. Call upgrade_scale_set first."
        return 1
    fi
    
    local count=0
    local max_iterations=120
    local sleep_interval=1
    
    echo "Starting verification for ${max_iterations}s (interval: ${sleep_interval}s)"
    echo "Expected marker: ${UPGRADE_MARKER}"
    echo "Expected creationTimestamp > ${PATCH_APPLIED_TIME}"
    echo "Selector: app.kubernetes.io/component=runner,e2e.arc/upgrade-marker=${UPGRADE_MARKER}"
    echo "Namespace: ${SCALE_SET_NAMESPACE}"
    
    while [ "${count}" -lt "${max_iterations}" ]; do
        # Fetch runner pods with marker label
        local runner_json
        runner_json=$(kubectl get pods -n "${SCALE_SET_NAMESPACE}" \
            -l "app.kubernetes.io/component=runner,e2e.arc/upgrade-marker=${UPGRADE_MARKER}" \
            -o json) || {
            echo "ERROR: kubectl failed to fetch runner pods during verification"
            echo "Namespace: ${SCALE_SET_NAMESPACE}"
            echo "Selector: app.kubernetes.io/component=runner,e2e.arc/upgrade-marker=${UPGRADE_MARKER}"
            echo "Time elapsed: ${count}s"
            return 1
        }
        
        local runner_count
        runner_count=$(echo "${runner_json}" | jq '.items | length')
        
        if [ "${runner_count}" -eq 0 ]; then
            if [ $((count % 10)) -eq 0 ]; then
                echo "No runners with marker found yet. Elapsed: ${count}s"
            fi
            sleep "${sleep_interval}"
            count=$((count + sleep_interval))
            continue
        fi
        
        # Filter pods created after PATCH_APPLIED_TIME
        local matching_runners
        matching_runners=$(echo "${runner_json}" | jq -r --arg patch_time "${PATCH_APPLIED_TIME}" \
            '.items[] | select(.metadata.creationTimestamp > $patch_time) | .metadata.name')
        
        local matching_count
        matching_count=$(echo "${matching_runners}" | grep -c . || true)
        
        if [ "${matching_count}" -gt 0 ]; then
            # Check if at least one is Running
            local running_count
            running_count=$(echo "${runner_json}" | jq -r --arg patch_time "${PATCH_APPLIED_TIME}" \
                '[.items[] | select(.metadata.creationTimestamp > $patch_time and .status.phase == "Running")] | length')
            
            if [ "${running_count}" -gt 0 ]; then
                echo "SUCCESS: Found ${running_count} running runner(s) with marker spawned after upgrade"
                echo "Marker: ${UPGRADE_MARKER}"
                echo "Patch applied time: ${PATCH_APPLIED_TIME}"
                echo "Matching pods:"
                echo "${matching_runners}"
                echo ""
                echo "Detailed pod information:"
                kubectl get pods -n "${SCALE_SET_NAMESPACE}" \
                    -l "app.kubernetes.io/component=runner,e2e.arc/upgrade-marker=${UPGRADE_MARKER}" \
                    -o json | jq '.items[] | select(.metadata.creationTimestamp > "'${PATCH_APPLIED_TIME}'") | {name: .metadata.name, uid: .metadata.uid, phase: .status.phase, creationTimestamp: .metadata.creationTimestamp, "upgrade-marker": .metadata.labels."e2e.arc/upgrade-marker"}' || echo "WARNING: kubectl diagnostic command failed"
                return 0
            else
                echo "Found ${matching_count} matching pod(s), but none are Running yet. Elapsed: ${count}s"
            fi
        fi
        
        # Progress indicator every 10 seconds
        if [ $((count % 10)) -eq 0 ]; then
            echo "Verification ongoing: ${count}s elapsed | Runners with marker: ${runner_count} | Matching time: ${matching_count}"
        fi
        
        sleep "${sleep_interval}"
        count=$((count + sleep_interval))
    done
    
    # Timeout - dump diagnostics
    echo "FAIL: No running runner pod with marker spawned after upgrade within ${max_iterations}s"
    echo "Expected marker: ${UPGRADE_MARKER}"
    echo "Expected creationTimestamp > ${PATCH_APPLIED_TIME}"
    echo "Selector: app.kubernetes.io/component=runner,e2e.arc/upgrade-marker=${UPGRADE_MARKER}"
    echo "Namespace: ${SCALE_SET_NAMESPACE}"
    echo ""
    echo "Candidate pods with marker (regardless of timestamp):"
    kubectl get pods -n "${SCALE_SET_NAMESPACE}" \
        -l "app.kubernetes.io/component=runner,e2e.arc/upgrade-marker=${UPGRADE_MARKER}" \
        -o json | jq -r '.items[] | "Pod: \(.metadata.name) | UID: \(.metadata.uid) | Created: \(.metadata.creationTimestamp) | Phase: \(.status.phase) | Marker: \(.metadata.labels."e2e.arc/upgrade-marker")"' || echo "WARNING: kubectl diagnostic command failed"
    
    echo ""
    echo "All runner pods in namespace (for timestamp/label analysis):"
    kubectl get pods -n "${SCALE_SET_NAMESPACE}" -l "app.kubernetes.io/component=runner" \
        -o json | jq '.items[] | {name: .metadata.name, uid: .metadata.uid, phase: .status.phase, creationTimestamp: .metadata.creationTimestamp, "upgrade-marker": .metadata.labels."e2e.arc/upgrade-marker"}' || echo "WARNING: kubectl diagnostic command failed"
    
    return 1
}
 
function assert_ephemeral_runner_set_stays_up() {
    echo "Asserting EphemeralRunnerSet remains unchanged and stays up"
    
    if [ -z "${PRE_UPGRADE_ERS_UID:-}" ]; then
        echo "ERROR: PRE_UPGRADE_ERS_UID not set. Call capture_pre_upgrade_state first."
        return 1
    fi
    
    # Get current ERS
    local ers_json
    ers_json=$(kubectl get autoscalingrunnersets -n "${SCALE_SET_NAMESPACE}" \
        -l "app.kubernetes.io/instance=${SCALE_SET_NAME}" \
        -o json) || {
        echo "ERROR: kubectl failed to fetch EphemeralRunnerSet"
        echo "Namespace: ${SCALE_SET_NAMESPACE}"
        echo "Selector: app.kubernetes.io/instance=${SCALE_SET_NAME}"
        return 1
    }
    
    local ers_count
    ers_count=$(echo "${ers_json}" | jq '.items | length')
    
    # Validate exactly one ERS exists
    if [ "${ers_count}" -ne 1 ]; then
        echo "FAIL: Expected exactly 1 EphemeralRunnerSet, found ${ers_count}"
        echo "Expected UID: ${PRE_UPGRADE_ERS_UID}"
        echo "Selector: app.kubernetes.io/instance=${SCALE_SET_NAME}"
        echo "Namespace: ${SCALE_SET_NAMESPACE}"
        echo ""
        echo "Observed ERS resources:"
        kubectl get autoscalingrunnersets -n "${SCALE_SET_NAMESPACE}" \
            -l "app.kubernetes.io/instance=${SCALE_SET_NAME}" \
            -o json | jq '.items[] | {name: .metadata.name, uid: .metadata.uid, generation: .metadata.generation, observedGeneration: .status.observedGeneration, creationTimestamp: .metadata.creationTimestamp}' || echo "WARNING: kubectl diagnostic command failed"
        return 1
    fi
    
    # Extract current ERS UID
    local current_ers_uid
    current_ers_uid=$(echo "${ers_json}" | jq -r '.items[0].metadata.uid')
    
    # Validate UID has not changed
    if [ "${current_ers_uid}" != "${PRE_UPGRADE_ERS_UID}" ]; then
        echo "FAIL: ERS UID changed from ${PRE_UPGRADE_ERS_UID} to ${current_ers_uid}"
        echo "ERS was recreated during upgrade, indicating pod replacement instead of true update"
        echo "Expected UID: ${PRE_UPGRADE_ERS_UID}"
        echo "Observed UID: ${current_ers_uid}"
        echo "Selector: app.kubernetes.io/instance=${SCALE_SET_NAME}"
        echo "Namespace: ${SCALE_SET_NAMESPACE}"
        echo ""
        echo "Full ERS metadata:"
        kubectl get autoscalingrunnersets -n "${SCALE_SET_NAMESPACE}" \
            -l "app.kubernetes.io/instance=${SCALE_SET_NAME}" \
            -o json | jq '.items[0].metadata' || echo "WARNING: kubectl diagnostic command failed"
        echo ""
        echo "ERS status:"
        kubectl get autoscalingrunnersets -n "${SCALE_SET_NAMESPACE}" \
            -l "app.kubernetes.io/instance=${SCALE_SET_NAME}" \
            -o json | jq '.items[0].status' || echo "WARNING: kubectl diagnostic command failed"
        return 1
    fi
    
    # Optionally verify status.currentReplicas is queryable
    local current_replicas
    current_replicas=$(echo "${ers_json}" | jq -r '.items[0].status.currentReplicas // "N/A"')
    local generation
    generation=$(echo "${ers_json}" | jq -r '.items[0].metadata.generation // "N/A"')
    local observedGeneration
    observedGeneration=$(echo "${ers_json}" | jq -r '.items[0].status.observedGeneration // "N/A"')
    
    echo "SUCCESS: EphemeralRunnerSet remains unchanged"
    echo "ERS UID: ${current_ers_uid}"
    echo "Expected UID: ${PRE_UPGRADE_ERS_UID}"
    echo "Generation: ${generation} | Observed Generation: ${observedGeneration}"
    echo "Current Replicas: ${current_replicas}"
    echo "Selector: app.kubernetes.io/instance=${SCALE_SET_NAME}"
    echo "Namespace: ${SCALE_SET_NAMESPACE}"
    echo ""
    echo "ERS detail:"
    kubectl get autoscalingrunnersets -n "${SCALE_SET_NAMESPACE}" \
        -l "app.kubernetes.io/instance=${SCALE_SET_NAME}" \
        -o json | jq '.items[0] | {name: .metadata.name, uid: .metadata.uid, generation: .metadata.generation, creationTimestamp: .metadata.creationTimestamp, observedGeneration: .status.observedGeneration, currentReplicas: .status.currentReplicas}' || echo "WARNING: kubectl diagnostic command failed"
    return 0
}
 
 function main() {
    local failed=()

    build_image
    create_cluster
    install_arc
    install_scale_set

     WORKFLOW_FILE="${WORKFLOW_FILE}" SCALE_SET_NAME="${SCALE_SET_NAME}" run_workflow || failed+=("run_workflow")

     capture_pre_upgrade_state || failed+=("capture_pre_upgrade_state")
     upgrade_scale_set || failed+=("upgrade_scale_set")
     assert_listener_stays_up || failed+=("assert_listener_stays_up")
     assert_ephemeral_runner_set_stays_up || failed+=("assert_ephemeral_runner_set_stays_up")
     WORKFLOW_FILE="${WORKFLOW_FILE}" SCALE_SET_NAME="${SCALE_SET_NAME}" run_workflow || failed+=("run_workflow")
     assert_marked_runner_spawned_after_upgrade || failed+=("assert_marked_runner_spawned_after_upgrade")

    INSTALLATION_NAME="${SCALE_SET_NAME}" NAMESPACE="${SCALE_SET_NAMESPACE}" cleanup_scale_set || failed+=("cleanup_scale_set")

    NAMESPACE="${ARC_NAMESPACE}" log_arc || failed+=("log_arc")

    delete_cluster

    print_results "${failed[@]}"
}

main
