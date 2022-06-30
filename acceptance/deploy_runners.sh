#!/usr/bin/env bash

set -e

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
      cat acceptance/testdata/runnerset.envsubst.yaml | TEST_ENTERPRISE= TEST_REPO= RUNNER_MIN_REPLICAS=${ORG_RUNNER_MIN_REPLICAS} TEST_GROUP=${TEST_ORG_GROUP} NAME=orggroup-runnerset envsubst | kubectl apply -f -
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
