ifdef DOCKER_USER
	NAME ?= ${DOCKER_USER}/actions-runner-controller
else
	NAME ?= summerwind/actions-runner-controller
endif
DOCKER_USER ?= $(shell echo ${NAME} | cut -d / -f1)
VERSION ?= latest
RUNNER_VERSION ?= 2.293.0
TARGETPLATFORM ?= $(shell arch)
RUNNER_NAME ?= ${DOCKER_USER}/actions-runner
RUNNER_TAG  ?= ${VERSION}
TEST_REPO ?= ${DOCKER_USER}/actions-runner-controller
TEST_ORG ?=
TEST_ORG_REPO ?=
TEST_EPHEMERAL ?= false
SYNC_PERIOD ?= 1m
USE_RUNNERSET ?=
KUBECONTEXT ?= kind-acceptance
CLUSTER ?= acceptance
CERT_MANAGER_VERSION ?= v1.1.1
KUBE_RBAC_PROXY_VERSION ?= v0.11.0

# Produce CRDs that work back to Kubernetes 1.11 (no version conversion)
CRD_OPTIONS ?= "crd:generateEmbeddedObjectMeta=true"

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

GO_TEST_ARGS ?= -short

# Run tests
test: generate fmt vet manifests
	go test $(GO_TEST_ARGS) ./... -coverprofile cover.out
	go test -fuzz=Fuzz -fuzztime=10s -run=Fuzz* ./controllers

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
manifests: manifests-gen-crds chart-crds

manifests-gen-crds: controller-gen yq
	$(CONTROLLER_GEN) $(CRD_OPTIONS) rbac:roleName=manager-role webhook paths="./..." output:crd:artifacts:config=config/crd/bases
	for YAMLFILE in config/crd/bases/actions*.yaml; do \
		$(YQ) write --inplace "$$YAMLFILE" spec.preserveUnknownFields false; \
	done

