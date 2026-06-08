#!/bin/bash

DIR="$(dirname "${BASH_SOURCE[0]}")"

DIR="$(realpath "${DIR}")"

ROOT_DIR="$(realpath "${DIR}/../..")"

export TARGET_ORG="${TARGET_ORG:-actions-runner-controller}"
export TARGET_REPO="${TARGET_REPO:-arc_e2e_test_dummy}"
export IMAGE_NAME="${IMAGE_NAME:-arc-test-image}"

function log() {
    echo "[$(date -u +%FT%T%Z)] $*" >&2
}

# Trims a single pair of matching surrounding quotes from the provided string.
# Examples:
#   trim_quotes '"1.2.3"' -> 1.2.3
#   trim_quotes "'v1'"   -> v1
function trim_quotes() {
    local s
    s="$*"

    if [[ ${#s} -ge 2 ]]; then
        local first last
        first="${s:0:1}"
        last="${s: -1}"
        if [[ ( "${first}" == '"' && "${last}" == '"' ) || ( "${first}" == "'" && "${last}" == "'" ) ]]; then
            s="${s:1:${#s}-2}"
        fi
    fi

    printf '%s\n' "${s}"
}

# Tests decide which chart version to use. Helper provides extraction utilities.
function chart_version() {
    local chart_yaml="$1"
    if [[ -z "${chart_yaml}" ]] || [[ ! -f "${chart_yaml}" ]]; then
        echo "Chart.yaml not found: ${chart_yaml}" >&2
        return 1
    fi

    local version
    version=""

    if command -v yq >/dev/null 2>&1; then
        # Prefer yq v4+ syntax, but accept older variants.
        version="$(yq -r '.version' "${chart_yaml}" 2>/dev/null || true)"
        if [[ -z "${version}" ]] || [[ "${version}" == "null" ]]; then
            version="$(yq '.version' <"${chart_yaml}" 2>/dev/null || true)"
        fi
    fi

    # Fallback for environments without yq.
    if [[ -z "${version}" ]] || [[ "${version}" == "null" ]]; then
        version="$(awk -F: 'tolower($1)=="version" {sub(/^[[:space:]]+/,"",$2); print $2; exit}' "${chart_yaml}" 2>/dev/null || true)"
    fi

    version="$(trim_quotes "${version}" | tr -d "[:space:]")"
    if [[ -z "${version}" ]]; then
        log "Failed to extract version from ${chart_yaml} via yq"
        return 1
    fi
    printf '%s\n' "${version}"
    return 0
}

function ensure_version_set() {
    if [[ -z "${VERSION:-}" ]]; then
        log 'VERSION is not set. Set it in the test, e.g. export VERSION="$(chart_version path/to/Chart.yaml)".'
        return 1
    fi

    # Defensive: if a tool produced quoted output, normalize it before using in tags/args.
    export VERSION="$(printf '%s' "${VERSION}" | tr -d "\"'[:space:]")"
    if [[ -z "${VERSION}" ]]; then
        log "VERSION resolved to an empty value"
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
    log "Building ARC image ${IMAGE}"

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
    log "Deleting minikube cluster if exists"
    minikube delete || true

    log "Creating minikube cluster"
    minikube start --driver=docker --container-runtime=docker --wait=all

    log "Verifying ns works"
    if ! minikube ssh "nslookup github.com >/dev/null 2>&1"; then
        log "Nameserver configuration failed"
        exit 1
    fi

    log "Loading image into minikube cluster"
    minikube image load "${IMAGE}"

    log "Loading runner image into minikube cluster"
    minikube image load "ghcr.io/actions/actions-runner:latest"
}

function delete_cluster() {
    log "Deleting minikube cluster"
    minikube delete
}

function log_arc() {
    log "ARC logs"
    kubectl logs -n "${NAMESPACE}" -l "app.kubernetes.io/part-of=gha-rs-controller,app.kubernetes.io/component=controller-manager"
}

function wait_for_arc() {
    log "Waiting for ARC to be ready"
    local count=0
    while true; do
        POD_NAME=$(kubectl get pods -n "${NAMESPACE}" -l "app.kubernetes.io/part-of=gha-rs-controller,app.kubernetes.io/component=controller-manager" -o name 2>/dev/null || true)
        if [ -n "$POD_NAME" ]; then
            log "Pod found: $POD_NAME"
            break
        fi
        if [ "$count" -ge 60 ]; then
            log "Timeout waiting for controller pod with labels app.kubernetes.io/part-of=gha-rs-controller,app.kubernetes.io/component=controller-manager"
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
            log "Pod found: ${POD_NAME}"
            break
        fi

        if [ "$count" -ge 60 ]; then
            log "Timeout waiting for listener pod with label actions.github.com/scale-set-name=${NAME}"
            return 1
        fi

        sleep 1
        count=$((count + 1))
    done
    kubectl wait --timeout=30s --for=condition=ready pod -n "${NAMESPACE}" -l "actions.github.com/scale-set-name=${NAME}"
    kubectl get pod -n "${NAMESPACE}" -l "actions.github.com/scale-set-name=${NAME}"
}

function cleanup_scale_set() {
    log "Uninstalling Helm release ${INSTALLATION_NAME}"
    helm uninstall "${INSTALLATION_NAME}" --namespace "${NAMESPACE}" --debug

    log "Waiting for autoscaling runner sets to be deleted"
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

function extract_run_id() {
    local workflow_output="$1"
    local run_id

    run_id="$(printf '%s\n' "${workflow_output}" | awk '/^[[:space:]]*[0-9]+[[:space:]]*$/ { gsub(/[[:space:]]/, ""); print; exit }')"
    if [[ -z "${run_id}" ]]; then
        run_id="$(printf '%s\n' "${workflow_output}" | awk 'match($0, /actions\/runs\/[0-9]+/) { run=substr($0, RSTART, RLENGTH); sub(/^actions\/runs\//, "", run); print run; exit }')"
    fi

    if [[ -z "${run_id}" ]]; then
        log "Failed to extract run id from output: ${workflow_output}"
        return 1
    fi

    printf '%s\n' "${run_id}"
}

function start_workflow() {
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
        log "Workflow not found in ${repo}: ${WORKFLOW_FILE}" >&2
        log "Available workflows in ${repo}:" >&2
        gh workflow list -R "${repo}" --limit 50 || true
        log "Hint: set TARGET_ORG/TARGET_REPO to a repo that contains the workflow on its default branch, or set WORKFLOW_FILE to a valid workflow name/id/filename." >&2
        return 1
    fi

    log "Resolved workflow id: ${workflow_id} (ref: ${WORKFLOW_REF})"

    local queue_time
    queue_time="$(date -u +%FT%TZ)"

    log "Running workflow ${WORKFLOW_FILE}"
    local workflow_output
    workflow_output="$(gh workflow run -R "${repo}" "${workflow_id}" --ref "${WORKFLOW_REF}" -f arc_name="${SCALE_SET_NAME}")" || return 1
    if [[ -n "${workflow_output}" ]]; then
        log "${workflow_output}"
    fi

    log "Waiting for run to start"
    local count=0
    local run_id=
    local run_id_output=
    while true; do
        if [[ "${count}" -ge 12 ]]; then
            log "Timeout waiting for run to start"
            return 1
        fi
        run_id_output=$(gh run list -R "${repo}" --workflow "${workflow_id}" --created ">${queue_time}" --json "name,databaseId" --jq ".[] | select(.name | contains(\"${SCALE_SET_NAME}\")) | .databaseId")
        if [[ -n "${run_id_output}" ]]; then
            run_id=$(extract_run_id "${run_id_output}" || true)
        fi
        log "Run ID: ${run_id}"
        if [ -n "$run_id" ]; then
            log "Run found!"
            break
        fi

        log "Run not found yet, waiting 5 seconds"
        sleep 5
        count=$((count + 1))
    done

    echo "${run_id}"
}

function wait_for_run_completion() {
    local run_id="$1"
    local repo="${TARGET_ORG}/${TARGET_REPO}"

    log "Waiting for run ${run_id} to complete"
    gh run watch "${run_id}" -R "${repo}" --exit-status &>/dev/null
    local status=$?
    if [[ "${status}" -ne 0 ]]; then
        log "Run failed with exit code ${status}"
        return 1
    fi

    log "Run completed successfully"
}

function run_workflow() {
    local run_id
    if ! run_id=$(start_workflow); then
        log "Failed to start workflow"
        return 1
    fi
    wait_for_run_completion "${run_id}"
}

function retry() {
    local retries=$1
    shift
    local delay=$1
    shift
    local n=1

    until "$@"; do
        if [[ $n -ge $retries ]]; then
            log "Attempt $n failed! No more retries left."
            return 1
        else
            log "Attempt $n failed! Retrying in $delay seconds..."
            sleep "$delay"
            n=$((n + 1))
        fi
    done
}

function install_openebs() {
    log "Install openebs/dynamic-localpv-provisioner"
    helm repo add openebs https://openebs.github.io/openebs
    helm repo update
    helm install openebs openebs/openebs -n openebs --create-namespace
}
