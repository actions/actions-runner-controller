DOCKER_USER ?= summerwind
DOCKER ?= docker
NAME ?= ${DOCKER_USER}/actions-runner
DIND_RUNNER_NAME ?= ${DOCKER_USER}/actions-runner-dind
DIND_ROOTLESS_RUNNER_NAME ?= ${DOCKER_USER}/actions-runner-dind-rootless
TAG ?= latest
TARGETPLATFORM ?= $(shell arch)

RUNNER_VERSION ?= 2.299.1
RUNNER_CONTAINER_HOOKS_VERSION ?= 0.1.2
DOCKER_VERSION ?= 20.10.18

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

docker-build-ubuntu: check-target-platform
	${DOCKER} build \
	  --build-arg TARGETPLATFORM=${TARGETPLATFORM} \
	  --build-arg RUNNER_VERSION=${RUNNER_VERSION} \
	  --build-arg RUNNER_CONTAINER_HOOKS_VERSION=${RUNNER_CONTAINER_HOOKS_VERSION} \
	  --build-arg DOCKER_VERSION=${DOCKER_VERSION} \
	  -f actions-runner.dockerfile \
	  -t ${NAME}:${TAG} .
	${DOCKER} build \
	  --build-arg TARGETPLATFORM=${TARGETPLATFORM} \
	  --build-arg RUNNER_VERSION=${RUNNER_VERSION} \
	  --build-arg DOCKER_VERSION=${DOCKER_VERSION} \
	  -f actions-runner-dind.dockerfile \
	  -t ${DIND_RUNNER_NAME}:${TAG} .
	${DOCKER} build \
	  --build-arg TARGETPLATFORM=${TARGETPLATFORM} \
	  --build-arg RUNNER_VERSION=${RUNNER_VERSION} \
	  --build-arg DOCKER_VERSION=${DOCKER_VERSION} \
	  -f actions-runner-dind-rootless.dockerfile \
	  -t ${DIND_ROOTLESS_RUNNER_NAME}:${TAG} .

docker-push-ubuntu:
	${DOCKER} push ${NAME}:${TAG}
	${DOCKER} push ${DIND_RUNNER_NAME}:${TAG}
	${DOCKER} push ${DIND_ROOTLESS_RUNNER_NAME}:${TAG}

docker-buildx-ubuntu:
	export DOCKER_CLI_EXPERIMENTAL=enabled ;\
    export DOCKER_BUILDKIT=1
	@if ! docker buildx ls | grep -q container-builder; then\
	  docker buildx create --platform ${PLATFORMS} --name container-builder --use;\
	fi
	${DOCKER} buildx build --platform ${PLATFORMS} \
	  --build-arg RUNNER_VERSION=${RUNNER_VERSION} \
	  --build-arg RUNNER_CONTAINER_HOOKS_VERSION=${RUNNER_CONTAINER_HOOKS_VERSION} \
	  --build-arg DOCKER_VERSION=${DOCKER_VERSION} \
	  -f actions-runner.dockerfile \
	  -t "${NAME}:${TAG}" \
	  . ${PUSH_ARG}
	${DOCKER} buildx build --platform ${PLATFORMS} \
	  --build-arg RUNNER_VERSION=${RUNNER_VERSION} \
	  --build-arg RUNNER_CONTAINER_HOOKS_VERSION=${RUNNER_CONTAINER_HOOKS_VERSION} \
	  --build-arg DOCKER_VERSION=${DOCKER_VERSION} \
	  -f actions-runner-dind.dockerfile \
	  -t "${DIND_RUNNER_NAME}:${TAG}" \
	  . ${PUSH_ARG}
	${DOCKER} buildx build --platform ${PLATFORMS} \
	  --build-arg RUNNER_VERSION=${RUNNER_VERSION} \
	  --build-arg DOCKER_VERSION=${DOCKER_VERSION} \
	  -f actions-runner-dind-rootless.dockerfile \
	  -t "${DIND_ROOTLESS_RUNNER_NAME}:${TAG}" \
	  . ${PUSH_ARG}