chart-crds:
	cp config/crd/bases/*.yaml charts/actions-runner-controller/crds/

# Run go fmt against code
fmt:
	go fmt ./...

# Run go vet against code
vet:
	go vet ./...

# Generate code
generate: controller-gen
	$(CONTROLLER_GEN) object:headerFile=./hack/boilerplate.go.txt paths="./..."

docker-buildx:
	export DOCKER_CLI_EXPERIMENTAL=enabled ;\
	export DOCKER_BUILDKIT=1
	@if ! docker buildx ls | grep -q container-builder; then\
		docker buildx create --platform ${PLATFORMS} --name container-builder --use;\
	fi
	docker buildx build --platform ${PLATFORMS} \
		--build-arg RUNNER_VERSION=${RUNNER_VERSION} \
		--build-arg DOCKER_VERSION=${DOCKER_VERSION} \
		-t "${NAME}:${VERSION}" \
		-f Dockerfile \
		. ${PUSH_ARG}

# Push the docker image
docker-push:
	docker push ${NAME}:${VERSION}
	docker push ${RUNNER_NAME}:${RUNNER_TAG}

# Generate the release manifest file
release: manifests
	cd config/manager && kustomize edit set image controller=${NAME}:${VERSION}
	mkdir -p release
	kustomize build config/default > release/actions-runner-controller.yaml

.PHONY: release/clean
release/clean:
	rm -rf release

.PHONY: acceptance
acceptance: release/clean acceptance/pull docker-build release
	ACCEPTANCE_TEST_SECRET_TYPE=token make acceptance/run
	ACCEPTANCE_TEST_SECRET_TYPE=app make acceptance/run
	ACCEPTANCE_TEST_DEPLOYMENT_TOOL=helm ACCEPTANCE_TEST_SECRET_TYPE=token make acceptance/run
	ACCEPTANCE_TEST_DEPLOYMENT_TOOL=helm ACCEPTANCE_TEST_SECRET_TYPE=app make acceptance/run

acceptance/run: acceptance/kind acceptance/load acceptance/setup acceptance/deploy acceptance/tests acceptance/teardown

acceptance/kind:
	kind create cluster --name ${CLUSTER} --config acceptance/kind.yaml

# Set TMPDIR to somewhere under $HOME when you use docker installed with Ubuntu snap
# Otherwise `load docker-image` fail while running `docker save`.
# See https://kind.sigs.k8s.io/docs/user/known-issues/#docker-installed-with-snap
acceptance/load:
	kind load docker-image ${NAME}:${VERSION} --name ${CLUSTER}
	kind load docker-image quay.io/brancz/kube-rbac-proxy:$(KUBE_RBAC_PROXY_VERSION) --name ${CLUSTER}
	kind load docker-image ${RUNNER_NAME}:${RUNNER_TAG} --name ${CLUSTER}
	kind load docker-image docker:dind --name ${CLUSTER}
	kind load docker-image quay.io/jetstack/cert-manager-controller:$(CERT_MANAGER_VERSION) --name ${CLUSTER}
	kind load docker-image quay.io/jetstack/cert-manager-cainjector:$(CERT_MANAGER_VERSION) --name ${CLUSTER}
	kind load docker-image quay.io/jetstack/cert-manager-webhook:$(CERT_MANAGER_VERSION) --name ${CLUSTER}
	kubectl cluster-info --context ${KUBECONTEXT}

# Pull the docker images for acceptance
acceptance/pull:
	docker pull quay.io/brancz/kube-rbac-proxy:$(KUBE_RBAC_PROXY_VERSION)
	docker pull docker:dind
	docker pull quay.io/jetstack/cert-manager-controller:$(CERT_MANAGER_VERSION)
	docker pull quay.io/jetstack/cert-manager-cainjector:$(CERT_MANAGER_VERSION)
	docker pull quay.io/jetstack/cert-manager-webhook:$(CERT_MANAGER_VERSION)

acceptance/setup:
	kubectl apply --validate=false -f https://github.com/jetstack/cert-manager/releases/download/$(CERT_MANAGER_VERSION)/cert-manager.yaml	#kubectl create namespace actions-runner-system
	kubectl -n cert-manager wait deploy/cert-manager-cainjector --for condition=available --timeout 90s
	kubectl -n cert-manager wait deploy/cert-manager-webhook --for condition=available --timeout 60s
	kubectl -n cert-manager wait deploy/cert-manager --for condition=available --timeout 60s
	kubectl create namespace actions-runner-system || true
	# Adhocly wait for some time until cert-manager's admission webhook gets ready
	sleep 5

acceptance/teardown:
	kind delete cluster --name ${CLUSTER}

acceptance/deploy:
	NAME=${NAME} DOCKER_USER=${DOCKER_USER} VERSION=${VERSION} RUNNER_NAME=${RUNNER_NAME} RUNNER_TAG=${RUNNER_TAG} TEST_REPO=${TEST_REPO} \
	TEST_ORG=${TEST_ORG} TEST_ORG_REPO=${TEST_ORG_REPO} SYNC_PERIOD=${SYNC_PERIOD} \
	USE_RUNNERSET=${USE_RUNNERSET} \
	TEST_EPHEMERAL=${TEST_EPHEMERAL} \
	acceptance/deploy.sh

acceptance/tests:
	acceptance/checks.sh

acceptance/runner/entrypoint:
	cd test/entrypoint/ && bash test.sh

# We use -count=1 instead of `go clean -testcache`
# See https://terratest.gruntwork.io/docs/testing-best-practices/avoid-test-caching/
.PHONY: e2e
e2e:
	go test -count=1 -v -timeout 600s -run '^TestE2E$$' ./test/e2e

# Upload release file to GitHub.
github-release: release
	ghr ${VERSION} release/

# Find or download controller-gen
#
# Note that controller-gen newer than 0.4.1 is needed for https://github.com/kubernetes-sigs/controller-tools/issues/444#issuecomment-680168439
# Otherwise we get errors like the below:
#   Error: failed to install CRD crds/actions.summerwind.dev_runnersets.yaml: CustomResourceDefinition.apiextensions.k8s.io "runnersets.actions.summerwind.dev" is invalid: [spec.validation.openAPIV3Schema.properties[spec].properties[template].properties[spec].properties[containers].items.properties[ports].items.properties[protocol].default: Required value: this property is in x-kubernetes-list-map-keys, so it must have a default or be a required property, spec.validation.openAPIV3Schema.properties[spec].properties[template].properties[spec].properties[initContainers].items.properties[ports].items.properties[protocol].default: Required value: this property is in x-kubernetes-list-map-keys, so it must have a default or be a required property]
#
# Note that controller-gen newer than 0.6.0 is needed due to https://github.com/kubernetes-sigs/controller-tools/issues/448
# Otherwise ObjectMeta embedded in Spec results in empty on the storage.
controller-gen:
ifeq (, $(shell which controller-gen))
ifeq (, $(wildcard $(GOBIN)/controller-gen))
	@{ \
	set -e ;\
	CONTROLLER_GEN_TMP_DIR=$$(mktemp -d) ;\
	cd $$CONTROLLER_GEN_TMP_DIR ;\
	go mod init tmp ;\
	go install sigs.k8s.io/controller-tools/cmd/controller-gen@v0.7.0 ;\
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
	go install github.com/mikefarah/yq/v3@3.4.0 ;\
	rm -rf $$YQ_TMP_DIR ;\
	}
endif
YQ=$(GOBIN)/yq

OS_NAME := $(shell uname -s | tr A-Z a-z)

# find or download etcd
etcd:
ifeq (, $(shell which etcd))
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
else
ETCD_BIN=$(shell which etcd)
endif

# find or download kube-apiserver
kube-apiserver:
ifeq (, $(shell which kube-apiserver))
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
else
KUBE_APISERVER_BIN=$(shell which kube-apiserver)
endif

# find or download kubectl
kubectl:
ifeq (, $(shell which kubectl))
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
else
KUBECTL_BIN=$(shell which kubectl)
endif
