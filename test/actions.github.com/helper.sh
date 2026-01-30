#!/bin/bash

DIR="$(dirname "${BASH_SOURCE[0]}")"

DIR="$(realpath "${DIR}")"

ROOT_DIR="$(realpath "${DIR}/../..")"

export TARGET_ORG="${TARGET_ORG:-actions-runner-controller}"
export TARGET_REPO="${TARGET_REPO:-arc_e2e_test_dummy}"
export IMAGE_NAME="${IMAGE_NAME:-arc-test-image}"

# Tests decide which chart version to use. Helper provides extraction utilities.
function chart_version() {
    local chart_yaml="$1"
    if [[ -z "${chart_yaml}" ]] || [[ ! -f "${chart_yaml}" ]]; then
        echo "Chart.yaml not found: ${chart_yaml}" >&2
        return 1
    fi

    local version

    if command -v yq >/dev/null 2>&1; then
        # Support both common yq variants:
        # - mikefarah/yq: yq -r '.version' file
        # - kislyuk/yq:  yq '.version' < file
        if version="$(yq -r '.version' "${chart_yaml}" 2>/dev/null)"; then
            :
        else
            version="$(yq '.version' <"${chart_yaml}" 2>/dev/null)" || return 1
        fi

        version="$(printf '%s' "${version}" | tr -d "\"'[:space:]")"
        if [[ -z "${version}" ]]; then
            echo "Failed to extract version from ${chart_yaml} via yq" >&2
            return 1
        fi
        printf '%s\n' "${version}"
        return 0
    fi

    version="$(awk -F: '$1 ~ /^version$/ { v=$2; gsub(/[[:space:]\"\x27]/, "", v); print v; exit }' "${chart_yaml}")"
    version="$(printf '%s' "${version}" | tr -d "\"'[:space:]")"
    if [[ -z "${version}" ]]; then
        echo "Failed to extract version from ${chart_yaml}" >&2
        return 1
    fi
    printf '%s\n' "${version}"
}

# Backwards-compatible alias (kept so older local branches still work).
function extract_chart_version() { chart_version "$@"; }

function ensure_version_set() {
    if [[ -z "${VERSION:-}" ]]; then
        echo "VERSION is not set. Set it in the test, e.g. export VERSION=\"$(chart_version path/to/Chart.yaml)\"." >&2
        return 1
    fi

    # Defensive: if a tool produced quoted output, normalize it before using in tags/args.
    export VERSION="$(printf '%s' "${VERSION}" | tr -d "\"'[:space:]")"
    if [[ -z "${VERSION}" ]]; then
        echo "VERSION resolved to an empty value" >&2
        return 1
    fi

    export IMAGE_TAG="${IMAGE_TAG:-${VERSION}}"
    export IMAGE="${IMAGE:-${IMAGE_NAME}:${IMAGE_TAG}}"
}

export PLATFORMS="linux/amd64"
COMMIT_SHA="$(git rev-parse HEAD)"
export COMMIT_SHA

function build_image() {
    ensure_version_set || return 1
    echo "Building ARC image ${IMAGE}"

    cd "${ROOT_DIR}" || exit 1

    docker buildx build --platform "${PLATFORMS}" \
        --build-arg VERSION="${VERSION}" \
        --build-arg COMMIT_SHA="${COMMIT_SHA}" \
        -t "${IMAGE}" \
        -f Dockerfile \
        . --load

    echo "Created image ${IMAGE}"
    cd - || exit 1
}

function create_cluster() {
    ensure_version_set || return 1
    echo "Deleting minikube cluster if exists"
    minikube delete || true

    echo "Creating minikube cluster"
    minikube start --driver=docker --container-runtime=docker --wait=all

    echo "Verifying ns works"
    if ! minikube ssh "nslookup github.com >/dev/null 2>&1"; then
        echo "Nameserver configuration failed"
        exit 1
    fi

    echo "Loading image into minikube cluster"
    minikube image load "${IMAGE}"

    echo "Loading runner image into minikube cluster"
    minikube image load "ghcr.io/actions/actions-runner:latest"
}

function delete_cluster() {
    echo "Deleting minikube cluster"
    minikube delete
}

function log_arc() {
    echo "ARC logs"
    kubectl logs -n "${NAMESPACE}" -l "app.kubernetes.io/part-of=gha-rs-controller,app.kubernetes.io/component=controller-manager"
}

function wait_for_arc() {
    echo "Waiting for ARC to be ready"
    local count=0
    while true; do
        POD_NAME=$(kubectl get pods -n "${NAMESPACE}" -l "app.kubernetes.io/part-of=gha-rs-controller,app.kubernetes.io/component=controller-manager" -o name 2>/dev/null || true)
        if [ -n "$POD_NAME" ]; then
            echo "Pod found: $POD_NAME"
            break
        fi
        if [ "$count" -ge 60 ]; then
            echo "Timeout waiting for controller pod with labels app.kubernetes.io/part-of=gha-rs-controller,app.kubernetes.io/component=controller-manager"
            return 1
        fi
        sleep 1
        count=$((count + 1))
    done

    kubectl wait --timeout=60s --for=condition=ready pod -n "${NAMESPACE}" -l "app.kubernetes.io/part-of=gha-rs-controller,app.kubernetes.io/component=controller-manager"
    kubectl get pod -n "${NAMESPACE}" -l "app.kubernetes.io/part-of=gha-rs-controller,app.kubernetes.io/component=controller-manager"
    kubectl describe deployment -n "${NAMESPACE}" -l "app.kubernetes.io/part-of=gha-rs-controller,app.kubernetes.io/component=controller-manager"
}

