#!/usr/bin/env bash

set -e

OP=${OP:-apply}

RUNNER_LABEL=${RUNNER_LABEL:-self-hosted}

cat acceptance/testdata/kubernetes_container_mode.envsubst.yaml  | NAMESPACE=${RUNNER_NAMESPACE} envsubst  | kubectl apply -f -

if [ -n "${TEST_REPO}" ]; then
  if [ "${USE_RUNNERSET}" != "false" ]; then
    cat acceptance/testdata/runnerset.envsubst.yaml | TEST_ENTERPRISE= TEST_ORG= RUNNER_MIN_REPLICAS=${REPO_RUNNER_MIN_REPLICAS} NAME=repo-runnerset envsubst | kubectl ${OP} -f -
  else
    echo "Running ${OP} runnerdeployment and hra. Set USE_RUNNERSET if you want to deploy runnerset instead."
    cat acceptance/testdata/runnerdeploy.envsubst.yaml | TEST_ENTERPRISE= TEST_ORG= RUNNER_MIN_REPLICAS=${REPO_RUNNER_MIN_REPLICAS} NAME=repo-runnerdeploy envsubst | kubectl ${OP} -f -
  fi
else
  echo "Skipped ${OP} for runnerdeployment and hra. Set TEST_REPO to "yourorg/yourrepo" to deploy."
fi

if [ -n "${TEST_ORG}" ]; then
  if [ "${USE_RUNNERSET}" != "false" ]; then
    cat acceptance/testdata/runnerset.envsubst.yaml | TEST_ENTERPRISE= TEST_REPO= RUNNER_MIN_REPLICAS=${ORG_RUNNER_MIN_REPLICAS} NAME=org-runnerset envsubst | kubectl ${OP} -f -
  else
    cat acceptance/testdata/runnerdeploy.envsubst.yaml | TEST_ENTERPRISE= TEST_REPO= RUNNER_MIN_REPLICAS=${ORG_RUNNER_MIN_REPLICAS} NAME=org-runnerdeploy envsubst | kubectl ${OP} -f -
  fi

  if [ -n "${TEST_ORG_GROUP}" ]; then
    if [ "${USE_RUNNERSET}" != "false" ]; then
      cat acceptance/testdata/runnerset.envsubst.yaml | TEST_ENTERPRISE= TEST_REPO= RUNNER_MIN_REPLICAS=${ORG_RUNNER_MIN_REPLICAS} TEST_GROUP=${TEST_ORG_GROUP} NAME=orggroup-runnerset envsubst | kubectl ${OP} -f -
    else
      cat acceptance/testdata/runnerdeploy.envsubst.yaml | TEST_ENTERPRISE= TEST_REPO= RUNNER_MIN_REPLICAS=${ORG_RUNNER_MIN_REPLICAS} TEST_GROUP=${TEST_ORG_GROUP} NAME=orggroup-runnerdeploy envsubst | kubectl ${OP} -f -
    fi
  else
    echo "Skipped ${OP} on enterprise runnerdeployment. Set TEST_ORG_GROUP to ${OP}."
  fi
else
  echo "Skipped ${OP} on organizational runnerdeployment. Set TEST_ORG to ${OP}."
fi

if [ -n "${TEST_ENTERPRISE}" ]; then
  if [ "${USE_RUNNERSET}" != "false" ]; then
    cat acceptance/testdata/runnerset.envsubst.yaml | TEST_ORG= TEST_REPO= RUNNER_MIN_REPLICAS=${ENTERPRISE_RUNNER_MIN_REPLICAS} NAME=enterprise-runnerset envsubst | kubectl ${OP} -f -
  else
    cat acceptance/testdata/runnerdeploy.envsubst.yaml | TEST_ORG= TEST_REPO= RUNNER_MIN_REPLICAS=${ENTERPRISE_RUNNER_MIN_REPLICAS} NAME=enterprise-runnerdeploy envsubst | kubectl ${OP} -f -
  fi

  if [ -n "${TEST_ENTERPRISE_GROUP}" ]; then
    if [ "${USE_RUNNERSET}" != "false" ]; then
      cat acceptance/testdata/runnerset.envsubst.yaml | TEST_ORG= TEST_REPO= RUNNER_MIN_REPLICAS=${ENTERPRISE_RUNNER_MIN_REPLICAS} TEST_GROUP=${TEST_ENTERPRISE_GROUP} NAME=enterprisegroup-runnerset envsubst | kubectl ${OP} -f -
    else
      cat acceptance/testdata/runnerdeploy.envsubst.yaml | TEST_ORG= TEST_REPO= RUNNER_MIN_REPLICAS=${ENTERPRISE_RUNNER_MIN_REPLICAS} TEST_GROUP=${TEST_ENTERPRISE_GROUP} NAME=enterprisegroup-runnerdeploy envsubst | kubectl ${OP} -f -
    fi
  else
    echo "Skipped ${OP} on enterprise runnerdeployment. Set TEST_ENTERPRISE_GROUP to ${OP}."
  fi
else
  echo "Skipped ${OP} on enterprise runnerdeployment. Set TEST_ENTERPRISE to ${OP}."
fi
