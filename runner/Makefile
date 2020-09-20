NAME ?= summerwind/actions-runner

RUNNER_VERSION ?= 2.273.4
DOCKER_VERSION ?= 19.03.12

docker-build:
	docker build --build-arg RUNNER_VERSION=${RUNNER_VERSION} --build-arg DOCKER_VERSION=${DOCKER_VERSION} -t ${NAME}:latest -t ${NAME}:v${RUNNER_VERSION} .

docker-push:
	docker push ${NAME}:latest
	docker push ${NAME}:v${RUNNER_VERSION}
