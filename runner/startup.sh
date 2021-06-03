#!/bin/bash
source /opt/bash-utils/logger.sh

function wait_for_process () {
    local max_time_wait=30
    local process_name="$1"
    local waited_sec=0
    while ! pgrep "$process_name" >/dev/null && ((waited_sec < max_time_wait)); do
        INFO "Process $process_name is not running yet. Retrying in 1 seconds"
        INFO "Waited $waited_sec seconds of $max_time_wait seconds"
        sleep 1
        ((waited_sec=waited_sec+1))
        if ((waited_sec >= max_time_wait)); then
            return 1
        fi
    done
    return 0
}

sudo /bin/bash <<SCRIPT
mkdir -p /etc/docker

echo "{}" > /etc/docker/daemon.json

if [ -n "${MTU}" ]; then
jq ".\"mtu\" = ${MTU}" /etc/docker/daemon.json > /tmp/.daemon.json && mv /tmp/.daemon.json /etc/docker/daemon.json
# See https://docs.docker.com/engine/security/rootless/
echo "environment=DOCKERD_ROOTLESS_ROOTLESSKIT_MTU=${MTU}" >> /etc/supervisor/conf.d/dockerd.conf
fi

if [ -n "${DOCKER_REGISTRY_MIRROR}" ]; then
jq ".\"registry-mirrors\"[0] = \"${DOCKER_REGISTRY_MIRROR}\"" /etc/docker/daemon.json > /tmp/.daemon.json && mv /tmp/.daemon.json /etc/docker/daemon.json
fi
SCRIPT

INFO "Using /etc/docker/daemon.json with the following content"

cat /etc/docker/daemon.json

INFO "Using /etc/supervisor/conf.d/dockerd.conf with the following content"

cat /etc/supervisor/conf.d/dockerd.conf

INFO "Starting supervisor"
sudo /usr/bin/supervisord -n >> /dev/null 2>&1 &

INFO "Waiting for processes to be running"
processes=(dockerd)

for process in "${processes[@]}"; do
    wait_for_process "$process"
    if [ $? -ne 0 ]; then
        ERROR "$process is not running after max time"
        ERROR "Dumping /var/log/dockerd.err.log to help investigation"
        cat /var/log/dockerd.err.log
        exit 1
    else 
        INFO "$process is running"
    fi
done

if [ -n "${MTU}" ]; then
  sudo ifconfig docker0 mtu ${MTU} up
fi

# Wait processes to be running
entrypoint.sh
