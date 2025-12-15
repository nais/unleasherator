
# Image URL to use all building/pushing image targets
IMG ?= ghcr.io/nais/unleasherator:main
# ENVTEST_K8S_VERSION refers to the version of kubebuilder assets to be downloaded by envtest binary.
ENVTEST_K8S_VERSION = 1.33.0

# Get the currently used golang install path (in GOPATH/bin, unless GOBIN is set)
ifeq (,$(shell go env GOBIN))
GOBIN=$(shell go env GOPATH)/bin
else
GOBIN=$(shell go env GOBIN)
endif

# Setting SHELL to bash allows bash commands to be executed by recipes.
# Options are set to exit when a recipe line exits non-zero or a piped command fails.
SHELL = /usr/bin/env bash -o pipefail
.SHELLFLAGS = -ec

.PHONY: all
all: fmt lint vet build generate helm

##@ General

# The help target prints out all targets with their descriptions organized
# beneath their categories. The categories are represented by '##@' and the
# target descriptions by '##'. The awk commands is responsible for reading the
# entire set of makefiles included in this invocation, looking for lines of the
# file as xyz: ## something, and then pretty-format the target and help. Then,
# if there's a line with ##@ something, that gets pretty-printed as a category.
# More info on the usage of ANSI control characters for terminal formatting:
# https://en.wikipedia.org/wiki/ANSI_escape_code#SGR_parameters
# More info on the awk command:
# http://linuxcommand.org/lc3_adv_awk.php

.PHONY: help
help: ## Display this help.
	@awk 'BEGIN {FS = ":.*##"; printf "\nUsage:\n	make \033[36m<target>\033[0m\n"} /^[a-zA-Z_0-9-]+:.*?##/ { printf "	\033[36m%-15s\033[0m %s\n", $$1, $$2 } /^##@/ { printf "\n\033[1m%s\033[0m\n", substr($$0, 5) } ' $(MAKEFILE_LIST)

##@ Development

.PHONY: manifests
manifests: controller-gen ## Generate WebhookConfiguration, ClusterRole and CustomResourceDefinition objects.
	$(CONTROLLER_GEN) rbac:roleName=manager-role crd webhook paths="./..." output:crd:artifacts:config=config/crd/bases

.PHONY: generate
generate: controller-gen ## Generate code containing DeepCopy, DeepCopyInto, and DeepCopyObject method implementations.
	$(CONTROLLER_GEN) object:headerFile="hack/boilerplate.go.txt" paths="./..."

.PHONY: proto
proto: protoc protoc-gen-go ## Generate protobuf files.
	$(PROTOC) --go_opt=paths=source_relative --plugin $(LOCALBIN)/protoc-gen-go --go_out=. internal/pb/unleasherator.proto

.PHONY: fmt
fmt: ## Run go fmt against code.
	go fmt ./...

.PHONY: lint
lint: golangci-lint ## Run golangci-lint against code.
	$(GOLANGCI_LINT) run

.PHONY: vet
vet: ## Run go vet against code.
	go vet ./...

.PHONY: test
test: envtest ## Run tests. Use FOCUS="test name" to run specific tests.
	KUBEBUILDER_ASSETS="$(shell $(ENVTEST) use $(ENVTEST_K8S_VERSION) --bin-dir $(LOCALBIN) -p path)" go test $(if $(FOCUS),./internal/controller -run TestAPIs --ginkgo.focus="$(FOCUS)",./...) -coverprofile cover.out

.PHONY: test-schemas
test-schemas: ## Run schema validation tests to verify types match official Unleash API.
	@echo "=== Running schema validation tests ==="
	go test ./internal/unleashclient -run TestApiToken -v
	@echo ""
	@echo "✅ Schema validation complete"

.PHONY: fetch-schemas
fetch-schemas: ## Fetch OpenAPI schemas from Unleash v5, v6, and v7 using Docker.
	@echo "=== Fetching Unleash OpenAPI schemas ==="
	@./hack/fetch-unleash-schemas.sh
	@echo ""
	@echo "✅ Schemas saved to hack/schemas/"
	@echo "   Run 'make test-schemas' to validate types against these schemas"

