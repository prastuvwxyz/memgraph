.DEFAULT_GOAL := build

BIN := memgraph
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
LDFLAGS := -s -w -X main.version=$(VERSION)

.PHONY: build
build: ## Build binary for current platform
	go build -ldflags "$(LDFLAGS)" -o $(BIN) ./cmd/memgraph

.PHONY: install
install: ## Install to $GOPATH/bin
	go install -ldflags "$(LDFLAGS)" ./cmd/memgraph

.PHONY: test
test: ## Run all tests
	go test ./...

.PHONY: test-verbose
test-verbose: ## Run tests with verbose output
	go test -v ./...

.PHONY: lint
lint: ## Run go vet
	go vet ./...

.PHONY: release-dry
release-dry: ## Dry-run GoReleaser (no publish)
	goreleaser release --snapshot --clean

.PHONY: clean
clean: ## Remove build artifacts
	rm -f $(BIN)
	rm -rf dist/

.PHONY: help
help: ## Show available commands
	@awk 'BEGIN {FS = ":.*##"} /^[a-zA-Z_-]+:.*##/ { printf "  %-15s %s\n", $$1, $$2 }' $(MAKEFILE_LIST)
