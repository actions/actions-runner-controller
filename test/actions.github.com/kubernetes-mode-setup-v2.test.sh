#!/bin/bash

set -euo pipefail

DIR="$(realpath "$(dirname "${BASH_SOURCE[0]}")")"

ROOT_DIR="$(realpath "${DIR}/../..")"

source "${DIR}/helper.sh"

export VERSION="$(chart_version "${ROOT_DIR}/charts/gha-runner-scale-set-controller-experimental/Chart.yaml")"

SCALE_SET_NAME="kubernetes-mode-$(date +'%M%S')$(((RANDOM + 100) % 100 + 1))"
SCALE_SET_NAMESPACE="arc-runners"
WORKFLOW_FILE="arc-test-kubernetes-workflow.yaml"
ARC_NAME="arc"
ARC_NAMESPACE="arc-systems"

function install_arc() {
    install_openebs || {
        echo "OpenEBS installation failed"
        return 1
    }

    echo "Creating namespace ${ARC_NAMESPACE}"
    kubectl create namespace "${SCALE_SET_NAMESPACE}"

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
    echo "Installing scale set ${SCALE_SET_NAMESPACE}/${SCALE_SET_NAME}"
    helm install "${SCALE_SET_NAME}" \
        --namespace "${SCALE_SET_NAMESPACE}" \
        --create-namespace \
        --set controllerServiceAccount.name="${ARC_NAME}-gha-rs-controller" \
        --set controllerServiceAccount.namespace="${ARC_NAMESPACE}" \
        --set auth.url="https://github.com/${TARGET_ORG}/${TARGET_REPO}" \
        --set auth.githubToken="${GITHUB_TOKEN}" \
        --set runner.mode="kubernetes" \
        --set runner.kubernetesMode.workVolumeClaim.accessModes={"ReadWriteOnce"} \
        --set runner.kubernetesMode.workVolumeClaim.storageClassName="openebs-hostpath" \
        --set runner.kubernetesMode.workVolumeClaim.resources.requests.storage="1Gi" \
        "${ROOT_DIR}/charts/gha-runner-scale-set-experimental"

    if ! NAME="${SCALE_SET_NAME}" NAMESPACE="${ARC_NAMESPACE}" wait_for_scale_set; then
        NAMESPACE="${ARC_NAMESPACE}" log_arc
        return 1
    fi
}

function main() {
    local failed=()

    build_image
    create_cluster

    install_arc
    install_scale_set

    WORKFLOW_FILE="${WORKFLOW_FILE}" SCALE_SET_NAME="${SCALE_SET_NAME}" run_workflow || failed+=("run_workflow")

    INSTALLATION_NAME="${SCALE_SET_NAME}" NAMESPACE="${SCALE_SET_NAMESPACE}" cleanup_scale_set || failed+=("cleanup_scale_set")

    NAMESPACE="${ARC_NAMESPACE}" log_arc || failed+=("log_arc")

    delete_cluster

    print_results "${failed[@]}"
}

main