.PHONY: build
build: manifests generate proto ## Build manager binary.
	go build -o bin/manager cmd/main.go

.PHONY: run
run: build ## Run a controller from your host.
	go run ./cmd/main.go

.PHONY: helm
helm: manifests kustomize helmify ## Generate Helm chart.
	$(KUSTOMIZE) build config/default | $(HELMIFY) charts/unleasherator
	$(KUSTOMIZE) build config/crd | $(HELMIFY) charts/unleasherator-crds
	rm charts/unleasherator/templates/*-crd.yaml # delete superflous crds from the file tree

##@ End-to-End Testing

# Image tag for e2e tests
E2E_IMG ?= ghcr.io/nais/unleasherator:main
# Namespace for e2e tests
E2E_NAMESPACE ?= unleasherator-e2e-test

.PHONY: test-e2e
test-e2e: test-e2e-setup ## Run end-to-end tests using ct install (same as CI).
	ct install \
		--charts ./charts/unleasherator \
		--namespace $(E2E_NAMESPACE) \
		--helm-extra-args '--timeout 400s'

.PHONY: test-e2e-debug
test-e2e-debug: test-e2e-setup ## Run e2e tests but keep resources for debugging.
	ct install \
		--charts ./charts/unleasherator \
		--namespace $(E2E_NAMESPACE) \
		--helm-extra-args '--timeout 400s' \
		--skip-clean-up \
		--debug

.PHONY: test-e2e-setup
test-e2e-setup: test-e2e-build test-e2e-install-crds ## Set up e2e test environment (build image, install CRDs, create namespace).
	@echo "=== Creating test namespace ==="
	@kubectl create namespace $(E2E_NAMESPACE) --dry-run=client -o yaml | kubectl apply -f -
	@echo ""
	@echo "✅ E2E setup complete"

.PHONY: test-e2e-build
test-e2e-build: ## Build and load Docker image for e2e tests.
	@echo "=== Verifying cluster connectivity ==="
	@kubectl cluster-info || (echo "❌ No cluster available. Start colima or another k8s cluster." && exit 1)
	@echo ""
	@echo "=== Building Docker image (no cache) ==="
	@docker build --no-cache --pull \
		--build-arg TARGETOS=linux \
		--build-arg TARGETARCH=$$(uname -m | sed 's/x86_64/amd64/') \
		-t ${IMG} .
	@echo ""
	@echo "=== Loading Docker image into cluster ==="
	@if command -v colima >/dev/null 2>&1 && colima status >/dev/null 2>&1; then \
		echo "Colima: removing existing images and reloading..."; \
		colima ssh sudo crictl rmi ${IMG} 2>/dev/null || true; \
	elif command -v kind >/dev/null 2>&1; then \
		echo "Kind: loading image..."; \
		kind load docker-image ${IMG} || true; \
	else \
		echo "Using local Docker daemon"; \
	fi

.PHONY: test-e2e-install-crds
test-e2e-install-crds: ## Install required CRDs (prometheus-operator, unleasherator).
	@echo "=== Installing CRDs ==="
	@helm repo add prometheus-community https://prometheus-community.github.io/helm-charts --force-update >/dev/null 2>&1
	@helm repo update >/dev/null 2>&1
	@if ! kubectl get crd alertmanagerconfigs.monitoring.coreos.com >/dev/null 2>&1; then \
		echo "Installing prometheus-operator-crds..."; \
		helm upgrade --install prometheus-operator-crds prometheus-community/prometheus-operator-crds --wait; \
	else \
		echo "✓ Prometheus operator CRDs already installed"; \
	fi
	@if ! kubectl get crd apitokens.unleash.nais.io >/dev/null 2>&1; then \
		echo "Installing unleasherator-crds..."; \
		helm upgrade --install unleasherator-crds ./charts/unleasherator-crds --wait; \
	else \
		echo "✓ Unleasherator CRDs already installed"; \
	fi

.PHONY: test-e2e-clean
test-e2e-clean: ## Clean up e2e test resources.
	@echo "=== Cleaning up $(E2E_NAMESPACE) ==="
	@echo "Removing finalizers..."
	@for crd in unleash apitoken releasechannel remoteunleash; do \
		kubectl get $$crd -n $(E2E_NAMESPACE) -o name 2>/dev/null | xargs -r -I {} kubectl patch {} -n $(E2E_NAMESPACE) --type merge -p '{"metadata":{"finalizers":null}}' 2>/dev/null || true; \
	done
	@echo "Deleting resources..."
	@kubectl delete unleash,apitoken,releasechannel,remoteunleash -n $(E2E_NAMESPACE) --all --timeout=10s --ignore-not-found >/dev/null 2>&1 || true
	@helm uninstall unleasherator -n $(E2E_NAMESPACE) --ignore-not-found >/dev/null 2>&1 || true
	@kubectl delete ns $(E2E_NAMESPACE) --timeout=30s --ignore-not-found >/dev/null 2>&1 || true
	@kubectl patch ns $(E2E_NAMESPACE) --type merge -p '{"metadata":{"finalizers":null}}' >/dev/null 2>&1 || true
	@kubectl delete clusterrole,clusterrolebinding -l app.kubernetes.io/part-of=unleasherator --ignore-not-found >/dev/null 2>&1 || true
	@echo "✅ Cleanup complete"

.PHONY: test-e2e-status
test-e2e-status: ## Show status of e2e test resources.
	@echo "=== E2E Test Status ==="
	@echo ""
	@echo "Namespace:"
	@kubectl get ns $(E2E_NAMESPACE) 2>/dev/null || echo "  Not found"
	@echo ""
	@echo "Pods:"
	@kubectl get pods -n $(E2E_NAMESPACE) 2>/dev/null || echo "  None"
	@echo ""
	@echo "Unleash:"
	@kubectl get unleash -n $(E2E_NAMESPACE) 2>/dev/null || echo "  None"
	@echo ""
	@echo "ReleaseChannel:"
	@kubectl get releasechannel -n $(E2E_NAMESPACE) 2>/dev/null || echo "  None"
	@echo ""
	@echo "ApiToken:"
	@kubectl get apitoken -n $(E2E_NAMESPACE) 2>/dev/null || echo "  None"

# If you wish built the manager image targeting other platforms you can use the --platform flag.
# (i.e. docker build --platform linux/arm64 ). However, you must enable docker buildKit for it.
# More info: https://docs.docker.com/develop/develop-images/build_enhancements/
.PHONY: docker-build
docker-build: ## Build docker image with the manager.
	docker build --pull --build-arg TARGETOS=linux --build-arg TARGETARCH=$$(uname -m | sed 's/x86_64/amd64/') -t ${IMG} .

.PHONY: docker-image
docker-image: ## Echo the docker image name.
	@echo ${IMG}

.PHONY: docker-push
docker-push: ## Push docker image with the manager.
	docker push ${IMG}

# PLATFORMS defines the target platforms for	the manager image be build to provide support to multiple
# architectures. (i.e. make docker-buildx IMG=myregistry/mypoperator:0.0.1). To use this option you need to:
# - able to use docker buildx . More info: https://docs.docker.com/build/buildx/
# - have enable BuildKit, More info: https://docs.docker.com/develop/develop-images/build_enhancements/
# - be able to push the image for your registry (i.e. if you do not inform a valid value via IMG=<myregistry/image:<tag>> than the export will fail)
# To properly provided solutions that supports more than one platform you should use this option.
PLATFORMS ?= linux/arm64,linux/amd64,linux/s390x,linux/ppc64le
.PHONY: docker-buildx
docker-buildx: ## Build and push docker image for the manager for cross-platform support
	# copy existing Dockerfile and insert --platform=${BUILDPLATFORM} into Dockerfile.cross, and preserve the original Dockerfile
	sed -e '1 s/\(^FROM\)/FROM --platform=\$$\{BUILDPLATFORM\}/; t' -e ' 1,// s//FROM --platform=\$$\{BUILDPLATFORM\}/' Dockerfile > Dockerfile.cross
	- docker buildx create --name project-v3-builder
	docker buildx use project-v3-builder
	- docker buildx build --push --platform=$(PLATFORMS) --tag ${IMG} -f Dockerfile.cross
	- docker buildx rm project-v3-builder
	rm Dockerfile.cross

.PHONY: docker-buildx-local
docker-buildx-local: ## Build docker image locally for the current platform without tests
	@echo "Building for platform: $$(uname -m)"
	docker buildx build --load --platform=linux/$$(uname -m) --tag ${IMG} .

.PHONY: docker-inspect
docker-inspect: ## Inspect the architecture of the built docker image
	@echo "Image: ${IMG}"
	@echo "Architecture: $$(docker inspect ${IMG} --format='{{.Architecture}}')"
	@echo "OS: $$(docker inspect ${IMG} --format='{{.Os}}')"
	@echo "Platform: $$(docker inspect ${IMG} --format='{{.Os}}/{{.Architecture}}')"

.PHONY: docker-build-info
docker-build-info: ## Show what build arguments will be used
	@echo "Host architecture: $$(uname -m)"
	@echo "TARGETOS will be: linux"
	@echo "TARGETARCH will be: $$(uname -m | sed 's/x86_64/amd64/')"
	@echo "Platform: linux/$$(uname -m | sed 's/x86_64/amd64/')"

##@ Deployment

IGNORE_NOT_FOUND ?= false

.PHONY: install
install: manifests kustomize ## Install CRDs into the K8s cluster specified in ~/.kube/config.
	$(KUSTOMIZE) build config/crd | kubectl apply -f -

.PHONY: uninstall
uninstall: manifests kustomize ## Uninstall CRDs from the K8s cluster specified in ~/.kube/config. Call with ignore-not-found=true to ignore resource not found errors during deletion.
	$(KUSTOMIZE) build config/crd | kubectl delete --ignore-not-found=${IGNORE_NOT_FOUND} -f -

.PHONY: deploy
deploy: manifests kustomize ## Deploy controller to the K8s cluster specified in ~/.kube/config.
	cd config/manager && $(KUSTOMIZE) edit set image controller=${IMG}
	$(KUSTOMIZE) build config/localhost | kubectl apply -f -

.PHONY: restart
restart: manifests kustomize ## Restart controller in the K8s cluster specified in ~/.kube/config.
	kubectl rollout restart deployment/controller-manager -n unleasherator-system
	kubectl rollout status deployment/controller-manager -n unleasherator-system --timeout=60s

.PHONY: logs
logs: manifests kustomize ## Show logs for controller in the K8s cluster specified in ~/.kube/config.
	kubectl logs deployment/unleasherator-controller-manager -n unleasherator-system -f

.PHONY: undeploy
undeploy: ## Undeploy controller from the K8s cluster specified in ~/.kube/config. Call with ignore-not-found=true to ignore resource not found errors during deletion.
	$(KUSTOMIZE) build config/localhost | kubectl delete --ignore-not-found=${IGNORE_NOT_FOUND} -f -

##@ Build Dependencies

## Location to install dependencies to
LOCALBIN ?= $(shell pwd)/bin
$(LOCALBIN):
	mkdir -p $(LOCALBIN)

## Tool Binaries
KUSTOMIZE ?= $(LOCALBIN)/kustomize
CONTROLLER_GEN ?= $(LOCALBIN)/controller-gen
ENVTEST ?= $(LOCALBIN)/setup-envtest
HELMIFY ?= $(LOCALBIN)/helmify
GOLANGCI_LINT ?= $(LOCALBIN)/golangci-lint
PROTOC ?= $(LOCALBIN)/protoc
PROTOC_GEN ?= $(LOCALBIN)/protoc-gen-go

## Tool Versions
KUSTOMIZE_VERSION ?= v5.8.0
PROTOBUF_VERSION ?= 26.1# without v-prefix

KUSTOMIZE_INSTALL_SCRIPT ?= "https://raw.githubusercontent.com/kubernetes-sigs/kustomize/master/hack/install_kustomize.sh"
.PHONY: kustomize
kustomize: $(KUSTOMIZE) ## Download kustomize locally if necessary.
$(KUSTOMIZE): $(LOCALBIN)
	test -s $(LOCALBIN)/kustomize || { curl -Ss $(KUSTOMIZE_INSTALL_SCRIPT) | bash -s -- $(subst v,,$(KUSTOMIZE_VERSION)) $(LOCALBIN); }

.PHONY: controller-gen
controller-gen: $(CONTROLLER_GEN) ## Download controller-gen locally if necessary.
$(CONTROLLER_GEN): $(LOCALBIN)
	test -s $(LOCALBIN)/controller-gen || GOBIN=$(LOCALBIN) go install sigs.k8s.io/controller-tools/cmd/controller-gen

.PHONY: envtest
envtest: $(ENVTEST) ## Download envtest-setup locally if necessary.
$(ENVTEST): $(LOCALBIN)
	test -s $(LOCALBIN)/setup-envtest || GOBIN=$(LOCALBIN) go install sigs.k8s.io/controller-runtime/tools/setup-envtest

.PHONY: helmify
helmify: $(HELMIFY) ## Download helmify locally if necessary.
$(HELMIFY): $(LOCALBIN)
	test -s $(LOCALBIN)/helmify || GOBIN=$(LOCALBIN) go install github.com/arttor/helmify/cmd/helmify

.PHONY: golangci-lint
golangci-lint: $(GOLANGCI_LINT) ## Download golangci-lint locally if necessary.
$(GOLANGCI_LINT): $(LOCALBIN)
	test -s $(LOCALBIN)/golangci-lint || GOBIN=$(LOCALBIN) go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@latest

.PHONY: protoc-gen-go
protoc-gen-go: $(PROTOC_GEN)
$(PROTOC_GEN): $(LOCALBIN)
	test -s $(LOCALBIN)/protoc-gen-go || GOBIN=$(LOCALBIN) go install google.golang.org/protobuf/cmd/protoc-gen-go

.PHONY: protoc
protoc: $(PROTOC)
$(PROTOC): $(LOCALBIN)
	if [ -f $(LOCALBIN)/protoc ]; then \
		if [ "$$($(LOCALBIN)/protoc --version | cut -d' ' -f2)" == "$(PROTOBUF_VERSION)" ]; then \
			exit 0; \
		else \
			echo "protoc version $$($(LOCALBIN)/protoc --version | cut -d' ' -f2) does not match required version $(PROTOBUF_VERSION)"; \
			rm $(LOCALBIN)/protoc; \
		fi; \
	fi; \
	\
	OS=$$(uname -s | tr '[:upper:]' '[:lower:]'); \
	\
	ARCH=$$(uname -m); \
	\
	if [[ "$$OS" == "darwin" && "$$ARCH" == "x86_64" ]]; then \
			OS="osx"; \
			ARCH="x86_64"; \
	elif [[ "$$OS" == "darwin" && "$$ARCH" == "arm64" ]]; then \
			OS="osx"; \
			ARCH="aarch_64"; \
	elif [[ "$$OS" == "linux" && "$$ARCH" == "x86_64" ]]; then \
			OS="linux"; \
			ARCH="x86_64"; \
	elif [[ "$$OS" == "linux" && "$$ARCH" == "aarch64" ]]; then \
			OS="linux"; \
			ARCH="aarch_64"; \
	else \
			echo "Unsupported operating system or architecture: $$OS $$ARCH"; \
			exit 1; \
	fi; \
	\
	echo "Installing protoc for $$OS $$ARCH"; \
	URL="https://github.com/protocolbuffers/protobuf/releases/download/v$(PROTOBUF_VERSION)/protoc-$(PROTOBUF_VERSION)-$$OS-$$ARCH.zip"; \
	\
	echo "Downloading $$URL..."; \
	curl -LO "$$URL"; \
	\
	echo "Unzipping protoc binary..."; \
	unzip -q -j protoc-*.zip 'bin/protoc' -d $(LOCALBIN); \
	\
	echo "Cleaning up..."; \
	rm protoc-*.zip;
