#!/bin/bash

set -e

DIR="$(dirname "${BASH_SOURCE[0]}")"

DIR="$(realpath "${DIR}")"

ROOT_DIR="$(ralpath "${DIR}/../../")"

source "${DIR}/helper.sh"

SCALE_SET_NAME="update-strategy-$(date '+%M%S')$((($RANDOM + 100) % 100 + 1))"
SCALE_SET_NAMESPACE="arc-runners"
WORKFLOW_FILE="arc-test-sleepy-matrix.yaml"
ARC_NAME="arc"
ARC_NAMESPACE="arc-systems"

function install_arc() {
    echo "Installing ARC"

    helm install "${ARC_NAME}" \
        --namespace "${ARC_NAMESPACE}" \
        --create-namespace \
        --set image.repository="${IMAGE_NAME}" \
        --set image.tag="${IMAGE_TAG}" \
        --set flags.updateStrategy="eventual" \
        ${ROOT_DIR}/charts/gha-runner-scale-set-controller \
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
        --set githubConfigUrl="https://github.com/${TARGET_ORG}/${TARGET_REPO}" \
        --set githubConfigSecret.github_token="${GITHUB_TOKEN}" \
        "${ROOT_DIR}/charts/gha-runner-scale-set" \
        --debug

    if ! NAME="${SCALE_SET_NAME}" NAMESPACE="${ARC_NAMESPACE}" wait_for_scale_set; then
        NAMESPACE="${ARC_NAMESPACE}" log_arc
        return 1
    fi
}

function upgrade_scale_set() {
    echo "Upgrading scale set ${SCALE_SET_NAME}/${SCALE_SET_NAMESPACE}"
    helm upgrade "${SCALE_SET_NAME}" \
        --namespace "${SCALE_SET_NAMESPACE}" \
        --set githubConfigUrl="https://github.com/${TARGET_ORG}/${TARGET_REPO}" \
        --set githubConfigSecret.github_token="${GITHUB_TOKEN}" \
        --set template.spec.containers[0].name="runner" \
        --set template.spec.containers[0].image="ghcr.io/actions/actions-runner:latest" \
        --set template.spec.containers[0].command={"/home/runner/run.sh"} \
        --set template.spec.containers[0].env[0].name="TEST" \
        --set template.spec.containers[0].env[0].value="E2E TESTS" \
        ${ROOT_DIR}/charts/gha-runner-scale-set \
        --version="${VERSION}" \
        --debug

}

function assert_listener_deleted() {
    local count=0
    while true; do
        LISTENER_COUNT="$(kubectl get pods -l actions.github.com/scale-set-name="${SCALE_SET_NAME}" -n "${ARC_NAMESPACE}" --field-selector=status.phase=Running -o=jsonpath='{.items}' | jq 'length')"
        RUNNERS_COUNT="$(kubectl get pods -l app.kubernetes.io/component=runner -n "${SCALE_SET_NAMESPACE}" --field-selector=status.phase=Running -o=jsonpath='{.items}' | jq 'length')"
        RESOURCES="$(kubectl get pods -A)"

        if [ "${LISTENER_COUNT}" -eq 0 ]; then
          echo "Listener has been deleted"
          echo "${RESOURCES}"
          return 0
        fi
        if [ "${count}" -ge 60 ]; then
          echo "Timeout waiting for listener to be deleted"
          echo "${RESOURCES}"
          return 1
        fi

        echo "Waiting for listener to be deleted"
        echo "Listener count: ${LISTENER_COUNT} target: 0 | Runners count: ${RUNNERS_COUNT} target: 3"

        sleep 1
        count=$((count+1))
      done
}

function assert_listener_recreated() {
    count=0
    while true; do
      LISTENER_COUNT="$(kubectl get pods -l actions.github.com/scale-set-name="${SCALE_SET_NAME}" -n "${ARC_NAMESPACE}" --field-selector=status.phase=Running -o=jsonpath='{.items}' | jq 'length')"
      RUNNERS_COUNT="$(kubectl get pods -l app.kubernetes.io/component=runner -n "${SCALE_SET_NAMESPACE}" --field-selector=status.phase=Running -o=jsonpath='{.items}' | jq 'length')"
      RESOURCES="$(kubectl get pods -A)"

      if [ "${LISTENER_COUNT}" -eq 1 ]; then
        echo "Listener is up!"
        echo "${RESOURCES}"
        exit 0
      fi
      if [ "${count}" -ge 120 ]; then
        echo "Timeout waiting for listener to be recreated"
        echo "${RESOURCES}"
        exit 1
      fi

      echo "Waiting for listener to be recreated"
      echo "Listener count: ${LISTENER_COUNT} target: 1 | Runners count: ${RUNNERS_COUNT} target: 0"

      sleep 1
      count=$((count+1))
    done
}

function main() {
    local failed=()

    build_image
    create_cluster
    install_arc

    install_scale_set || failed+=("install_scale_set")
    run_workflow || failed+=("run_workflow_1")

    upgrade_scale_set || failed+=("upgrade_scale_set")
    assert_listener_deleted || failed+=("assert_listener_deleted")
    assert_listener_recreated || failed+=("assert_listener_recreated")

    INSTALLATION_NAME="${SCALE_SET_NAME}" NAMESPACE="${SCALE_SET_NAMESPACE}" cleanup_scale_set || failed+=("cleanup_scale_set")
    NAMESPACE="${ARC_NAMESPACE}" arc_logs

    delete_cluster

    print_results "${failed[@]}"
}

main
