group default {
  targets = ["actions-runner-dind-ubuntu-22-04"]
}

variable TAG_SUFFIX { default = "latest" }
variable RUNNER_VERSION { default = "2.323.0" }
variable RUNNER_CONTAINER_HOOKS_VERSION { default = "0.6.2" }
variable DOCKER_VERSION { default = "24.0.7" }

target actions-runner-dind-ubuntu-22-04 {
  context     = "runner/"
  contexts = {
    "ubuntu:18.04" = "docker-image://registry.smtx.io/sdn-base/ubuntu:18.04"
    "ubuntu:20.04" = "docker-image://registry.smtx.io/sdn-base/ubuntu:20.04"
    "ubuntu:22.04" = "docker-image://registry.smtx.io/sdn-base/ubuntu:22.04"
    "ubuntu:24.04" = "docker-image://registry.smtx.io/sdn-base/ubuntu:24.04"
  }
  dockerfile = "actions-runner-dind.ubuntu-22.04.dockerfile"
  args = {
    TARGETPLATFORM                 = "linux/amd64"
    RUNNER_VERSION                 = RUNNER_VERSION
    RUNNER_CONTAINER_HOOKS_VERSION = RUNNER_CONTAINER_HOOKS_VERSION
    DOCKER_VERSION                 = DOCKER_VERSION
  }
  tags      = ["registry.smtx.io/everoute/summerwind/actions-runner-dind:ubuntu-22.04-buildx-${TAG_SUFFIX}"]
  platforms = ["linux/amd64"]
  output    = ["type=registry"]
}
