BINARY  := okf-operator
CMD     := ./cmd/okf-operator

# Release builds pass VERSION; plain builds report "dev".
VERSION ?= dev
COMMIT  ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo none)

.PHONY: build
build: ## Build the okf-operator binary
	go build -ldflags "-X main.version=$(VERSION) -X main.commit=$(COMMIT)" -o $(BINARY) $(CMD)

.PHONY: test
test: ## Run unit tests
	go test ./...

# renovate: datasource=go depName=sigs.k8s.io/controller-tools
CONTROLLER_GEN_VERSION := v0.23.0

.PHONY: generate
generate: ## Regenerate deepcopy code and CRDs from api/
	go run sigs.k8s.io/controller-tools/cmd/controller-gen@$(CONTROLLER_GEN_VERSION) \
		object paths=./api/... \
		crd paths=./api/... output:crd:artifacts:config=deploy/crds

.PHONY: lint
lint: ## Run linter
	golangci-lint run ./...

.PHONY: help
help: ## Show this help
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | awk 'BEGIN {FS = ":.*?## "}; {printf "\033[36m%-20s\033[0m %s\n", $$1, $$2}'