function wait_for_scale_set() {
    local count=0
    while true; do
        POD_NAME=$(kubectl get pods -n "${NAMESPACE}" -l "actions.github.com/scale-set-name=${NAME}" -o name)
        if [ -n "$POD_NAME" ]; then
            echo "Pod found: ${POD_NAME}"
            break
        fi

        if [ "$count" -ge 60 ]; then
            echo "Timeout waiting for listener pod with label actions.github.com/scale-set-name=${NAME}"
            return 1
        fi

        sleep 1
        count=$((count + 1))
    done
    kubectl wait --timeout=30s --for=condition=ready pod -n "${NAMESPACE}" -l "actions.github.com/scale-set-name=${NAME}"
    kubectl get pod -n "${NAMESPACE}" -l "actions.github.com/scale-set-name=${NAME}"
}

function cleanup_scale_set() {
    helm uninstall "${INSTALLATION_NAME}" --namespace "${NAMESPACE}" --debug

    kubectl wait --timeout=40s --for=delete autoscalingrunnersets -n "${NAMESPACE}" -l app.kubernetes.io/instance="${INSTALLATION_NAME}"
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
    local repo
    repo="${TARGET_ORG}/${TARGET_REPO}"

    # Pick a ref for workflow dispatch. Default to the repo's default branch.
    if [[ -z "${WORKFLOW_REF:-}" ]]; then
        WORKFLOW_REF="$(gh repo view -R "${repo}" --json defaultBranchRef --jq '.defaultBranchRef.name' 2>/dev/null || true)"
        WORKFLOW_REF="${WORKFLOW_REF:-main}"
    fi

    local workflow_query
    workflow_query="${WORKFLOW_FILE}"

    local workflow_id
    if [[ "${workflow_query}" =~ ^[0-9]+$ ]]; then
        workflow_id="${workflow_query}"
    else
        local q_escaped
        q_escaped="${workflow_query//\"/\\\"}"
        workflow_id="$(gh workflow list -R "${repo}" --limit 200 --json id,name,path --jq ".[] | select((.path | endswith(\"${q_escaped}\")) or (.name == \"${q_escaped}\")) | .id" 2>/dev/null | head -n1)"

        if [[ -z "${workflow_id}" ]]; then
            # Common mismatch: .yml vs .yaml
            if [[ "${workflow_query}" == *.yaml ]]; then
                q_escaped="${workflow_query%.yaml}.yml"
            elif [[ "${workflow_query}" == *.yml ]]; then
                q_escaped="${workflow_query%.yml}.yaml"
            else
                q_escaped=""
            fi

            if [[ -n "${q_escaped}" ]]; then
                local q2_escaped
                q2_escaped="${q_escaped//\"/\\\"}"
                workflow_id="$(gh workflow list -R "${repo}" --limit 200 --json id,name,path --jq ".[] | select((.path | endswith(\"${q2_escaped}\")) or (.name == \"${q2_escaped}\")) | .id" 2>/dev/null | head -n1)"
                if [[ -n "${workflow_id}" ]]; then
                    WORKFLOW_FILE="${q_escaped}"
                fi
            fi
        fi
    fi

    if [[ -z "${workflow_id}" ]]; then
        echo "Workflow not found in ${repo}: ${WORKFLOW_FILE}" >&2
        echo "Available workflows in ${repo}:" >&2
        gh workflow list -R "${repo}" --limit 50 || true
        echo "Hint: set TARGET_ORG/TARGET_REPO to a repo that contains the workflow on its default branch, or set WORKFLOW_FILE to a valid workflow name/id/filename." >&2
        return 1
    fi

    echo "Resolved workflow id: ${workflow_id} (ref: ${WORKFLOW_REF})"

    local queue_time
    queue_time="$(date -u +%FT%TZ)"

    echo "Running workflow ${WORKFLOW_FILE}"
    gh workflow run -R "${repo}" "${workflow_id}" --ref "${WORKFLOW_REF}" -f arc_name="${SCALE_SET_NAME}" || return 1

    echo "Waiting for run to start"
    local count=0
    local run_id=
    while true; do
        if [[ "${count}" -ge 12 ]]; then
            echo "Timeout waiting for run to start"
            return 1
        fi
        run_id=$(gh run list -R "${repo}" --workflow "${workflow_id}" --created ">${queue_time}" --json "name,databaseId" --jq ".[] | select(.name | contains(\"${SCALE_SET_NAME}\")) | .databaseId")
        echo "Run ID: ${run_id}"
        if [ -n "$run_id" ]; then
            echo "Run found!"
            break
        fi

        echo "Run not found yet, waiting 5 seconds"
        sleep 5
        count=$((count + 1))
    done

    echo "Waiting for run to complete"
    gh run watch "${run_id}" -R "${repo}" --exit-status &>/dev/null
    local status=$?
    if [[ "${status}" -ne 0 ]]; then
        echo "Run failed with exit code ${status}"
        return 1
    fi

    echo "Run completed successfully"
}

function retry() {
    local retries=$1
    shift
    local delay=$1
    shift
    local n=1

    until "$@"; do
        if [[ $n -ge $retries ]]; then
            echo "Attempt $n failed! No more retries left."
            return 1
        else
            echo "Attempt $n failed! Retrying in $delay seconds..."
            sleep "$delay"
            n=$((n + 1))
        fi
    done
}

function install_openebs() {
    echo "Install openebs/dynamic-localpv-provisioner"
    helm repo add openebs https://openebs.github.io/openebs
    helm repo update
    helm install openebs openebs/openebs -n openebs --create-namespace
}
