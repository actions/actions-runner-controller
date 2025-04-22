#!/bin/bash

for chart in `ls charts`;
do
helm template --values charts/$chart/ci/ci-values.yaml charts/$chart | kube-score score - \
    --ignore-test pod-networkpolicy \
    --ignore-test deployment-has-poddisruptionbudget \
    --ignore-test deployment-has-host-podantiaffinity \
    --ignore-test pod-probes \
    --ignore-test container-image-tag \
    --enable-optional-test container-security-context-privileged \
    --enable-optional-test container-security-context-readonlyrootfilesystem \
    --ignore-test container-security-context
done
