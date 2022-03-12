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
  helm upgrade --install actions-runner-controller \
    charts/actions-runner-controller \
    -n actions-runner-system \
    --create-namespace \
    --set syncPeriod=${SYNC_PERIOD} \
    --set authSecret.create=false \
    --set image.repository=${NAME} \
    --set image.tag=${VERSION} \
    --set podAnnotations.test-id=${TEST_ID} \
    --set githubWebhookServer.podAnnotations.test-id=${TEST_ID} \
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

RUNNER_LABEL=${RUNNER_LABEL:-self-hosted}

if [ -n "${TEST_REPO}" ]; then
  if [ "${USE_RUNNERSET}" != "false" ]; then
    cat acceptance/testdata/runnerset.envsubst.yaml | TEST_ENTERPRISE= TEST_ORG= RUNNER_MIN_REPLICAS=${REPO_RUNNER_MIN_REPLICAS} NAME=repo-runnerset envsubst | kubectl apply -f -
  else
    echo 'Deploying runnerdeployment and hra. Set USE_RUNNERSET if you want to deploy runnerset instead.'
    cat acceptance/testdata/runnerdeploy.envsubst.yaml | TEST_ENTERPRISE= TEST_ORG= RUNNER_MIN_REPLICAS=${REPO_RUNNER_MIN_REPLICAS} NAME=repo-runnerdeploy envsubst | kubectl apply -f -
  fi
else
  echo 'Skipped deploying runnerdeployment and hra. Set TEST_REPO to "yourorg/yourrepo" to deploy.'
fi

if [ -n "${TEST_ORG}" ]; then
  if [ "${USE_RUNNERSET}" != "false" ]; then
    cat acceptance/testdata/runnerset.envsubst.yaml | TEST_ENTERPRISE= TEST_REPO= RUNNER_MIN_REPLICAS=${ORG_RUNNER_MIN_REPLICAS} NAME=org-runnerset envsubst | kubectl apply -f -
  else
    cat acceptance/testdata/runnerdeploy.envsubst.yaml | TEST_ENTERPRISE= TEST_REPO= RUNNER_MIN_REPLICAS=${ORG_RUNNER_MIN_REPLICAS} NAME=org-runnerdeploy envsubst | kubectl apply -f -
  fi

  if [ -n "${TEST_ORG_GROUP}" ]; then
    if [ "${USE_RUNNERSET}" != "false" ]; then
      cat acceptance/testdata/runnerset.envsubst.yaml | TEST_ENTERPRISE= TEST_REPO= RUNNER_MIN_REPLICAS=${ORG_RUNNER_MIN_REPLICAS} TEST_GROUP=${TEST_ORG_GROUP} NAME=orgroupg-runnerset envsubst | kubectl apply -f -
    else
      cat acceptance/testdata/runnerdeploy.envsubst.yaml | TEST_ENTERPRISE= TEST_REPO= RUNNER_MIN_REPLICAS=${ORG_RUNNER_MIN_REPLICAS} TEST_GROUP=${TEST_ORG_GROUP} NAME=orggroup-runnerdeploy envsubst | kubectl apply -f -
    fi
  else
    echo 'Skipped deploying enterprise runnerdeployment. Set TEST_ORG_GROUP to deploy.'
  fi
else
  echo 'Skipped deploying organizational runnerdeployment. Set TEST_ORG to deploy.'
fi

if [ -n "${TEST_ENTERPRISE}" ]; then
  if [ "${USE_RUNNERSET}" != "false" ]; then
    cat acceptance/testdata/runnerset.envsubst.yaml | TEST_ORG= TEST_REPO= RUNNER_MIN_REPLICAS=${ENTERPRISE_RUNNER_MIN_REPLICAS} NAME=enterprise-runnerset envsubst | kubectl apply -f -
  else
    cat acceptance/testdata/runnerdeploy.envsubst.yaml | TEST_ORG= TEST_REPO= RUNNER_MIN_REPLICAS=${ENTERPRISE_RUNNER_MIN_REPLICAS} NAME=enterprise-runnerdeploy envsubst | kubectl apply -f -
  fi

  if [ -n "${TEST_ENTERPRISE_GROUP}" ]; then
    if [ "${USE_RUNNERSET}" != "false" ]; then
      cat acceptance/testdata/runnerset.envsubst.yaml | TEST_ORG= TEST_REPO= RUNNER_MIN_REPLICAS=${ENTERPRISE_RUNNER_MIN_REPLICAS} TEST_GROUP=${TEST_ENTERPRISE_GROUP} NAME=enterprisegroup-runnerset envsubst | kubectl apply -f -
    else
      cat acceptance/testdata/runnerdeploy.envsubst.yaml | TEST_ORG= TEST_REPO= RUNNER_MIN_REPLICAS=${ENTERPRISE_RUNNER_MIN_REPLICAS} TEST_GROUP=${TEST_ENTERPRISE_GROUP} NAME=enterprisegroup-runnerdeploy envsubst | kubectl apply -f -
    fi
  else
    echo 'Skipped deploying enterprise runnerdeployment. Set TEST_ENTERPRISE_GROUP to deploy.'
  fi
else
  echo 'Skipped deploying enterprise runnerdeployment. Set TEST_ENTERPRISE to deploy.'
fi
