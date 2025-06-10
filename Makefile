# Go parameters
GOCMD=go
GOBUILD=$(GOCMD) build
GOCLEAN=$(GOCMD) clean
GOTEST=$(GOCMD) test
GOGET=$(GOCMD) get
GOMOD=$(GOCMD) mod
GOLINT=golangci-lint
#
# Binary name
BINARY_NAME=k8-highlander
VERSION=$(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
BUILD_TIME=$(shell date -u '+%Y-%m-%d_%H:%M:%S')
LDFLAGS=-ldflags "-X github.com/bhatti/k8-highlander/pkg/common.VERSION=$(VERSION) -X github.com/bhatti/k8-highlander/pkg/common.BuildInfo=$(BUILD_TIME)"
# Source files
SRC=$(shell find . -name "*.go" -type f)
PKG_LIST=$(shell go list ./... | grep -v /vendor/)
# Docker parameters
DOCKER_REGISTRY?=docker.io
DOCKER_REPO?=bhatti
DOCKER_TAG?=$(VERSION)
DOCKER_IMAGE=$(DOCKER_REGISTRY)/$(DOCKER_REPO)/$(BINARY_NAME):$(DOCKER_TAG)
# Default target
.PHONY: all
all: test lint build
# Build the binary
.PHONY: build
build:
	mkdir -p bin
	$(GOBUILD) $(LDFLAGS) -o bin/$(BINARY_NAME) ./cmd/main.go
# Clean build artifacts
.PHONY: clean
clean:
	$(GOCLEAN)
	rm -rf bin/
# Run unit tests
.PHONY: test
test:
	$(GOTEST) -v -race -cover $(PKG_LIST)
# Run integration tests
.PHONY: integ-test
integ-test:
	HIGHLANDER_TEST_MODE=real $(GOTEST) -v -race -tags=integration ./pkg/leader/... -timeout 10m -failfast
# Run all tests (unit + integration)
.PHONY: test-all
test-all: test integ-test
# Run linter
.PHONY: lint
lint:
	$(GOLINT) run ./pkg/...
# Format code
.PHONY: fmt
fmt:
	gofmt -s -w $(SRC)
# Tidy go modules
.PHONY: tidy
tidy:
	$(GOMOD) tidy
# Verify dependencies
.PHONY: verify
verify: tidy
	$(GOMOD) verify
# Build Docker image
.PHONY: docker-build
docker-build:
	docker build -t $(DOCKER_IMAGE) \
		--build-arg VERSION=$(VERSION) \
		--build-arg BUILD_TIME=$(BUILD_TIME) \
		.
# Push Docker image
.PHONY: docker-push
docker-push: docker-build
	docker push $(DOCKER_IMAGE)
# Run in Docker
.PHONY: docker-run
docker-run:
	docker run --rm -p 8080:8080 \
		-v $(PWD)/config.yaml:/etc/k8-highlander/config.yaml \
		$(DOCKER_IMAGE)
# Install dependencies
.PHONY: deps
deps:
	$(GOGET) -u golang.org/x/lint/golint
	$(GOGET) -u github.com/golangci/golangci-lint/cmd/golangci-lint
# Create static directory if it doesn't exist
.PHONY: ensure-static
ensure-static:
	mkdir -p static
# Copy dashboard files to static directory
.PHONY: dashboard
dashboard: ensure-static
	cp -r dashboard/* static/
#
# Build with dashboard files included
.PHONY: build-full
build-full: dashboard build
# Help target
.PHONY: help
help:
	@echo "Available targets:"
	@echo "  all          : Run tests, lint, and build (default)"
	@echo "  build        : Build the binary"
	@echo "  clean        : Clean build artifacts"
	@echo "  test         : Run unit tests"
	@echo "  integ-test   : Run integration tests"
	@echo "  test-all     : Run all tests (unit + integration)"
	@echo "  lint         : Run linter"
	@echo "  fmt          : Format code"
	@echo "  tidy         : Tidy go modules"
	@echo "  verify       : Verify dependencies"
	@echo "  docker-build : Build Docker image"
	@echo "  docker-push  : Push Docker image to registry"
	@echo "  docker-run   : Run in Docker"
	@echo "  deps         : Install dependencies"
	@echo "  dashboard    : Copy dashboard files to static directory"
	@echo "  build-full   : Build with dashboard files included"
	@echo "  help         : Show this help message"
