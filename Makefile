BINARY_DIR := bin
ORCHESTRATOR_BIN := $(BINARY_DIR)/orchestrator
ROUTER_BIN := $(BINARY_DIR)/router

.PHONY: all build test test-unit test-integration vet lint clean docker-build

all: build

## build: compile both binaries
build:
	@mkdir -p $(BINARY_DIR)
	go build -o $(ORCHESTRATOR_BIN) ./cmd/orchestrator
	go build -o $(ROUTER_BIN) ./cmd/router
	@echo "✅ Built: $(ORCHESTRATOR_BIN), $(ROUTER_BIN)"

## test: run all tests (unit + integration)
test:
	go test ./... -timeout 120s

## test-unit: run unit tests only (no Docker required)
test-unit:
	go test ./... -short -timeout 30s

## test-integration: run integration tests (requires Docker)
test-integration:
	go test ./internal/integration/... -v -timeout 120s

## test-coverage: run tests with coverage report
test-coverage:
	go test ./... -coverprofile=coverage.out -timeout 120s
	go tool cover -html=coverage.out -o coverage.html
	@echo "Coverage report: coverage.html"

## vet: run go vet
vet:
	go vet ./...

## tidy: tidy go modules
tidy:
	go mod tidy

## clean: remove build artifacts
clean:
	rm -rf $(BINARY_DIR) coverage.out coverage.html

## docker-build: build Docker images
docker-build:
	docker build -f Dockerfile.orchestrator -t orchestrator:latest .
	docker build -f Dockerfile.router -t router:latest .

## run-orchestrator: run orchestrator locally (requires env vars)
run-orchestrator: build
	$(ORCHESTRATOR_BIN)

## run-router: run router locally (requires env vars)
run-router: build
	$(ROUTER_BIN)

help:
	@grep -E '^## ' Makefile | sed 's/## //'
