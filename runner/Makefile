NAME ?= summerwind/actions-runner
DIND_RUNNER_NAME ?= ${NAME}-dind
TAG ?= latest

RUNNER_VERSION ?= 2.280.3
DOCKER_VERSION ?= 20.10.8

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
	docker build --build-arg TARGETPLATFORM=$(shell arch) --build-arg RUNNER_VERSION=${RUNNER_VERSION} --build-arg DOCKER_VERSION=${DOCKER_VERSION} -t ${NAME}:${TAG} .
	docker build --build-arg TARGETPLATFORM=$(shell arch) --build-arg RUNNER_VERSION=${RUNNER_VERSION} --build-arg DOCKER_VERSION=${DOCKER_VERSION} -t ${DIND_RUNNER_NAME}:${TAG} -f Dockerfile.dindrunner .

docker-push-ubuntu:
	docker push ${NAME}:${TAG}
	docker push ${DIND_RUNNER_NAME}:${TAG}

docker-buildx-ubuntu:
	export DOCKER_CLI_EXPERIMENTAL=enabled
	@if ! docker buildx ls | grep -q container-builder; then\
		docker buildx create --platform ${PLATFORMS} --name container-builder --use;\
	fi
	docker buildx build --platform ${PLATFORMS} \
		--build-arg RUNNER_VERSION=${RUNNER_VERSION} \
		--build-arg DOCKER_VERSION=${DOCKER_VERSION} \
		-t "${NAME}:latest" \
		-f Dockerfile \
		. ${PUSH_ARG}
	docker buildx build --platform ${PLATFORMS} \
		--build-arg RUNNER_VERSION=${RUNNER_VERSION} \
		--build-arg DOCKER_VERSION=${DOCKER_VERSION} \
		-t "${DIND_RUNNER_NAME}:latest" \
		-f Dockerfile.dindrunner \
		. ${PUSH_ARG}
