NAME ?= summerwind/actions-runner

RUNNER_VERSION ?= 2.169.1

docker-build:
	docker build --build-arg RUNNER_VERSION=${RUNNER_VERSION} --build-arg DOCKER_VERSION=${DOCKER_VERSION} -t ${NAME}:latest-systemd -t ${NAME}:v${RUNNER_VERSION}-systemd .

docker-push:
	docker push ${NAME}:latest-systemd
	docker push ${NAME}:v${RUNNER_VERSION}-systemd
