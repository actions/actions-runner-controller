DOCKER_USER ?= summerwind
DOCKER ?= docker
DEFAULT_RUNNER_NAME ?= ${DOCKER_USER}/actions-runner
DIND_RUNNER_NAME ?= ${DOCKER_USER}/actions-runner-dind
DIND_ROOTLESS_RUNNER_NAME ?= ${DOCKER_USER}/actions-runner-dind-rootless
OS_IMAGE ?= ubuntu-22.04
TARGETPLATFORM ?= $(shell arch)

RUNNER_VERSION ?= 2.304.0 
RUNNER_CONTAINER_HOOKS_VERSION ?= 0.2.0
DOCKER_VERSION ?= 20.10.23

# default list of platforms for which multiarch image is built
ifeq (${PLATFORMS}, )
	export PLATFORMS="linux/amd64,linux/arm64"
endif

# if IMG_RESULT is unspecified, by default the image will be pushed to registry
ifeq (${IMG_RESULT}, load)
	export PUSH_ARG="--load"
    # if load is specified, image will be built only for the build machine architecture.
    export PLATFORMS="local"
else ifeq (${IMG_RESULT}, cache)
	# if cache is specified, image will only be available in the build cache, it won't be pushed or loaded
	# therefore no PUSH_ARG will be specified
else
	export PUSH_ARG="--push"
endif

check-target-platform:
# Handle target platform variants.
# arch command on OS X reports "i386" for Intel CPUs regardless of bitness
ifeq ($(TARGETPLATFORM), $(filter $(TARGETPLATFORM), x86_64 x64 amd64 i386 linux/amd64))
  TARGETPLATFORM = linux/amd64
else ifeq ($(TARGETPLATFORM), $(filter $(TARGETPLATFORM), arm64 aarch64 linux/arm64))
  TARGETPLATFORM = linux/arm64
else
  $(warning Unsupported target platform $(TARGETPLATFORM))
  $(error Supported target platforms: linux/amd64 and linux/arm64)
endif

docker-build-set: check-target-platform
	${DOCKER} build \
	  --build-arg TARGETPLATFORM=${TARGETPLATFORM} \
	  --build-arg RUNNER_VERSION=${RUNNER_VERSION} \
	  --build-arg RUNNER_CONTAINER_HOOKS_VERSION=${RUNNER_CONTAINER_HOOKS_VERSION} \
	  --build-arg DOCKER_VERSION=${DOCKER_VERSION} \
	  -f actions-runner.${OS_IMAGE}.dockerfile \
	  -t ${DEFAULT_RUNNER_NAME}:${OS_IMAGE} .
	${DOCKER} build \
	  --build-arg TARGETPLATFORM=${TARGETPLATFORM} \
	  --build-arg RUNNER_VERSION=${RUNNER_VERSION} \
	  --build-arg RUNNER_CONTAINER_HOOKS_VERSION=${RUNNER_CONTAINER_HOOKS_VERSION} \
	  --build-arg DOCKER_VERSION=${DOCKER_VERSION} \
	  -f actions-runner-dind.${OS_IMAGE}.dockerfile \
	  -t ${DIND_RUNNER_NAME}:${OS_IMAGE} .
	${DOCKER} build \
	  --build-arg TARGETPLATFORM=${TARGETPLATFORM} \
	  --build-arg RUNNER_VERSION=${RUNNER_VERSION} \
	  --build-arg DOCKER_VERSION=${DOCKER_VERSION} \
	  -f actions-runner-dind-rootless.${OS_IMAGE}.dockerfile \
	  -t "${DIND_ROOTLESS_RUNNER_NAME}:${OS_IMAGE}" .

docker-build-default: check-target-platform
	${DOCKER} build \
	  --build-arg TARGETPLATFORM=${TARGETPLATFORM} \
	  --build-arg RUNNER_VERSION=${RUNNER_VERSION} \
	  --build-arg RUNNER_CONTAINER_HOOKS_VERSION=${RUNNER_CONTAINER_HOOKS_VERSION} \
	  --build-arg DOCKER_VERSION=${DOCKER_VERSION} \
	  -f actions-runner.${OS_IMAGE}.dockerfile \
	  -t ${DEFAULT_RUNNER_NAME}:${OS_IMAGE} .

docker-build-dind: check-target-platform
	${DOCKER} build \
	  --build-arg TARGETPLATFORM=${TARGETPLATFORM} \
	  --build-arg RUNNER_VERSION=${RUNNER_VERSION} \
	  --build-arg RUNNER_CONTAINER_HOOKS_VERSION=${RUNNER_CONTAINER_HOOKS_VERSION} \
	  --build-arg DOCKER_VERSION=${DOCKER_VERSION} \
	  -f actions-runner-dind.${OS_IMAGE}.dockerfile \
	  -t ${DIND_RUNNER_NAME}:${OS_IMAGE} .

docker-push-default:
	${DOCKER} push "${DEFAULT_RUNNER_NAME}:${OS_IMAGE}"

docker-push-dind:
	${DOCKER} push "${DIND_RUNNER_NAME}:${OS_IMAGE}"

