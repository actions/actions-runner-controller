DOCKER_USER ?= summerwind
DOCKER ?= docker
NAME ?= ${DOCKER_USER}/actions-runner
DIND_RUNNER_NAME ?= ${DOCKER_USER}/actions-runner-dind
TAG ?= latest
TARGETPLATFORM ?= $(shell arch)

RUNNER_VERSION ?= 2.298.2
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

docker-build-ubuntu:
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

docker-push-ubuntu:
	${DOCKER} push ${NAME}:${TAG}
	${DOCKER} push ${DIND_RUNNER_NAME}:${TAG}

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
