#!/usr/bin/env bash
set -Eeuo pipefail

# This path can only be customized through arguments passed to the dockerd cli
# at startup. Parsing the supervisor docker.conf file is cumbersome, and there
# is little reason to allow users to customize this. Hence, we hardcode the path
# here.
#
# See: https://docs.docker.com/engine/reference/commandline/dockerd/
readonly dockerd_config_path=/etc/docker/daemon.json
readonly dockerd_supervisor_config_path=/etc/supervisor/conf.d/dockerd.conf

# Extend existing config...
dockerd_config=$(! cat $dockerd_config_path 2>&-)

# ...and fallback to an empty object if there is no existing configuration, or
# otherwise the following `jq` merge commands would not produce anything.
if [[ $dockerd_config == '' ]]; then
  dockerd_config='{}'
fi

readonly MTU=${MTU:-}
if [[ $MTU ]]; then
  # We have to verify that this value is a valid integer because we are going to
  # start docker in the background and invalid values would never be surfaced.
  # Well, except that startup fails and our users are sent on a nice debugging
  # session.
  if [[ $MTU =~ ^[1-9][0-9]*$ ]]; then
    # See https://docs.docker.com/engine/security/rootless/
    sudo tee -a $dockerd_supervisor_config_path 1>&- <<<"environment=DOCKERD_ROOTLESS_ROOTLESSKIT_MTU=$MTU"
    dockerd_config=$(jq -S ".mtu = $MTU" <<<"$dockerd_config")
  else
    # shellcheck source=runner/logger.bash
    source logger.bash
    log.error "Docker MTU must be an integer, continuing bootstrapping without it, got: $MTU"
    MTU=
  fi
fi

if [[ ${DOCKER_REGISTRY_MIRROR:-} != '' ]]; then
  dockerd_config=$(jq -S --arg url "$DOCKER_REGISTRY_MIRROR" '."registry-mirrors" += ["$url"]' <<<"$dockerd_config")
fi

sudo mkdir -p ${dockerd_config_path%/*}
sudo tee $dockerd_config_path 1>&- <<<"$dockerd_config"

for config in $dockerd_config_path $dockerd_supervisor_config_path; do
  (
    echo "Using '$config' with the following content <<CONFIG"
    cat $config
    echo 'CONFIG'
  ) 1>&2
done

sudo /usr/bin/supervisord -c /etc/supervicor/supervisord.conf

if [[ $MTU ]]; then
  # It might take a few ticks for the `docker0` device to be up after starting
  # the docker daemon with supervisord, hence, we retry for a little while.
  # Notice how we use `nice` to execute the command with a least favorable
  # scheduling priority and are using a noop (`:`) instruction instead of
  # `sleep`. We do this to ensure that we keep the startup as fast as possible.
  if ! nice -n 19 timeout -k 70 60 -- bash -c "(until sudo ifconfig docker0 mtu '$MTU' up; do :; done) 1>&- 2>&-"; then
    # shellcheck source=runner/logger.bash
    source logger.bash
    log.error 'Failed to set docker interface mtu within 1 minute, continuing bootstrapping without it...'
  fi
fi

# We leave the wait for docker to come up to our entrypoint.sh script, since its
# check properly handles the case where the docker service is in a crash loop.
# Users still have the ability to disable the wait.
export DOCKER_ENABLED=true
export DISABLE_WAIT_FOR_DOCKER=${DISABLE_WAIT_FOR_DOCKER:-false}
exec entrypoint.sh