docker-push-set:
	${DOCKER} push "${DEFAULT_RUNNER_NAME}:${OS_IMAGE}"
	${DOCKER} push "${DIND_RUNNER_NAME}:${OS_IMAGE}"
	${DOCKER} push "${DIND_ROOTLESS_RUNNER_NAME}:${OS_IMAGE}"

docker-buildx-set:
	export DOCKER_CLI_EXPERIMENTAL=enabled ;\
    export DOCKER_BUILDKIT=1
	@if ! docker buildx ls | grep -q container-builder; then\
	  docker buildx create --platform ${PLATFORMS} --name container-builder --use;\
	fi
	${DOCKER} buildx build --platform ${PLATFORMS} \
	  --build-arg RUNNER_VERSION=${RUNNER_VERSION} \
	  --build-arg RUNNER_CONTAINER_HOOKS_VERSION=${RUNNER_CONTAINER_HOOKS_VERSION} \
	  --build-arg DOCKER_VERSION=${DOCKER_VERSION} \
	  -f actions-runner.${OS_IMAGE}.dockerfile \
	  -t "${DEFAULT_RUNNER_NAME}:${OS_IMAGE}" \
	  . ${PUSH_ARG}
	${DOCKER} buildx build --platform ${PLATFORMS} \
	  --build-arg RUNNER_VERSION=${RUNNER_VERSION} \
	  --build-arg RUNNER_CONTAINER_HOOKS_VERSION=${RUNNER_CONTAINER_HOOKS_VERSION} \
	  --build-arg DOCKER_VERSION=${DOCKER_VERSION} \
	  -f actions-runner-dind.${OS_IMAGE}.dockerfile \
	  -t "${DIND_RUNNER_NAME}:${OS_IMAGE}" \
	  . ${PUSH_ARG}
	${DOCKER} buildx build --platform ${PLATFORMS} \
	  --build-arg RUNNER_VERSION=${RUNNER_VERSION} \
	  --build-arg RUNNER_CONTAINER_HOOKS_VERSION=${RUNNER_CONTAINER_HOOKS_VERSION} \
	  --build-arg DOCKER_VERSION=${DOCKER_VERSION} \
	  -f actions-runner-dind-rootless.${OS_IMAGE}.dockerfile \
	  -t "${DIND_ROOTLESS_RUNNER_NAME}:${OS_IMAGE}" \
	  . ${PUSH_ARG}

docker-buildx-default:
	export DOCKER_CLI_EXPERIMENTAL=enabled ;\
    export DOCKER_BUILDKIT=1
	@if ! docker buildx ls | grep -q container-builder; then\
	  docker buildx create --platform ${PLATFORMS} --name container-builder --use;\
	fi
	${DOCKER} buildx build --platform ${PLATFORMS} \
	  --build-arg RUNNER_VERSION=${RUNNER_VERSION} \
	  --build-arg RUNNER_CONTAINER_HOOKS_VERSION=${RUNNER_CONTAINER_HOOKS_VERSION} \
	  --build-arg DOCKER_VERSION=${DOCKER_VERSION} \
	  -f actions-runner.${OS_IMAGE}.dockerfile \
	  -t "${DEFAULT_RUNNER_NAME}:${OS_IMAGE}" \
	  . ${PUSH_ARG}

docker-buildx-dind:
	export DOCKER_CLI_EXPERIMENTAL=enabled ;\
    export DOCKER_BUILDKIT=1
	@if ! docker buildx ls | grep -q container-builder; then\
	  docker buildx create --platform ${PLATFORMS} --name container-builder --use;\
	fi
	${DOCKER} buildx build --platform ${PLATFORMS} \
	  --build-arg RUNNER_VERSION=${RUNNER_VERSION} \
	  --build-arg RUNNER_CONTAINER_HOOKS_VERSION=${RUNNER_CONTAINER_HOOKS_VERSION} \
	  --build-arg DOCKER_VERSION=${DOCKER_VERSION} \
	  -f actions-runner-dind.${OS_IMAGE}.dockerfile \
	  -t "${DIND_RUNNER_NAME}:${OS_IMAGE}" \
	  . ${PUSH_ARG}

docker-buildx-dind-rootless:
	export DOCKER_CLI_EXPERIMENTAL=enabled ;\
    export DOCKER_BUILDKIT=1
	@if ! docker buildx ls | grep -q container-builder; then\
	  docker buildx create --platform ${PLATFORMS} --name container-builder --use;\
	fi
	${DOCKER} buildx build --platform ${PLATFORMS} \
	  --build-arg RUNNER_VERSION=${RUNNER_VERSION} \
	  --build-arg RUNNER_CONTAINER_HOOKS_VERSION=${RUNNER_CONTAINER_HOOKS_VERSION} \
	  --build-arg DOCKER_VERSION=${DOCKER_VERSION} \
	  -f actions-runner-dind-rootless.${OS_IMAGE}.dockerfile \
	  -t "${DIND_ROOTLESS_RUNNER_NAME}:${OS_IMAGE}" \
	  . ${PUSH_ARG}
