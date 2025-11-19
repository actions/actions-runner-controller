#!/bin/bash

set -euo pipefail

DIR="$(realpath "$(dirname "${BASH_SOURCE[0]}")")"

ROOT_DIR="$(realpath "${DIR}/../..")"

source "${DIR}/helper.sh" || {
    echo "Failed to source helper.sh"
    exit 1
}

TEMP_DIR=$(mktemp -d)
LOCAL_CERT_PATH="${TEMP_DIR}/mitmproxy-ca-cert.crt"
MITM_CERT_PATH="/root/.mitmproxy/mitmproxy-ca-cert.pem"

trap 'rm -rf "$TEMP_DIR"' EXIT

SCALE_SET_NAME="self-signed-crt-$(date '+%M%S')$(((RANDOM + 100) % 100 + 1))"
SCALE_SET_NAMESPACE="arc-runners"
WORKFLOW_FILE="arc-test-workflow.yaml"
ARC_NAME="arc"
ARC_NAMESPACE="arc-systems"

MITMPROXY_NAMESPACE="mitmproxy"
MITMPROXY_POD_NAME="mitmproxy"

function install_arc() {
    echo "Installing ARC"
    helm install "${ARC_NAME}" \
        --namespace "${ARC_NAMESPACE}" \
        --create-namespace \
        --set image.repository="${IMAGE_NAME}" \
        --set image.tag="${IMAGE_TAG}" \
        "${ROOT_DIR}/charts/gha-runner-scale-set-controller" \
        --debug

    if ! NAME="${ARC_NAME}" NAMESPACE="${ARC_NAMESPACE}" wait_for_arc; then
        NAMESPACE="${ARC_NAMESPACE}" log_arc
        return 1
    fi
}

function install_scale_set() {
    echo "Creating namespace ${SCALE_SET_NAMESPACE}"
    kubectl create namespace "${SCALE_SET_NAMESPACE}"

    echo "Installing ca-cert config map"
    kubectl -n "${SCALE_SET_NAMESPACE}" create configmap ca-cert \
        --from-file=mitmproxy-ca-cert.crt="${LOCAL_CERT_PATH}"

    echo "Installing scale set ${SCALE_SET_NAME}/${SCALE_SET_NAMESPACE}"
    helm install "${SCALE_SET_NAME}" \
        --namespace "${SCALE_SET_NAMESPACE}" \
        --create-namespace \
        --set githubConfigUrl="https://github.com/${TARGET_ORG}/${TARGET_REPO}" \
        --set githubConfigSecret.github_token="${GITHUB_TOKEN}" \
        --set proxy.https.url="http://mitmproxy.mitmproxy.svc.cluster.local:8080" \
        --set "proxy.noProxy[0]=10.96.0.1:443" \
        --set "githubServerTLS.certificateFrom.configMapKeyRef.name=ca-cert" \
        --set "githubServerTLS.certificateFrom.configMapKeyRef.key=mitmproxy-ca-cert.crt" \
        --set "githubServerTLS.runnerMountPath=/usr/local/share/ca-certificates/" \
        "${ROOT_DIR}/charts/gha-runner-scale-set" \
        --debug

    if ! NAME="${SCALE_SET_NAME}" NAMESPACE="${ARC_NAMESPACE}" wait_for_scale_set; then
        NAMESPACE="${ARC_NAMESPACE}" log_arc
        return 1
    fi
}

function wait_for_mitmproxy_ready() {
    echo "Waiting for mitmproxy pod to be ready"

    # Wait for pod to be running
    if ! kubectl wait --for=condition=ready pod -n "${MITMPROXY_NAMESPACE}" "${MITMPROXY_POD_NAME}" --timeout=60s; then
        echo "Timeout waiting for mitmproxy pod"
        kubectl get pods -n "${MITMPROXY_NAMESPACE}" || true
        kubectl describe pod -n "${MITMPROXY_NAMESPACE}" "${MITMPROXY_POD_NAME}" || true
        kubectl logs -n "${MITMPROXY_NAMESPACE}" "${MITMPROXY_POD_NAME}" || true
        return 1
    fi

    echo "Mitmproxy pod is ready, trying to copy the certitficate..."

    # Verify certificate exists
    retry 15 1 kubectl exec -n "${MITMPROXY_NAMESPACE}" "${MITMPROXY_POD_NAME}" -- test -f "${MITM_CERT_PATH}"

    echo "Getting mitmproxy CA certificate from pod"
    if ! kubectl exec -n "${MITMPROXY_NAMESPACE}" "${MITMPROXY_POD_NAME}" -- cat "${MITM_CERT_PATH}" >"${LOCAL_CERT_PATH}"; then
        echo "Failed to get mitmproxy CA certificate from pod"
        return 1
    fi
    echo "Mitmproxy certificate generated successfully and stored to ${LOCAL_CERT_PATH}"
    return 0
}

function run_mitmproxy() {
    echo "Deploying mitmproxy to Kubernetes"

    # Create namespace
    kubectl create namespace "${MITMPROXY_NAMESPACE}" || true

    # Create mitmproxy pod and service
    kubectl apply -f "${DIR}/self-signed-ca-setup.mitm.yaml"

    if ! wait_for_mitmproxy_ready; then
        return 1
    fi

    echo "Mitmproxy is ready"
}

function main() {
    local failed=()

    build_image
    create_cluster
    install_arc
    run_mitmproxy || {
        echo "Failed to run mitmproxy"
        echo "ARC logs:"
        NAMESPACE="${ARC_NAMESPACE}" log_arc
        echo "Deleting cluster..."
        delete_cluster
        exit 1
    }
    install_scale_set || {
        echo "Failed to run mitmproxy"
        echo "ARC logs:"
        NAMESPACE="${ARC_NAMESPACE}" log_arc
        echo "Deleting cluster..."
        delete_cluster
        exit 1
    }

    WORKFLOW_FILE="${WORKFLOW_FILE}" SCALE_SET_NAME="${SCALE_SET_NAME}" run_workflow || failed+=("run_workflow")
    INSTALLATION_NAME="${SCALE_SET_NAME}" NAMESPACE="${SCALE_SET_NAMESPACE}" cleanup_scale_set || failed+=("cleanup_scale_set")

    NAMESPACE="${ARC_NAMESPACE}" log_arc || failed+=("log_arc")

    delete_cluster

    print_results "${failed[@]}"
}

main
