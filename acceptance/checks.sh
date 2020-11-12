#!/usr/bin/env bash

set -e

runner_name=

while [ -z "${runner_name}" ]; do
  echo Finding the runner... 1>&2
  sleep 1
  runner_name=$(kubectl get runner --output=jsonpath="{.items[0].metadata.name}")
done

echo Found ${runner_name}.

while kubectl get pod --output=jsonpath="{.items[0].metadata.name}" | grep -v ${runner_name}; do
  echo Finding the runner pod... 1>&2
  sleep 1
done

echo Waiting for pod ${runner_name} to become ready... 1>&2

kubectl wait pod/${runner_name} --for condition=ready --timeout 120s

echo All tests passed. 1>&2
