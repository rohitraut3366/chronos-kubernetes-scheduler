# Variables
BINARY_NAME=chronos-kubernetes-scheduler
IMAGE_NAME=chronos-kubernetes-scheduler
VERSION?=latest
LDFLAGS="-w -s"

# Go parameters
GOCMD=go
GOBUILD=$(GOCMD) build
GOCLEAN=$(GOCMD) clean
GOTEST=$(GOCMD) test
GOGET=$(GOCMD) get
GOMOD=$(GOCMD) mod
BINARY_DIR=bin

# Build directory
BUILD_DIR=build

.PHONY: help build test clean docker-build docker-push run deps lint fmt vet

## help: Show this help message
help:
	@echo "Available targets:"
	@sed -n 's/^##//p' $(MAKEFILE_LIST) | column -t -s ':' | sed -e 's/^/ /'

## build: Build the scheduler binary
build: deps
	@echo "Building $(BINARY_NAME)..."
	@mkdir -p $(BINARY_DIR)
	CGO_ENABLED=0 GOOS=linux $(GOBUILD) -a -ldflags=$(LDFLAGS) -o $(BINARY_DIR)/$(BINARY_NAME) ./cmd/scheduler
	@echo "✅ Binary built at $(BINARY_DIR)/$(BINARY_NAME)"

## test: Run all tests
test:
	@echo "Running tests..."
	$(GOTEST) -v -race -coverprofile=coverage.out ./...
	@echo "✅ Tests completed"

## test-unit: Run unit tests only
test-unit:
	@echo "Running unit tests..."
	$(GOTEST) -v -race ./internal/scheduler -run "^Test.*[^Integration]$$"
	@echo "✅ Unit tests completed"

## test-integration: Run integration tests only  
test-integration:
	@echo "Running integration tests..."
	$(GOTEST) -v -race ./internal/scheduler -run "TestPluginIntegration|TestIntegration"
	@echo "✅ Integration tests completed"

## bench: Run benchmarks
bench:
	@echo "Running benchmarks..."
	$(GOTEST) -bench=. -benchmem ./...
	@echo "✅ Benchmarks completed"

## coverage: Generate test coverage report
coverage: test
	@echo "Generating coverage report..."
	$(GOCMD) tool cover -html=coverage.out -o coverage.html
	@echo "✅ Coverage report generated at coverage.html"

## clean: Clean build artifacts
clean:
	@echo "Cleaning build artifacts..."
	$(GOCLEAN)
	rm -rf $(BINARY_DIR)
	rm -f coverage.out coverage.html
	@echo "✅ Clean completed"

## deps: Download and tidy dependencies
deps:
	@echo "Downloading dependencies..."
	$(GOMOD) download
	$(GOMOD) tidy
	@echo "✅ Dependencies updated"

## fmt: Format Go code
fmt:
	@echo "Formatting Go code..."
	$(GOCMD) fmt ./...
	@echo "✅ Code formatted"

## vet: Run go vet
vet:
	@echo "Running go vet..."
	$(GOCMD) vet ./...
	@echo "✅ Vet completed"

## lint: Run golangci-lint (requires golangci-lint installation)
lint:
	@echo "Running linter..."
	@if command -v golangci-lint >/dev/null 2>&1; then \
		golangci-lint run; \
		echo "✅ Linting completed"; \
	else \
		echo "⚠️  golangci-lint not installed. Run: go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest"; \
	fi

## docker-build: Build Docker image
docker-build:
	@echo "Building Docker image $(IMAGE_NAME):$(VERSION)..."
	docker build -f $(BUILD_DIR)/Dockerfile -t $(IMAGE_NAME):$(VERSION) . --load
	@echo "✅ Docker image built: $(IMAGE_NAME):$(VERSION)"

## docker-push: Push Docker image (set REGISTRY variable for custom registry)
docker-push: docker-build
	@echo "Pushing Docker image $(IMAGE_NAME):$(VERSION)..."
	@if [ -n "$(REGISTRY)" ]; then \
		docker tag $(IMAGE_NAME):$(VERSION) $(REGISTRY)/$(IMAGE_NAME):$(VERSION); \
		docker push $(REGISTRY)/$(IMAGE_NAME):$(VERSION); \
	else \
		docker push $(IMAGE_NAME):$(VERSION); \
	fi
	@echo "✅ Docker image pushed"

