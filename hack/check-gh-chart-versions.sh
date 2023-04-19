#!/bin/bash
#
TEXT_RED='\033[0;31m'
TEXT_RESET='\033[0m'

chart_dir=$(pwd)/charts

controller_version=$(cat $chart_dir/gha-runner-scale-set-controller/Chart.yaml | yq .version)
controller_app_version=$(cat $chart_dir/gha-runner-scale-set-controller/Chart.yaml | yq .appVersion)

scaleset_version=$(cat $chart_dir/gha-runner-scale-set/Chart.yaml | yq .version)
scaleset_app_version=$(cat $chart_dir/gha-runner-scale-set/Chart.yaml | yq .appVersion)

if [[ $controller_version != $controller_app_version ]] || [[ $controller_version != $scaleset_version ]] || [[ $controller_version != $scaleset_app_version ]]; then
    echo -e "${TEXT_RED}Chart versions do not match${TEXT_RESET}"
    echo "Controller version:     " $controller_version
    echo "Controller app version: " $controller_app_version
    echo "Scale set version:      " $scaleset_version
    echo "Scale set app version:  " $scaleset_app_version
    exit 1
fi

echo "Chart versions: $controller_version"
