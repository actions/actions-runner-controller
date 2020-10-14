NAME ?= summerwind/actions-runner-controller
VERSION ?= latest
# From https://github.com/VictoriaMetrics/operator/pull/44
YAML_DROP=$(YQ) delete --inplace
YAML_DROP_PREFIX=spec.validation.openAPIV3Schema.properties.spec.properties.template.properties.spec.properties

# Produce CRDs that work back to Kubernetes 1.11 (no version conversion)
CRD_OPTIONS ?= "crd:trivialVersions=true"

# Get the currently used golang install path (in GOPATH/bin, unless GOBIN is set)
ifeq (,$(shell go env GOBIN))
GOBIN=$(shell go env GOPATH)/bin
else
GOBIN=$(shell go env GOBIN)
endif

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

all: manager

# Run tests
test: generate fmt vet manifests
	go test ./... -coverprofile cover.out

# Build manager binary
manager: generate fmt vet
	go build -o bin/manager main.go

# Run against the configured Kubernetes cluster in ~/.kube/config
run: generate fmt vet manifests
	go run ./main.go

# Install CRDs into a cluster
install: manifests
	kustomize build config/crd | kubectl apply -f -

# Uninstall CRDs from a cluster
uninstall: manifests
	kustomize build config/crd | kubectl delete -f -

# Deploy controller in the configured Kubernetes cluster in ~/.kube/config
deploy: manifests
	cd config/manager && kustomize edit set image controller=${NAME}:${VERSION}
	kustomize build config/default | kubectl apply -f -

# Generate manifests e.g. CRD, RBAC etc.
manifests: manifests-118 fix118

manifests-118: controller-gen
	$(CONTROLLER_GEN) $(CRD_OPTIONS) rbac:roleName=manager-role webhook paths="./..." output:crd:artifacts:config=config/crd/bases

# Run go fmt against code
fmt:
	go fmt ./...

# Run go vet against code
vet:
	go vet ./...

# workaround for CRD issue with k8s 1.18 & controller-gen
# ref: https://github.com/kubernetes/kubernetes/issues/91395
fix118: yq
	$(YAML_DROP) config/crd/bases/actions.summerwind.dev_runnerreplicasets.yaml $(YAML_DROP_PREFIX).containers.items.properties
	$(YAML_DROP) config/crd/bases/actions.summerwind.dev_runnerreplicasets.yaml $(YAML_DROP_PREFIX).initContainers.items.properties
	$(YAML_DROP) config/crd/bases/actions.summerwind.dev_runnerreplicasets.yaml $(YAML_DROP_PREFIX).sidecarContainers.items.properties
	$(YAML_DROP) config/crd/bases/actions.summerwind.dev_runnerreplicasets.yaml $(YAML_DROP_PREFIX).ephemeralContainers.items.properties
	$(YAML_DROP) config/crd/bases/actions.summerwind.dev_runnerdeployments.yaml $(YAML_DROP_PREFIX).containers.items.properties
	$(YAML_DROP) config/crd/bases/actions.summerwind.dev_runnerdeployments.yaml $(YAML_DROP_PREFIX).initContainers.items.properties
	$(YAML_DROP) config/crd/bases/actions.summerwind.dev_runnerdeployments.yaml $(YAML_DROP_PREFIX).sidecarContainers.items.properties
	$(YAML_DROP) config/crd/bases/actions.summerwind.dev_runnerdeployments.yaml $(YAML_DROP_PREFIX).ephemeralContainers.items.properties

# Generate code
generate: generate-118 fix118

generate-118: controller-gen
	$(CONTROLLER_GEN) object:headerFile=./hack/boilerplate.go.txt paths="./..."

# Build the docker image
docker-build: test
	docker build . -t ${NAME}:${VERSION}

# Push the docker image
docker-push:
	docker push ${NAME}:${VERSION}

docker-buildx:
	export DOCKER_CLI_EXPERIMENTAL=enabled
	@if ! docker buildx ls | grep -q container-builder; then\
		docker buildx create --platform ${PLATFORMS} --name container-builder --use;\
	fi
	docker buildx build --platform ${PLATFORMS} \
		--build-arg RUNNER_VERSION=${RUNNER_VERSION} \
		--build-arg DOCKER_VERSION=${DOCKER_VERSION} \
		-t "${NAME}:${VERSION}" \
		-f Dockerfile \
		. ${PUSH_ARG}

# Generate the release manifest file
release: manifests
	cd config/manager && kustomize edit set image controller=${NAME}:${VERSION}
	mkdir -p release
	kustomize build config/default > release/actions-runner-controller.yaml

# Upload release file to GitHub.
github-release: release
	ghr ${VERSION} release/

# find or download controller-gen
# download controller-gen if necessary
controller-gen:
ifeq (, $(shell which controller-gen))
	@{ \
	set -e ;\
	CONTROLLER_GEN_TMP_DIR=$$(mktemp -d) ;\
	cd $$CONTROLLER_GEN_TMP_DIR ;\
	go mod init tmp ;\
	go get sigs.k8s.io/controller-tools/cmd/controller-gen@v0.3.0 ;\
	rm -rf $$CONTROLLER_GEN_TMP_DIR ;\
	}
CONTROLLER_GEN=$(GOBIN)/controller-gen
else
CONTROLLER_GEN=$(shell which controller-gen)
endif

# find or download yq
# download yq if necessary
yq:
ifeq (, $(shell which yq))
	echo "Downloading yq"
	@{ \
	set -e ;\
	YQ_TMP_DIR=$$(mktemp -d) ;\
	cd $$YQ_TMP_DIR ;\
	go mod init tmp ;\
	go get github.com/mikefarah/yq/v3 ;\
	rm -rf $$YQ_TMP_DIR ;\
	}
YQ=$(GOBIN)/yq
else
YQ=$(shell which yq)
endif