## run: Run the scheduler locally (for development)
run: build
	@echo "Running $(BINARY_NAME) locally..."
	./$(BINARY_DIR)/$(BINARY_NAME) --v=2

## deploy: Deploy to Kubernetes (requires kubectl)
deploy:
	@echo "Deploying to Kubernetes..."
	kubectl apply -f deploy/manifests.yaml
	@echo "✅ Deployment completed"

## undeploy: Remove from Kubernetes
undeploy:
	@echo "Removing from Kubernetes..."
	kubectl delete -f deploy/manifests.yaml --ignore-not-found
	@echo "✅ Undeployment completed"

## logs: View scheduler logs (requires kubectl)
logs:
	@echo "Fetching scheduler logs..."
	kubectl logs -n kube-system -l app=chronos-kubernetes-scheduler -f

## install-tools: Install development tools
install-tools:
	@echo "Installing development tools..."
	go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest
	@if ! command -v helm >/dev/null 2>&1; then \
		echo "⚠️  Helm not found. Please install Helm 3.8+ from https://helm.sh/docs/intro/install/"; \
	fi
	@echo "✅ Tools installed"

## all: Run fmt, vet, test, and build
all: fmt vet test build

## ci: Run all checks for CI/CD
ci: deps fmt vet lint test build

## helm-lint: Lint Helm chart
helm-lint:
	@echo "Linting Helm chart..."
	@if command -v helm >/dev/null 2>&1; then \
		helm lint charts/chronos-kubernetes-scheduler; \
		echo "✅ Helm chart linting completed"; \
	else \
		echo "⚠️  Helm not found. Skipping chart linting."; \
	fi

## helm-template: Template Helm chart
helm-template:
	@echo "Templating Helm chart..."
	@if command -v helm >/dev/null 2>&1; then \
		helm template test-release charts/chronos-kubernetes-scheduler; \
		echo "✅ Helm chart templating completed"; \
	else \
		echo "⚠️  Helm not found. Skipping chart templating."; \
	fi

## helm-package: Package Helm chart
helm-package:
	@echo "Packaging Helm chart..."
	@if command -v helm >/dev/null 2>&1; then \
		helm package charts/chronos-kubernetes-scheduler; \
		echo "✅ Helm chart packaged"; \
	else \
		echo "⚠️  Helm not found. Skipping chart packaging."; \
	fi

## helm-install: Install Helm chart locally
helm-install:
	@echo "Installing Helm chart..."
	@if command -v helm >/dev/null 2>&1; then \
		helm upgrade --install chronos-kubernetes-scheduler charts/chronos-kubernetes-scheduler \
			--namespace kube-system \
			--set image.tag=latest; \
		echo "✅ Helm chart installed"; \
	else \
		echo "⚠️  Helm not found. Cannot install chart."; \
	fi

## helm-uninstall: Uninstall Helm chart
helm-uninstall:
	@echo "Uninstalling Helm chart..."
	@if command -v helm >/dev/null 2>&1; then \
		helm uninstall chronos-kubernetes-scheduler --namespace kube-system; \
		echo "✅ Helm chart uninstalled"; \
	else \
		echo "⚠️  Helm not found. Cannot uninstall chart."; \
	fi

## helm-test: Test Helm chart
helm-test:
	@echo "Testing Helm chart..."
	@if command -v helm >/dev/null 2>&1; then \
		helm test chronos-kubernetes-scheduler --namespace kube-system --logs; \
		echo "✅ Helm chart tests completed"; \
	else \
		echo "⚠️  Helm not found. Cannot run chart tests."; \
	fi

## helm-docs: Generate Helm chart documentation
helm-docs:
	@echo "Generating Helm chart documentation..."
	@if command -v helm-docs >/dev/null 2>&1; then \
		helm-docs charts/chronos-kubernetes-scheduler; \
		echo "✅ Helm documentation generated"; \
	else \
		echo "⚠️  helm-docs not found. Install with: go install github.com/norwoodj/helm-docs/cmd/helm-docs@latest"; \
	fi

## helm-all: Run all Helm operations (lint, template, package)
helm-all: helm-lint helm-template helm-package

# Default target
.DEFAULT_GOAL := help
