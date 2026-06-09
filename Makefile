.PHONY: build run clean test help build-singl install-singl tidy fmt deps setup install

BINARY_NAME=singularity
BUILD_DIR=build
VERSION=0.0.1

help: ## Show this help message
	@echo "Git Frontend - Makefile"
	@echo ""
	@echo "Available targets:"
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | awk 'BEGIN {FS = ":.*?## "}; {printf "  %-15s %s\n", $$1, $$2}'

build: ## Build the binary
	@echo "Building $(BINARY_NAME) v$(VERSION)..."
	go build -o $(BUILD_DIR)/$(BINARY_NAME) ./cmd/singularity
	go build -o $(BUILD_DIR)/singl ./cmd/singl

build-singl: ## Build singl CLI binary only
	go build -o $(BUILD_DIR)/singl ./cmd/singl

install-singl: build-singl ## Install singl to GOPATH/bin
	cp $(BUILD_DIR)/singl $(GOPATH)/bin/

run: ## Run the application
	go run ./cmd/singularity

clean: ## Clean build artifacts
	@echo "Cleaning build artifacts..."
	rm -rf $(BUILD_DIR)
	go clean

test: ## Run tests
	go test -v ./...

tidy: ## Tidy go modules
	go mod tidy

fmt: ## Format code
	go fmt ./...

install: build ## Install binaries to GOPATH/bin
	cp $(BUILD_DIR)/$(BINARY_NAME) $(GOPATH)/bin/
	cp $(BUILD_DIR)/singl $(GOPATH)/bin/

# Development helpers
deps: ## Download dependencies
	go mod download

setup: ## Install git hooks (works for main repo and all worktrees)
	cp .githooks/pre-commit .git/hooks/pre-commit
	chmod +x .git/hooks/pre-commit
	@echo "Git hooks installed"
