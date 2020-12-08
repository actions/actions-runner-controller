#!/usr/bin/env bash

set -e

runner_name=

while [ -z "${runner_name}" ]; do
  echo Finding the runner... 1>&2
  sleep 1
  runner_name=$(kubectl get runner --output=jsonpath="{.items[*].metadata.name}")
done

echo Found runner ${runner_name}.

pod_name=

while [ -z "${pod_name}" ]; do
  echo Finding the runner pod... 1>&2
  sleep 1
  pod_name=$(kubectl get pod --output=jsonpath="{.items[*].metadata.name}" | grep ${runner_name})
done

echo Found pod ${pod_name}.

echo Waiting for pod ${runner_name} to become ready... 1>&2

kubectl wait pod/${runner_name} --for condition=ready --timeout 180s

echo All tests passed. 1>&2
