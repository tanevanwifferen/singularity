.PHONY: build run clean test help

BINARY_NAME=git-frontend
BUILD_DIR=build
VERSION=0.0.1

help: ## Show this help message
	@echo "Git Frontend - Makefile"
	@echo ""
	@echo "Available targets:"
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | awk 'BEGIN {FS = ":.*?## "}; {printf "  %-15s %s\n", $$1, $$2}'

build: ## Build the binary
	@echo "Building $(BINARY_NAME) v$(VERSION)..."
	go build -o $(BUILD_DIR)/$(BINARY_NAME) ./cmd/git-frontend

run: ## Run the application
	go run ./cmd/git-frontend

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

install: build ## Install binary to GOPATH/bin
	cp $(BUILD_DIR)/$(BINARY_NAME) $(GOPATH)/bin/

# Development helpers
deps: ## Download dependencies
	go mod download
