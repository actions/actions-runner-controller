#!/bin/bash
# Checks the chart versions against an input version. Fails on mismatch.
#
# Usage:
#   check-gh-chart-versions.sh <VERSION>

set -eo pipefail

TEXT_RED='\033[0;31m'
TEXT_RESET='\033[0m'
TEXT_GREEN='\033[0;32m'

target_version=$1
if [[ $# -eq 0 ]]; then
    echo "Release version argument is required"
    echo
    echo "Usage:  ${0} <VERSION>"
    exit 1
fi

chart_dir="$(pwd)/charts"

controller_version=$(yq .version < "${chart_dir}/gha-runner-scale-set-controller/Chart.yaml")
controller_app_version=$(yq .appVersion < "${chart_dir}/gha-runner-scale-set-controller/Chart.yaml")

scaleset_version=$(yq .version < "${chart_dir}/gha-runner-scale-set/Chart.yaml")
scaleset_app_version=$(yq .appVersion < "${chart_dir}/gha-runner-scale-set/Chart.yaml")

if [[ "${controller_version}" != "${target_version}" ]] ||
   [[ "${controller_app_version}" != "${target_version}" ]] ||
   [[ "${scaleset_version}" != "${target_version}" ]] ||
   [[ "${scaleset_app_version}" != "${target_version}" ]]; then
    echo -e "${TEXT_RED}Chart versions do not match${TEXT_RESET}"
    echo "Target version:         ${target_version}"
    echo "Controller version:     ${controller_version}"
    echo "Controller app version: ${controller_app_version}"
    echo "Scale set version:      ${scaleset_version}"
    echo "Scale set app version:  ${scaleset_app_version}"
    exit 1
fi

echo -e "${TEXT_GREEN}Chart versions: ${controller_version}"
