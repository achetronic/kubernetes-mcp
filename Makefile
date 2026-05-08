
# Image URL to use all building/pushing image targets
IMG ?= ghcr.io/achetronic/kubernetes-mcp:placeholder

# Get the currently used golang install path (in GOPATH/bin, unless GOBIN is set)
ifeq (,$(shell go env GOBIN))
GOBIN=$(shell go env GOPATH)/bin
else
GOBIN=$(shell go env GOBIN)
endif

# Get the current Go OS
GO_OS ?= $(or $(GOOS),$(shell go env GOOS))
# Get the current Go ARCH
GO_ARCH ?= $(or $(GOARCH),$(shell go env GOARCH))

OS=$(shell uname | tr '[:upper:]' '[:lower:]')

# CONTAINER_TOOL defines the container tool to be used for building images.
# Be aware that the target commands are only tested with Docker which is
# scaffolded by default. However, you might want to replace it to use other
# tools. (i.e. podman)
CONTAINER_TOOL ?= docker

# Setting SHELL to bash allows bash commands to be executed by recipes.
# Options are set to exit when a recipe line exits non-zero or a piped command fails.
SHELL = /usr/bin/env bash -o pipefail
.SHELLFLAGS = -ec

.PHONY: all
all: build

##@ General

# The help target prints out all targets with their descriptions organized
# beneath their categories. The categories are represented by '##@' and the
# target descriptions by '##'. The awk command is responsible for reading the
# entire set of makefiles included in this invocation, looking for lines of the
# file as xyz: ## something, and then pretty-format the target and help. Then,
# if there's a line with ##@ something, that gets pretty-printed as a category.
# More info on the usage of ANSI control characters for terminal formatting:
# https://en.wikipedia.org/wiki/ANSI_escape_code#SGR_parameters
# More info on the awk command:
# http://linuxcommand.org/lc3_adv_awk.php

.PHONY: help
help: ## Display this help.
	@awk 'BEGIN {FS = ":.*##"; printf "\nUsage:\n  make \033[36m<target>\033[0m\n"} /^[a-zA-Z_0-9-]+:.*?##/ { printf "  \033[36m%-15s\033[0m %s\n", $$1, $$2 } /^##@/ { printf "\n\033[1m%s\033[0m\n", substr($$0, 5) } ' $(MAKEFILE_LIST)

##@ Development

.PHONY: fmt
fmt: ## Run go fmt against code.
	go fmt ./...

.PHONY: vet
vet: ## Run go vet against code.
	go vet ./...

GOLANGCI_LINT = $(shell pwd)/bin/golangci-lint
GOLANGCI_LINT_VERSION ?= v1.54.2
golangci-lint:
	@[ -f $(GOLANGCI_LINT) ] || { \
	set -e ;\
	curl -sSfL https://raw.githubusercontent.com/golangci/golangci-lint/master/install.sh | sh -s -- -b $(shell dirname $(GOLANGCI_LINT)) $(GOLANGCI_LINT_VERSION) ;\
	}

.PHONY: lint
lint: golangci-lint ## Run golangci-lint linter & yamllint
	$(GOLANGCI_LINT) run

.PHONY: lint-fix
lint-fix: golangci-lint ## Run golangci-lint linter and perform fixes
	$(GOLANGCI_LINT) run --fix

##@ Testing

# Kind cluster used by `make test-e2e`. Override to point at a different
# cluster, e.g. `make test-e2e KIND_CLUSTER=my-cluster`.
KIND_CLUSTER     ?= kmcp-e2e
KIND_K8S_VERSION ?= v1.31.0
KIND_NODE_IMAGE  ?= kindest/node:$(KIND_K8S_VERSION)

.PHONY: test
test: ## Run unit tests (no cluster needed).
	go test ./...

.PHONY: kind-up
kind-up: ## Create a local Kind cluster used by e2e tests (idempotent).
	@if ! command -v kind >/dev/null 2>&1; then \
		echo "kind is not installed. Install it from https://kind.sigs.k8s.io/"; exit 1; \
	fi
	@if ! kind get clusters 2>/dev/null | grep -qx "$(KIND_CLUSTER)"; then \
		echo "Creating Kind cluster $(KIND_CLUSTER) ($(KIND_NODE_IMAGE))..."; \
		kind create cluster --name $(KIND_CLUSTER) --image $(KIND_NODE_IMAGE); \
	else \
		echo "Kind cluster $(KIND_CLUSTER) already exists, reusing."; \
	fi

.PHONY: kind-down
kind-down: ## Delete the local Kind cluster used by e2e tests.
	@kind delete cluster --name $(KIND_CLUSTER) || true

.PHONY: test-e2e
test-e2e: kind-up ## Run end-to-end tests against the Kind cluster.
	KMCP_E2E_CONTEXT=kind-$(KIND_CLUSTER) \
	go test -tags=e2e -v -timeout 10m ./internal/k8stools/...

.PHONY: test-e2e-clean
test-e2e-clean: kind-down kind-up test-e2e kind-down ## Run e2e tests starting from a fresh Kind cluster.

##@ Build
.PHONY: swagger
swagger: install-swag ## Build Swagger documents.
	$(SWAG) init --dir "./cmd/,."  --outputTypes "go"

.PHONY: build
build: fmt vet ## Build CLI binary.
	go build -o bin/kubernetes-mcp-$(GO_OS)-$(GO_ARCH) cmd/main.go

.PHONY: run
run: fmt vet ## Run a controller from your host.
	go run ./cmd/ --config ./docs/config-http.yaml

# If you wish to build the manager image targeting other platforms you can use the --platform flag.
# (i.e. docker build --platform linux/arm64). However, you must enable docker buildKit for it.
# More info: https://docs.docker.com/develop/develop-images/build_enhancements/
.PHONY: docker-build
docker-build: ## Build docker image with the manager.
	$(CONTAINER_TOOL) build --no-cache -t ${IMG} .

.PHONY: docker-push
docker-push: ## Push docker image with the manager.
	$(CONTAINER_TOOL) push ${IMG}

PACKAGE_NAME ?= package.tar.gz
.PHONY: package
package: ## Package binary.
	@printf "\nCreating package at dist/$(PACKAGE_NAME) \n"
	@mkdir -p dist

	@if [ "$(OS)" = "linux" ]; then \
		tar --transform="s/kubernetes-mcp-$(GO_OS)-$(GO_ARCH)/kubernetes-mcp/" -cvzf dist/$(PACKAGE_NAME) -C bin kubernetes-mcp-$(GO_OS)-$(GO_ARCH) -C ../ LICENSE README.md; \
	elif [ "$(OS)" = "darwin" ]; then \
		tar -cvzf dist/$(PACKAGE_NAME) -s '/kubernetes-mcp-$(GO_OS)-$(GO_ARCH)/kubernetes-mcp/' -C bin kubernetes-mcp-$(GO_OS)-$(GO_ARCH) -C ../ LICENSE README.md; \
	else \
		echo "Unsupported OS: $(GO_OS)"; \
		exit 1; \
	fi

.PHONY: package-signature
package-signature: ## Create a signature for the package.
	@printf "\nCreating package signature at dist/$(PACKAGE_NAME).md5 \n"
	md5sum dist/$(PACKAGE_NAME) | awk '{ print $$1 }' > dist/$(PACKAGE_NAME).md5
