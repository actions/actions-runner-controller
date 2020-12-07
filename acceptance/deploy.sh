#!/usr/bin/env bash

set -e

tpe=${ACCEPTANCE_TEST_SECRET_TYPE}

if [ "${tpe}" == "token" ]; then
  kubectl create secret generic controller-manager \
	  -n actions-runner-system \
	  --from-literal=github_token=${GITHUB_TOKEN:?GITHUB_TOKEN must not be empty}
elif [ "${tpe}" == "app" ]; then
  kubectl create secret generic controller-manager \
    -n actions-runner-system \
    --from-literal=github_app_id=${APP_ID:?must not be empty} \
    --from-literal=github_app_installation_id=${INSTALLATION_ID:?must not be empty} \
    --from-file=github_app_private_key=${PRIVATE_KEY_FILE_PATH:?must not be empty}
else
  echo "ACCEPTANCE_TEST_SECRET_TYPE must be set to either \"token\" or \"app\"" 1>&2
  exit 1
fi

tool=${ACCEPTANCE_TEST_DEPLOYMENT_TOOL}

if [ "${tool}" == "helm" ]; then
  helm upgrade --install actions-runner-controller \
    charts/actions-runner-controller \
    -n actions-runner-system \
    --create-namespace \
    --set syncPeriod=5m
  kubectl -n actions-runner-system wait deploy/actions-runner-controller --for condition=available
else
  kubectl apply \
    -n actions-runner-system \
    -f release/actions-runner-controller.yaml
  kubectl -n actions-runner-system wait deploy/controller-manager --for condition=available --timeout 60s
fi

# Adhocly wait for some time until actions-runner-controller's admission webhook gets ready
sleep 20

kubectl apply \
  -f acceptance/testdata/runnerdeploy.yaml
