# Nightjar Makefile

# Image URL to use all building/pushing image targets
IMG ?= nightjar:dev
# Controller-gen tool
CONTROLLER_GEN ?= $(shell which controller-gen 2>/dev/null)

# Go parameters
GOOS ?= linux
GOARCH ?= amd64
GO_BUILD_FLAGS ?= -ldflags="-s -w"

.PHONY: all
all: build

##@ Development

.PHONY: fmt
fmt: ## Run go fmt
	go fmt ./...

.PHONY: vet
vet: ## Run go vet
	go vet ./...

.PHONY: lint
lint: ## Run golangci-lint
	golangci-lint run ./...

.PHONY: test
test: fmt vet ## Run unit tests
	go test ./... -coverprofile cover.out -v

.PHONY: test-integration
test-integration: ## Run integration tests (requires envtest)
	go test ./test/integration/... -v -tags=integration

.PHONY: test-e2e
test-e2e: ## Run e2e tests (requires Kind cluster)
	go test ./test/e2e/... -v -tags=e2e -timeout 30m

##@ Build

.PHONY: build
build: fmt vet ## Build controller binary
	CGO_ENABLED=0 GOOS=$(GOOS) GOARCH=$(GOARCH) go build $(GO_BUILD_FLAGS) \
		-o bin/controller ./cmd/controller/

.PHONY: build-cli
build-cli: fmt vet ## Build nightjarctl CLI
	CGO_ENABLED=0 GOOS=$(GOOS) GOARCH=$(GOARCH) go build $(GO_BUILD_FLAGS) \
		-o bin/nightjarctl ./cmd/nightjarctl/

.PHONY: build-webhook
build-webhook: fmt vet ## Build webhook binary
	CGO_ENABLED=0 GOOS=$(GOOS) GOARCH=$(GOARCH) go build $(GO_BUILD_FLAGS) \
		-o bin/webhook ./cmd/webhook/

.PHONY: run
run: fmt vet ## Run controller locally (outside cluster)
	go run ./cmd/controller/ --leader-elect=false

##@ Container

.PHONY: docker-build
docker-build: ## Build docker image
	docker build -t $(IMG) .

.PHONY: docker-push
docker-push: ## Push docker image
	docker push $(IMG)

##@ Code Generation

.PHONY: manifests
manifests: controller-gen ## Generate CRD manifests
	$(CONTROLLER_GEN) crd:allowDangerousTypes=true paths="./api/..." output:crd:artifacts:config=config/crd

.PHONY: generate
generate: controller-gen ## Generate deepcopy methods
	$(CONTROLLER_GEN) object paths="./api/..."

.PHONY: controller-gen
controller-gen: ## Download controller-gen if necessary
ifeq ($(CONTROLLER_GEN),)
	go install sigs.k8s.io/controller-tools/cmd/controller-gen@latest
	$(eval CONTROLLER_GEN := $(shell go env GOPATH)/bin/controller-gen)
endif

##@ Deployment

.PHONY: install
install: manifests ## Install CRDs into the cluster
	kubectl apply -f config/crd/

.PHONY: uninstall
uninstall: ## Uninstall CRDs from the cluster
	kubectl delete -f config/crd/

.PHONY: helm-template
helm-template: ## Render Helm chart templates
	helm template nightjar deploy/helm/

.PHONY: helm-install
helm-install: ## Install via Helm
	helm install nightjar deploy/helm/ \
		--namespace nightjar-system \
		--create-namespace

.PHONY: helm-upgrade
helm-upgrade: ## Upgrade via Helm
	helm upgrade nightjar deploy/helm/ \
		--namespace nightjar-system

.PHONY: helm-uninstall
helm-uninstall: ## Uninstall via Helm
	helm uninstall nightjar --namespace nightjar-system

##@ Local Development

.PHONY: kind-create
kind-create: ## Create a Kind cluster for local development
	kind create cluster --name nightjar

.PHONY: kind-delete
kind-delete: ## Delete the Kind cluster
	kind delete cluster --name nightjar

.PHONY: kind-load
kind-load: docker-build ## Load docker image into Kind cluster
	kind load docker-image $(IMG) --name nightjar

##@ Verification (used by hack/verify.sh and agents)

.PHONY: verify
verify: ## Run all verification checks
	bash hack/verify.sh all

.PHONY: verify-phase-0
verify-phase-0: ## Verify Phase 0 completion
	bash hack/verify.sh phase0

.PHONY: verify-phase-1
verify-phase-1: ## Verify Phase 1 completion
	bash hack/verify.sh phase1

.PHONY: setup
setup: ## Install all development tools
	bash hack/setup.sh

##@ Help

.PHONY: help
help: ## Display this help
	@awk 'BEGIN {FS = ":.*##"; printf "\nUsage:\n  make \033[36m<target>\033[0m\n"} /^[a-zA-Z_0-9-]+:.*?##/ { printf "  \033[36m%-20s\033[0m %s\n", $$1, $$2 } /^##@/ { printf "\n\033[1m%s\033[0m\n", substr($$0, 5) } ' $(MAKEFILE_LIST)
