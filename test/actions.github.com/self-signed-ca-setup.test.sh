#!/bin/bash

DIR="$(dirname "${BASH_SOURCE[0]}")"

DIR="$(realpath "${DIR}")"

ROOT_DIR="$(ralpath "${DIR}/../../")"

source "${DIR}/helper.sh"

SCALE_SET_NAME="self-signed-crt-$(date '+%M%S')$((($RANDOM + 100) % 100 + 1))"
SCALE_SET_NAMESPACE="arc-runners"
WORKFLOW_FILE="arc-test-workflow.yaml"
ARC_NAME="arc"
ARC_NAMESPACE="arc-systems"

function install_arc() {
    echo "Creating namespace ${ARC_NAMESPACE}"
    kubectl create namespace "${SCALE_SET_NAMESPACE}"

    echo "Installing ARC"
    helm install "${ARC_NAME}" \
        --namespace "${ARC_NAMESPACE}" \
        --create-namespace \
        --set image.repository="${IMAGE_NAME}" \
        --set image.tag="${IMAGE_TAG}" \
        ${ROOT_DIR}/charts/gha-runner-scale-set-controller \
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
        --from-file="${DIR}/mitmproxy/mitmproxy-ca-cert.pem"

    echo "Config map:"
    kubectl -n "${SCALE_SET_NAMESPACE}" get configmap ca-cert -o yaml

    echo "Installing scale set ${SCALE_SET_NAME}/${SCALE_SET_NAMESPACE}"
    helm install "${SCALE_SET_NAME}" \
        --namespace "${SCALE_SET_NAMESPACE}" \
        --create-namespace \
        --set githubConfigUrl="https://github.com/${TARGET_ORG}/${TARGET_REPO}" \
        --set githubConfigSecret.github_token="${GITHUB_TOKEN}" \
        --set proxy.https.url="http://host.minikube.internal:3128" \
        --set "proxy.noProxy[0]=10.96.0.1:443" \
        --set "githubServerTLS.certificateFrom.configMapKeyRef.name=ca-cert"
        --set "githubServerTLS.certificateFrom.configMapKeyRef.key=mitmproxy-ca-cert.pem"
        --set "githubServerTLS.runnerMountPath=/usr/local/share/ca-certificates/" \
        "${ROOT_DIR}/charts/gha-runner-scale-set" \
        --debug

    if ! NAME="${SCALE_SET_NAME}" NAMESPACE="${ARC_NAMESPACE}" wait_for_scale_set; then
        NAMESPACE="${ARC_NAMESPACE}" log_arc
        return 1
    fi
}

function wait_for_mitmproxy_cert() {
    echo "Waiting for mitmproxy generated CA certificate"
    local count=0
    while true; do
        if [ -f "./mitmproxy/mitmproxy-ca-cert.pem" ]; then
            echo "CA certificate is generated"
            echo "CA certificate:"
            cat "./mitmproxy/mitmproxy-ca-cert.pem"
            return 0
        fi

        if [ "${count}" -ge 60  ]; then
            echo "Timeout waiting for mitmproxy generated CA certificate"
            return 1
        fi

        sleep 1
        count=$((count + 1))
    done
}

function run_mitmproxy() {
    echo "Running mitmproxy"
    docker run -d \
        --rm \
        --name mitmproxy \
        --publish 8080:8080 \
        -b ./mitmproxy:/home/mitmproxy/.mitmproxy \
        mitmproxy/mitmproxy:latest \

    echo "Mitm dump:"
    mitmdump

    if ! wait_for_mitmproxy_cert; then
        return 1
    fi

    echo "CA certificate is generated"

    sudo cp ./mitmproxy/mitmproxy-ca-cert.pem /usr/local/share/ca-certificates/mitmproxy-ca-cert.crt
    sudo chown runner ./mitmproxy/mitmproxy-ca-cert.crt
}

function main() {
    local failed=()

    build_image
    create_cluster
    install_arc

    run_mitmproxy || failed+=("run_mitmproxy")
    install_scale_set || failed+=("install_scale_set")
    run_workflow || failed+=("run_workflow")
    INSTALLATION_NAME="${SCALE_SET_NAME}" NAMESPACE="${SCALE_SET_NAMESPACE}" cleanup_scale_set || failed+=("cleanup_scale_set")

    delete_cluster

    print_results "${failed[@]}"
}

main
