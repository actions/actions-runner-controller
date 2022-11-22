#!/usr/bin/env bash

set -e

tpe=${ACCEPTANCE_TEST_SECRET_TYPE}

VALUES_FILE=${VALUES_FILE:-$(dirname $0)/values.yaml}

kubectl delete secret -n actions-runner-system controller-manager || :

if [ "${tpe}" == "token" ]; then
  if ! kubectl get secret controller-manager -n actions-runner-system >/dev/null; then
    kubectl create secret generic controller-manager \
      -n actions-runner-system \
      --from-literal=github_token=${GITHUB_TOKEN:?GITHUB_TOKEN must not be empty}
  fi
elif [ "${tpe}" == "app" ]; then
  kubectl create secret generic controller-manager \
    -n actions-runner-system \
    --from-literal=github_app_id=${APP_ID:?must not be empty} \
    --from-literal=github_app_installation_id=${APP_INSTALLATION_ID:?must not be empty} \
    --from-file=github_app_private_key=${APP_PRIVATE_KEY_FILE:?must not be empty}
else
  echo "ACCEPTANCE_TEST_SECRET_TYPE must be set to either \"token\" or \"app\"" 1>&2
  exit 1
fi

if [ -n "${WEBHOOK_GITHUB_TOKEN}" ]; then
  kubectl -n actions-runner-system delete secret \
      github-webhook-server || :
  kubectl -n actions-runner-system create secret generic \
      github-webhook-server \
      --from-literal=github_token=${WEBHOOK_GITHUB_TOKEN:?WEBHOOK_GITHUB_TOKEN must not be empty}
else
  echo 'Skipped deploying secret "github-webhook-server". Set WEBHOOK_GITHUB_TOKEN to deploy.' 1>&2
fi

tool=${ACCEPTANCE_TEST_DEPLOYMENT_TOOL}

TEST_ID=${TEST_ID:-default}

if [ "${tool}" == "helm" ]; then
  set -v

  CHART=${CHART:-charts/actions-runner-controller}

  flags=()
  if [ "${IMAGE_PULL_SECRET}" != "" ]; then
    flags+=( --set imagePullSecrets[0].name=${IMAGE_PULL_SECRET})
    flags+=( --set image.actionsRunnerImagePullSecrets[0].name=${IMAGE_PULL_SECRET})
    flags+=( --set githubWebhookServer.imagePullSecrets[0].name=${IMAGE_PULL_SECRET})
  fi
  if [ "${CHART_VERSION}" != "" ]; then
    flags+=( --version ${CHART_VERSION})
  fi
  if [ "${LOG_FORMAT}" != "" ]; then
    flags+=( --set logFormat=${LOG_FORMAT})
    flags+=( --set githubWebhookServer.logFormat=${LOG_FORMAT})
  fi

  set -vx

  helm upgrade --install actions-runner-controller \
    ${CHART} \
    -n actions-runner-system \
    --create-namespace \
    --set syncPeriod=${SYNC_PERIOD} \
    --set authSecret.create=false \
    --set image.repository=${NAME} \
    --set image.tag=${VERSION} \
    --set podAnnotations.test-id=${TEST_ID} \
    --set githubWebhookServer.podAnnotations.test-id=${TEST_ID} \
    ${flags[@]} --set image.imagePullPolicy=${IMAGE_PULL_POLICY} \
    -f ${VALUES_FILE}
  set +v
  # To prevent `CustomResourceDefinition.apiextensions.k8s.io "runners.actions.summerwind.dev" is invalid: metadata.annotations: Too long: must have at most 262144 bytes`
  # errors
  kubectl create -f charts/actions-runner-controller/crds || kubectl replace -f charts/actions-runner-controller/crds
  # This wait fails due to timeout when it's already in crashloopback and this update doesn't change the image tag.
  # That's why we add `|| :`. With that we prevent stopping the script in case of timeout and
  # proceed to delete (possibly in crashloopback and/or running with outdated image) pods so that they are recreated by K8s.
  kubectl -n actions-runner-system wait deploy/actions-runner-controller --for condition=available --timeout 60s || :
else
  kubectl apply \
    -n actions-runner-system \
    -f release/actions-runner-controller.yaml
  kubectl -n actions-runner-system wait deploy/controller-manager --for condition=available --timeout 120s || :
fi

# Restart all ARC pods
kubectl -n actions-runner-system delete po -l app.kubernetes.io/name=actions-runner-controller

echo Waiting for all ARC pods to be up and running after restart

kubectl -n actions-runner-system wait deploy/actions-runner-controller --for condition=available --timeout 120s

# Adhocly wait for some time until actions-runner-controller's admission webhook gets ready
sleep 20
