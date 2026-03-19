#!/bin/bash

set -euo pipefail

DIR="$(realpath "$(dirname "${BASH_SOURCE[0]}")")"

ROOT_DIR="$(realpath "${DIR}/../..")"

source "${DIR}/helper.sh"

VERSION="$(chart_version "${ROOT_DIR}/charts/gha-runner-scale-set-controller-experimental/Chart.yaml")" || exit 1
export VERSION

SCALE_SET_NAME="custom-label-$(date +'%M%S')$(((RANDOM + 100) % 100 + 1))"
SCALE_SET_NAMESPACE="arc-runners"
SCALE_SET_LABEL="custom-$(date +'%s')${RANDOM}"
WORKFLOW_FILE="arc-custom-label.yaml"
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
    echo "Installing scale set ${SCALE_SET_NAMESPACE}/${SCALE_SET_NAME} with label ${SCALE_SET_LABEL}"
    helm install "${SCALE_SET_NAME}" \
        --namespace "${SCALE_SET_NAMESPACE}" \
        --create-namespace \
        --set controllerServiceAccount.name="${ARC_NAME}-gha-rs-controller" \
        --set controllerServiceAccount.namespace="${ARC_NAMESPACE}" \
        --set auth.url="https://github.com/${TARGET_ORG}/${TARGET_REPO}" \
        --set auth.githubToken="${GITHUB_TOKEN}" \
        --set scaleset.labels[0]="${SCALE_SET_LABEL}" \
        "${ROOT_DIR}/charts/gha-runner-scale-set-experimental" \
        --version="${VERSION}"

    if ! NAME="${SCALE_SET_NAME}" NAMESPACE="${ARC_NAMESPACE}" wait_for_scale_set; then
        NAMESPACE="${ARC_NAMESPACE}" log_arc
        return 1
    fi
}

function verify_scale_set_label() {
    local actual_label
    actual_label="$(kubectl get autoscalingrunnersets.actions.github.com -n "${SCALE_SET_NAMESPACE}" -l app.kubernetes.io/instance="${SCALE_SET_NAME}" -o jsonpath='{.items[0].spec.runnerScaleSetLabels[0]}')"
    if [[ "${actual_label}" != "${SCALE_SET_LABEL}" ]]; then
        echo "Expected scale set label '${SCALE_SET_LABEL}', got '${actual_label}'" >&2
        return 1
    fi
}

function run_custom_label_workflow() {
    local repo="${TARGET_ORG}/${TARGET_REPO}"
    local queue_time
    queue_time="$(date -u +%FT%TZ)"

    gh workflow run -R "${repo}" "${WORKFLOW_FILE}" \
        -f scaleset-label="${SCALE_SET_LABEL}" || return 1

    local count=0
    local run_id=
    while true; do
        if [[ "${count}" -ge 12 ]]; then
            echo "Timeout waiting for custom label workflow to start" >&2
            return 1
        fi

        run_id="$(gh run list -R "${repo}" --workflow "${WORKFLOW_FILE}" --created ">${queue_time}" --json databaseId --jq '.[0].databaseId' | head -n1)"
        if [[ -n "${run_id}" ]]; then
            break
        fi

        sleep 5
        count=$((count + 1))
    done

    gh run watch "${run_id}" -R "${repo}" --exit-status
}

function main() {
    local failed=()

    build_image
    create_cluster

    install_arc
    install_scale_set
    verify_scale_set_label || failed+=("verify_scale_set_label")

    run_custom_label_workflow || failed+=("run_custom_label_workflow")

    INSTALLATION_NAME="${SCALE_SET_NAME}" NAMESPACE="${SCALE_SET_NAMESPACE}" cleanup_scale_set || failed+=("cleanup_scale_set")

    NAMESPACE="${ARC_NAMESPACE}" log_arc || failed+=("log_arc")

    delete_cluster

    print_results "${failed[@]}"
}

main
