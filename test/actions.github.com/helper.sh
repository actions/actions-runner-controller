#!/bin/bash

DIR="$(dirname "${BASH_SOURCE[0]}")"

DIR="$(realpath "${DIR}")"

ROOT_DIR="$(realpath "${DIR}/../..")"

export TARGET_ORG="${TARGET_ORG:-actions-runner-controller}"
export TARGET_REPO="${TARGET_REPO:-arc_e2e_test_dummy}"
export IMAGE_NAME="${IMAGE_NAME:-arc-test-image}"
export VERSION="${VERSION:-$(yq .version < "${ROOT_DIR}/charts/gha-runner-scale-set-controller/Chart.yaml")}"
export IMAGE_VERSION="${IMAGE_VERSION:-${VERSION}}"

function build_image() {
    echo "Building ARC image ${IMAGE_NAME}:${IMAGE_VERSION}"

    cd ${ROOT_DIR}

	export DOCKER_CLI_EXPERIMENTAL=enabled
	export DOCKER_BUILDKIT=1
	docker buildx build --platform ${PLATFORMS} \
		--build-arg RUNNER_VERSION=${RUNNER_VERSION} \
		--build-arg DOCKER_VERSION=${DOCKER_VERSION} \
		--build-arg VERSION=${VERSION} \
		--build-arg COMMIT_SHA=${COMMIT_SHA} \
		-t "${IMAGE_NAME}:${IMAGE_VERSION}" \
		-f Dockerfile \
		. --load

    echo "Created image ${IMAGE_NAME}:${IMAGE_VERSION}"
    cd -
}

function create_cluster() {
    echo "Deleting minikube cluster if exists"
    minikube delete || true

    echo "Creating minikube cluster"
    minikube start

    echo "Loading image into minikube cluster"
    minikube image load "${IMAGE_NAME}:${IMAGE_VERSION}"
}

function delete_cluster() {
    echo "Deleting minikube cluster"
    minikube delete
}

function install_arc() {
    echo "Installing ARC"

    helm install ${NAME} \
        --namespace ${NAMESPACE} \
        --create-namespace \
        --set image.repository=${IMAGE_NAME} \
        --set image.tag=${IMAGE_VERSION} \
        ${ROOT_DIR}/charts/gha-runner-scale-set-controller \
        --debug

    echo "Waiting for ARC to be ready"
    local count=0;
    while true; do
        POD_NAME=$(kubectl get pods -n ${NAMESPACE} -l app.kubernetes.io/name=gha-rs-controller -o name)
        if [ -n "$POD_NAME" ]; then
            echo "Pod found: $POD_NAME"
            break
        fi
        if [ "$count" -ge 60 ]; then
            echo "Timeout waiting for controller pod with label app.kubernetes.io/name=gha-rs-controller"
            return 1
        fi
        sleep 1
        count=$((count+1))
    done

    kubectl wait --timeout=30s --for=condition=ready pod -n "${NAMESPACE}" -l app.kubernetes.io/name=gha-rs-controller
    kubectl get pod -n "${NAMESPACE}"
    kubectl describe deployment "${NAME}" -n "${NAMESPACE}"
}

function log_arc() {
    echo "ARC logs"
    kubectl logs -n "${NAMESPACE}" -l app.kubernetes.io/name=gha-rs-controller
}

function wait_for_scale_set() {
    local count=0
    while true; do
        POD_NAME=$(kubectl get pods -n ${NAMESPACE} -l actions.github.com/scale-set-name=${NAME} -o name)
        if [ -n "$POD_NAME" ]; then
            echo "Pod found: ${POD_NAME}"
            break
        fi

        if [ "$count" -ge 60 ]; then
            echo "Timeout waiting for listener pod with label actions.github.com/scale-set-name=${NAME}"
            return 1
        fi

        sleep 1
        count=$((count+1))
    done
    kubectl wait --timeout=30s --for=condition=ready pod -n ${NAMESPACE} -l actions.github.com/scale-set-name=${NAME}
    kubectl get pod -n ${NAMESPACE}
}

function cleanup_scale_set() {
    helm uninstall "${INSTALLATION_NAME}" --namespace "${NAMESPACE}" --debug

    kubectl wait --timeout=10s --for=delete AutoScalingRunnerSet -n "${NAMESPACE}" -l app.kubernetes.io/instance="${INSTALLATION_NAME}" --ignore-not-found
}
