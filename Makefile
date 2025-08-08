
# Image URL to use all building/pushing image targets
IMG ?= ghcr.io/nais/unleasherator:main
# ENVTEST_K8S_VERSION refers to the version of kubebuilder assets to be downloaded by envtest binary.
ENVTEST_K8S_VERSION = 1.32.0

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

.PHONY: test-e2e
test-e2e: install-all ## Run end-to-end tests using raw Kubernetes manifests from Helm charts.
	@echo "Running e2e tests..."
	kubectl apply -f charts/unleasherator/templates/tests/
	@echo "Waiting for PostgreSQL databases to be ready..."
	kubectl wait --for=condition=available deployment/postgres-postgresql --timeout=300s || true
	kubectl wait --for=condition=available deployment/postgres-rc-test --timeout=300s || true
	@echo "Waiting for Unleash instances to be ready..."
	kubectl wait --for=condition=available deployment/unleash-connection-test --timeout=600s || true
	kubectl wait --for=condition=available deployment/unleash-rc-prod --timeout=600s || true
	kubectl wait --for=condition=available deployment/unleash-rc-staging --timeout=600s || true
	@echo "Now waiting for test jobs to complete..."
	kubectl wait --for=condition=complete job/unleash-connection-test-probe --timeout=900s || true
	kubectl wait --for=condition=complete job/release-channel-test-probe --timeout=900s || true
	@echo ""
	@echo "=== Test Results ==="
	@echo "=== Unleash Connection Test ==="
	kubectl logs job/unleash-connection-test-probe --tail=50 || echo "❌ Test not found or failed"
	@echo ""
	@echo "=== Release Channel Test ==="
	kubectl logs job/release-channel-test-probe --tail=50 || echo "❌ Test not found or failed"
	@echo ""
	@echo "=== Test Job Status ==="
	kubectl get jobs -l app.kubernetes.io/part-of=unleasherator

.PHONY: test-e2e-clean
test-e2e-clean: ## Clean up e2e test resources.
	@echo "Cleaning up test resources..."
	@echo "Removing finalizers from Unleash instances..."
	@for unleash in $$(kubectl get unleash -o name 2>/dev/null || true); do \
		echo "Patching $$unleash"; \
		kubectl patch $$unleash --type merge -p '{"metadata":{"finalizers":[]}}' || true; \
	done
	@echo "Removing finalizers from ApiToken instances..."
	@for apitoken in $$(kubectl get apitoken -o name 2>/dev/null || true); do \
		echo "Patching $$apitoken"; \
		kubectl patch $$apitoken --type merge -p '{"metadata":{"finalizers":[]}}' || true; \
	done
	@echo "Deleting test resources..."
	kubectl delete -f charts/unleasherator/templates/tests/ --ignore-not-found=true

.PHONY: test-e2e-watch
test-e2e-watch: ## Watch the progress of e2e tests in real-time.
	@echo "Monitoring e2e test progress..."
	@echo "Press Ctrl+C to stop watching"
	@echo ""
	@echo "=== Pods Status ==="
	kubectl get pods -l app.kubernetes.io/part-of=unleasherator -w

.PHONY: test-e2e-status
test-e2e-status: ## Check current status of e2e test resources.
	@echo "=== Test Pods ==="
	kubectl get pods -l app.kubernetes.io/part-of=unleasherator
	@echo ""
	@echo "=== Test Jobs ==="
	kubectl get jobs -l app.kubernetes.io/part-of=unleasherator
	@echo ""
	@echo "=== Test Services ==="
	kubectl get services -l app.kubernetes.io/part-of=unleasherator
	@echo ""
	@echo "=== Unleash Instances ==="
	kubectl get unleash
	@echo ""
	@echo "=== Release Channels ==="
	kubectl get releasechannel
	@echo ""
	@echo "=== API Tokens ==="
	kubectl get apitoken

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

.PHONY: install-prometheus-crds
install-prometheus-crds: ## Install Prometheus Operator CRDs (required for ServiceMonitor resources).
	@echo "Installing Prometheus Operator CRDs..."
	kubectl apply --server-side -f https://raw.githubusercontent.com/prometheus-operator/prometheus-operator/main/example/prometheus-operator-crd/monitoring.coreos.com_servicemonitors.yaml
	kubectl apply --server-side -f https://raw.githubusercontent.com/prometheus-operator/prometheus-operator/main/example/prometheus-operator-crd/monitoring.coreos.com_prometheuses.yaml
	kubectl apply --server-side -f https://raw.githubusercontent.com/prometheus-operator/prometheus-operator/main/example/prometheus-operator-crd/monitoring.coreos.com_alertmanagers.yaml
	kubectl apply --server-side -f https://raw.githubusercontent.com/prometheus-operator/prometheus-operator/main/example/prometheus-operator-crd/monitoring.coreos.com_podmonitors.yaml
	kubectl apply --server-side -f https://raw.githubusercontent.com/prometheus-operator/prometheus-operator/main/example/prometheus-operator-crd/monitoring.coreos.com_prometheusrules.yaml
	@echo "✅ Prometheus Operator CRDs installed"

.PHONY: install-all
install-all: install-prometheus-crds install ## Install both Prometheus CRDs and Unleasherator CRDs.
	@echo "✅ All CRDs installed"

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
KUSTOMIZE_VERSION ?= v5.6.0
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
	test -s $(LOCALBIN)/golangci-lint || GOBIN=$(LOCALBIN) go install github.com/golangci/golangci-lint/cmd/golangci-lint

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
