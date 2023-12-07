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

function log_arc() {
    echo "ARC logs"
    kubectl logs -n "${NAMESPACE}" -l app.kubernetes.io/name=gha-rs-controller
}

function wait_for_arc() {
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

    kubectl wait --timeout=40s --for=delete autoscalingrunnersets -n "${NAMESPACE}" -l app.kubernetes.io/instance="${INSTALLATION_NAME}"
}

function install_openebs() {
    echo "Install openebs/dynamic-localpv-provisioner"
    helm repo add openebs https://openebs.github.io/charts
    helm repo update
    helm install openebs openebs/openebs --namespace openebs --create-namespace
}

function print_results() {
    local failed=("$@")

    if [[ "${#failed[@]}" -ne 0 ]]; then
        echo "----------------------------------"
        echo "The following tests failed:"
        for test in "${failed[@]}"; do
            echo "  - ${test}"
        done
        return 1
    else
        echo "----------------------------------"
        echo "All tests passed!"
    fi
}

function run_workflow() {
    echo "Checking if the workflow file exists"
    gh workflow view -R "${TARGET_ORG}/${TARGET_REPO}" "${WORKFLOW_FILE}" &> /dev/null || return 1

    local queue_time="$(date -u +%FT%TZ)"

    echo "Running workflow ${workflow_file}"
    gh workflow run -R "${TARGET_ORG}/${TARGET_REPO}" "${WORKFLOW_FILE}" --ref main -f arc_name="${SCALE_SET_NAME}" || return 1

    echo "Waiting for run to start"
    local count=0
    local run_id=
    while true; do
        if [[ "${count}" -ge 12 ]]; then
            echo "Timeout waiting for run to start"
            return 1
        fi
        run_id=$(gh run list -R "${TARGET_ORG}/${TARGET_REPO}" --workflow "${WORKFLOW_FILE}" --created ">${queue_time}" --json "name,databaseId" --jq ".[] | select(.name | contains(\"${SCALE_SET_NAME}\")) | .databaseId")
        echo "Run ID: ${run_id}"
        if [ -n "$run_id" ]; then
            echo "Run found!"
            break
        fi

        echo "Run not found yet, waiting 5 seconds"
        sleep 5
        count=$((count+1))
    done

    echo "Waiting for run to complete"
    local code=$(gh run watch "${run_id}" -R "${TARGET_ORG}/${TARGET_REPO}" --exit-status &> /dev/null)
    if [[ "${code}" -ne 0 ]]; then
        echo "Run failed with exit code ${code}"
        return 1
    fi

    echo "Run completed successfully"
}
