#!/bin/bash

docker run --rm -it -w /repo -v $(pwd):/repo quay.io/helmpack/chart-testing ct lint --all --config charts/.ci/ct-config.yaml
