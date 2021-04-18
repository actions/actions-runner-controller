NAME ?= summerwind/actions-runner-controller
VERSION ?= latest
# From https://github.com/VictoriaMetrics/operator/pull/44
YAML_DROP=$(YQ) delete --inplace
YAML_DROP_PREFIX=spec.validation.openAPIV3Schema.properties.spec.properties

# Produce CRDs that work back to Kubernetes 1.11 (no version conversion)
CRD_OPTIONS ?= "crd:trivialVersions=true"

# Get the currently used golang install path (in GOPATH/bin, unless GOBIN is set)
ifeq (,$(shell go env GOBIN))
GOBIN=$(shell go env GOPATH)/bin
else
GOBIN=$(shell go env GOBIN)
endif

TEST_ASSETS=$(PWD)/test-assets

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

test-with-deps: kube-apiserver etcd kubectl
	# See https://pkg.go.dev/sigs.k8s.io/controller-runtime/pkg/envtest#pkg-constants
	TEST_ASSET_KUBE_APISERVER=$(KUBE_APISERVER_BIN) \
	TEST_ASSET_ETCD=$(ETCD_BIN) \
	TEST_ASSET_KUBECTL=$(KUBECTL_BIN) \
	  make test

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
manifests: manifests-118 fix118 chart-crds

manifests-118: controller-gen
	$(CONTROLLER_GEN) $(CRD_OPTIONS) rbac:roleName=manager-role webhook paths="./..." output:crd:artifacts:config=config/crd/bases

