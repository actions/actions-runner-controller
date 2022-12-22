#!/usr/bin/env bash

set -Eeuo pipefail

DOCKER=/usr/bin/docker
if [ ! -e $DOCKER ]; then
  DOCKER=/home/runner/bin/docker
fi

if [[ ${ARC_DOCKER_MTU_PROPAGATION:-false} == true ]] &&
  (($# >= 2)) && [[ $1 == network && $2 == create ]] &&
  mtu=$($DOCKER network inspect bridge --format '{{index .Options "com.docker.network.driver.mtu"}}' 2>/dev/null); then
  shift 2
  set -- network create --opt com.docker.network.driver.mtu="$mtu" "$@"
fi

exec $DOCKER "$@"
