ifdef DOCKER_USER
	DOCKER_IMAGE_NAME ?= ${DOCKER_USER}/actions-runner-controller
else
	DOCKER_IMAGE_NAME ?= ghcr.io/actions-runner-controller
endif

DOCKER_USER ?= $(shell echo ${DOCKER_IMAGE_NAME} | cut -d / -f1)

YQ=$(shell )

IMAGE_VERSION ?= dev
BINARY_VERSION ?= $(shell cat ./charts/gha-runner-scale-set-controller/Chart.yaml | yq .version)

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

.PHONY: help
help: ## Display this help.
	@awk 'BEGIN {FS = ":.*##"; printf "\nUsage:\n  make \033[36m<target>\033[0m\n"} /^[a-zA-Z_0-9-]+:.*?##/ { printf "  \033[36m%-15s\033[0m %s\n", $$1, $$2 } /^##@/ { printf "\n\033[1m%s\033[0m\n", substr($$0, 5) } ' $(MAKEFILE_LIST)

.PHONY: fmt
fmt: ## Run go fmt.
	go fmt ./...

.PHONY: vet
vet: ## Run go vet.
	go vet ./...

.PHONY: lint
lint: # Run golangci-lint in docker
	docker run --rm -v $(PWD):/app -w /app golangci/golangci-lint:v1.52.2 golangci-lint run

.PHONY: test-ctrl
test-ctrl: manifests generate fmt vet envtest ## Run test on actions.github.com controllers.
	KUBEBUILDER_ASSETS="$(shell $(ENVTEST) use $(ENVTEST_K8S_VERSION) --bin-dir $(LOCALBIN) -p path)" go test ./controllers/actions.github.com

.PHONY: test-charts
test-charts: ## Run tests on helm charts.
	go test ./charts/gha-runner-scale-set-controller/tests
	go test ./charts/gha-runner-scale-set/tests

.PHONY: test-listener
test-listener: ## Run tests on listener
	go test ./cmd/githubrunnerscalesetlistener

.PHONY: test-chart-versions
test-chart-versions: ## Test the chart versions against an input version
	./hack/check-gh-chart-versions.sh

.PHONY: test
test: test-ctrl test-charts test-listener # Run all tests.

.PHONY: docker-buildx
docker-buildx: fmt vet ## Build a docker image.
	export DOCKER_CLI_EXPERIMENTAL=enabled ;\
	export DOCKER_BUILDKIT=1
	docker buildx build --platform ${PLATFORMS} \
		--build-arg RUNNER_VERSION=${RUNNER_VERSION} \
		--build-arg DOCKER_VERSION=${DOCKER_VERSION} \
		--build-arg VERSION=${BINARY_VERSION} \
		-t "${DOCKER_IMAGE_NAME}:${IMAGE_VERSION}" \
		-f Dockerfile \
		. ${PUSH_ARG}

.PHONY: generate
generate: controller-gen ## Generate code containing DeepCopy, DeepCopyInto, and DeepCopyObject method implementations.
	$(CONTROLLER_GEN) object:headerFile=./hack/boilerplate.go.txt paths="./..."

chart-crds:
	cp config/crd/bases/actions.github.com_* charts/gha-runner-scale-set-controller/crds/
	rm charts/actions-runner-controller/crds/actions.github.com* || true

.PHONY: manifests
manifests: controller-gen ## Generate ClusterRole, CustomResourceDefinition objects.
	$(CONTROLLER_GEN) rbac:roleName=manager-role crd paths="./controllers/actions.github.com" paths="./apis/actions.github.com/..." output:crd:artifacts:config=config/crd/bases


## Tool Binaries

## Location to install dependencies to
LOCALBIN ?= $(shell pwd)/bin
$(LOCALBIN):
	mkdir -p $(LOCALBIN)

KUSTOMIZE ?= $(LOCALBIN)/kustomize
CONTROLLER_GEN ?= $(LOCALBIN)/controller-gen
ENVTEST ?= $(LOCALBIN)/setup-envtest

## Tool Versions
KUSTOMIZE_VERSION ?= v5.0.0
CONTROLLER_TOOLS_VERSION ?= v0.11.3

KUSTOMIZE_INSTALL_SCRIPT ?= "https://raw.githubusercontent.com/kubernetes-sigs/kustomize/master/hack/install_kustomize.sh"
.PHONY: kustomize
kustomize: $(KUSTOMIZE) ## Download kustomize locally if necessary. If wrong version is installed, it will be removed before downloading.
$(KUSTOMIZE): $(LOCALBIN)
	@if test -x $(LOCALBIN)/kustomize && ! $(LOCALBIN)/kustomize version | grep -q $(KUSTOMIZE_VERSION); then \
		echo "$(LOCALBIN)/kustomize version is not expected $(KUSTOMIZE_VERSION). Removing it before installing."; \
		rm -rf $(LOCALBIN)/kustomize; \
	fi
	test -s $(LOCALBIN)/kustomize || { curl -Ss $(KUSTOMIZE_INSTALL_SCRIPT) --output install_kustomize.sh && bash install_kustomize.sh $(subst v,,$(KUSTOMIZE_VERSION)) $(LOCALBIN); rm install_kustomize.sh; }

.PHONY: controller-gen
controller-gen: $(CONTROLLER_GEN) ## Download controller-gen locally if necessary. If wrong version is installed, it will be overwritten.
$(CONTROLLER_GEN): $(LOCALBIN)
	test -s $(LOCALBIN)/controller-gen && $(LOCALBIN)/controller-gen --version | grep -q $(CONTROLLER_TOOLS_VERSION) || \
	GOBIN=$(LOCALBIN) go install sigs.k8s.io/controller-tools/cmd/controller-gen@$(CONTROLLER_TOOLS_VERSION)

.PHONY: envtest
envtest: $(ENVTEST) ## Download envtest-setup locally if necessary.
$(ENVTEST): $(LOCALBIN)
	test -s $(LOCALBIN)/setup-envtest || GOBIN=$(LOCALBIN) go install sigs.k8s.io/controller-runtime/tools/setup-envtest@latest