chart-crds:
	cp config/crd/bases/*.yaml charts/actions-runner-controller/crds/

# Run go fmt against code
fmt:
	go fmt ./...

# Run go vet against code
vet:
	go vet ./...

# workaround for CRD issue with k8s 1.18 & controller-gen
# ref: https://github.com/kubernetes/kubernetes/issues/91395
fix118: yq
	$(YAML_DROP) config/crd/bases/actions.summerwind.dev_runnerreplicasets.yaml $(YAML_DROP_PREFIX).template.properties.spec.properties.containers.items.properties
	$(YAML_DROP) config/crd/bases/actions.summerwind.dev_runnerreplicasets.yaml $(YAML_DROP_PREFIX).template.properties.spec.properties.initContainers.items.properties
	$(YAML_DROP) config/crd/bases/actions.summerwind.dev_runnerreplicasets.yaml $(YAML_DROP_PREFIX).template.properties.spec.properties.sidecarContainers.items.properties
	$(YAML_DROP) config/crd/bases/actions.summerwind.dev_runnerreplicasets.yaml $(YAML_DROP_PREFIX).template.properties.spec.properties.ephemeralContainers.items.properties
	$(YAML_DROP) config/crd/bases/actions.summerwind.dev_runnerdeployments.yaml $(YAML_DROP_PREFIX).template.properties.spec.properties.containers.items.properties
	$(YAML_DROP) config/crd/bases/actions.summerwind.dev_runnerdeployments.yaml $(YAML_DROP_PREFIX).template.properties.spec.properties.initContainers.items.properties
	$(YAML_DROP) config/crd/bases/actions.summerwind.dev_runnerdeployments.yaml $(YAML_DROP_PREFIX).template.properties.spec.properties.sidecarContainers.items.properties
	$(YAML_DROP) config/crd/bases/actions.summerwind.dev_runnerdeployments.yaml $(YAML_DROP_PREFIX).template.properties.spec.properties.ephemeralContainers.items.properties
	$(YAML_DROP) config/crd/bases/actions.summerwind.dev_runners.yaml $(YAML_DROP_PREFIX).containers.items.properties
	$(YAML_DROP) config/crd/bases/actions.summerwind.dev_runners.yaml $(YAML_DROP_PREFIX).initContainers.items.properties
	$(YAML_DROP) config/crd/bases/actions.summerwind.dev_runners.yaml $(YAML_DROP_PREFIX).sidecarContainers.items.properties
	$(YAML_DROP) config/crd/bases/actions.summerwind.dev_runners.yaml $(YAML_DROP_PREFIX).ephemeralContainers.items.properties

# Generate code
generate: controller-gen
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

.PHONY: release/clean
release/clean:
	rm -rf release

.PHONY: acceptance
acceptance: release/clean docker-build release
	make acceptance/pull
	ACCEPTANCE_TEST_SECRET_TYPE=token make acceptance/kind acceptance/setup acceptance/tests acceptance/teardown
	ACCEPTANCE_TEST_SECRET_TYPE=app make acceptance/kind acceptance/setup acceptance/tests acceptance/teardown
	ACCEPTANCE_TEST_DEPLOYMENT_TOOL=helm ACCEPTANCE_TEST_SECRET_TYPE=token make acceptance/kind acceptance/setup acceptance/tests acceptance/teardown
	ACCEPTANCE_TEST_DEPLOYMENT_TOOL=helm ACCEPTANCE_TEST_SECRET_TYPE=app make acceptance/kind acceptance/setup acceptance/tests acceptance/teardown

acceptance/kind:
	kind create cluster --name acceptance
	kind load docker-image ${NAME}:${VERSION} --name acceptance
	kind load docker-image quay.io/brancz/kube-rbac-proxy:v0.8.0 --name acceptance
	kind load docker-image summerwind/actions-runner:latest --name acceptance
	kind load docker-image docker:dind --name acceptance
	kind load docker-image quay.io/jetstack/cert-manager-controller:v1.0.4 --name acceptance
	kind load docker-image quay.io/jetstack/cert-manager-cainjector:v1.0.4 --name acceptance
	kind load docker-image quay.io/jetstack/cert-manager-webhook:v1.0.4 --name acceptance
	kubectl cluster-info --context kind-acceptance

acceptance/pull:
	docker pull quay.io/brancz/kube-rbac-proxy:v0.8.0
	docker pull summerwind/actions-runner:latest
	docker pull docker:dind
	docker pull quay.io/jetstack/cert-manager-controller:v1.0.4
	docker pull quay.io/jetstack/cert-manager-cainjector:v1.0.4
	docker pull quay.io/jetstack/cert-manager-webhook:v1.0.4

acceptance/setup:
	kubectl apply --validate=false -f https://github.com/jetstack/cert-manager/releases/download/v1.0.4/cert-manager.yaml	#kubectl create namespace actions-runner-system
	kubectl -n cert-manager wait deploy/cert-manager-cainjector --for condition=available --timeout 60s
	kubectl -n cert-manager wait deploy/cert-manager-webhook --for condition=available --timeout 60s
	kubectl -n cert-manager wait deploy/cert-manager --for condition=available --timeout 60s
	kubectl create namespace actions-runner-system || true
	# Adhocly wait for some time until cert-manager's admission webhook gets ready
	sleep 5

acceptance/teardown:
	kind delete cluster --name acceptance

acceptance/tests:
	acceptance/deploy.sh
	acceptance/checks.sh

# Upload release file to GitHub.
github-release: release
	ghr ${VERSION} release/

# find or download controller-gen
# download controller-gen if necessary
controller-gen:
ifeq (, $(shell which controller-gen))
ifeq (, $(wildcard $(GOBIN)/controller-gen))
	@{ \
	set -e ;\
	CONTROLLER_GEN_TMP_DIR=$$(mktemp -d) ;\
	cd $$CONTROLLER_GEN_TMP_DIR ;\
	go mod init tmp ;\
	go get sigs.k8s.io/controller-tools/cmd/controller-gen@v0.3.0 ;\
	rm -rf $$CONTROLLER_GEN_TMP_DIR ;\
	}
endif
CONTROLLER_GEN=$(GOBIN)/controller-gen
else
CONTROLLER_GEN=$(shell which controller-gen)
endif

# find or download yq
# download yq if necessary
# Use always go-version to get consistent line wraps etc.
yq:
ifeq (, $(wildcard $(GOBIN)/yq))
	echo "Downloading yq"
	@{ \
	set -e ;\
	YQ_TMP_DIR=$$(mktemp -d) ;\
	cd $$YQ_TMP_DIR ;\
	go mod init tmp ;\
	go get github.com/mikefarah/yq/v3@3.4.0 ;\
	rm -rf $$YQ_TMP_DIR ;\
	}
endif
YQ=$(GOBIN)/yq

OS_NAME := $(shell uname -s | tr A-Z a-z)

# find or download etcd
etcd:
ifeq (, $(wildcard $(TEST_ASSETS)/etcd))
	@{ \
	set -xe ;\
	INSTALL_TMP_DIR=$$(mktemp -d) ;\
	cd $$INSTALL_TMP_DIR ;\
	wget https://github.com/kubernetes-sigs/kubebuilder/releases/download/v2.3.2/kubebuilder_2.3.2_$(OS_NAME)_amd64.tar.gz ;\
	mkdir -p $(TEST_ASSETS) ;\
	tar zxvf kubebuilder_2.3.2_$(OS_NAME)_amd64.tar.gz ;\
	mv kubebuilder_2.3.2_$(OS_NAME)_amd64/bin/etcd $(TEST_ASSETS)/etcd ;\
	mv kubebuilder_2.3.2_$(OS_NAME)_amd64/bin/kube-apiserver $(TEST_ASSETS)/kube-apiserver ;\
	mv kubebuilder_2.3.2_$(OS_NAME)_amd64/bin/kubectl $(TEST_ASSETS)/kubectl ;\
	rm -rf $$INSTALL_TMP_DIR ;\
	}
ETCD_BIN=$(TEST_ASSETS)/etcd
else
ETCD_BIN=$(TEST_ASSETS)/etcd
endif

# find or download kube-apiserver
kube-apiserver:
ifeq (, $(wildcard $(TEST_ASSETS)/kube-apiserver))
	@{ \
	set -xe ;\
	INSTALL_TMP_DIR=$$(mktemp -d) ;\
	cd $$INSTALL_TMP_DIR ;\
	wget https://github.com/kubernetes-sigs/kubebuilder/releases/download/v2.3.2/kubebuilder_2.3.2_$(OS_NAME)_amd64.tar.gz ;\
	mkdir -p $(TEST_ASSETS) ;\
	tar zxvf kubebuilder_2.3.2_$(OS_NAME)_amd64.tar.gz ;\
	mv kubebuilder_2.3.2_$(OS_NAME)_amd64/bin/etcd $(TEST_ASSETS)/etcd ;\
	mv kubebuilder_2.3.2_$(OS_NAME)_amd64/bin/kube-apiserver $(TEST_ASSETS)/kube-apiserver ;\
	mv kubebuilder_2.3.2_$(OS_NAME)_amd64/bin/kubectl $(TEST_ASSETS)/kubectl ;\
	rm -rf $$INSTALL_TMP_DIR ;\
	}
KUBE_APISERVER_BIN=$(TEST_ASSETS)/kube-apiserver
else
KUBE_APISERVER_BIN=$(TEST_ASSETS)/kube-apiserver
endif


# find or download kubectl
kubectl:
ifeq (, $(wildcard $(TEST_ASSETS)/kubectl))
	@{ \
	set -xe ;\
	INSTALL_TMP_DIR=$$(mktemp -d) ;\
	cd $$INSTALL_TMP_DIR ;\
	wget https://github.com/kubernetes-sigs/kubebuilder/releases/download/v2.3.2/kubebuilder_2.3.2_$(OS_NAME)_amd64.tar.gz ;\
	mkdir -p $(TEST_ASSETS) ;\
	tar zxvf kubebuilder_2.3.2_$(OS_NAME)_amd64.tar.gz ;\
	mv kubebuilder_2.3.2_$(OS_NAME)_amd64/bin/etcd $(TEST_ASSETS)/etcd ;\
	mv kubebuilder_2.3.2_$(OS_NAME)_amd64/bin/kube-apiserver $(TEST_ASSETS)/kube-apiserver ;\
	mv kubebuilder_2.3.2_$(OS_NAME)_amd64/bin/kubectl $(TEST_ASSETS)/kubectl ;\
	rm -rf $$INSTALL_TMP_DIR ;\
	}
KUBECTL_BIN=$(TEST_ASSETS)/kubectl
else
KUBECTL_BIN=$(TEST_ASSETS)/kubectl
endif
