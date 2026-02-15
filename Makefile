.PHONY: build install clean test help

# Version information
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
COMMIT := $(shell git rev-parse --short HEAD 2>/dev/null || echo "none")
DATE := $(shell date -u +"%Y-%m-%d %H:%M:%S UTC")

# Build flags
LDFLAGS := -ldflags "-X 'main.version=$(VERSION)' -X 'main.commit=$(COMMIT)' -X 'main.date=$(DATE)'"

# Binary name
BINARY := lokit

# Default target
all: build

# Build the binary
build:
	@echo "Building $(BINARY) $(VERSION)..."
	go build $(LDFLAGS) -o $(BINARY)
	@echo "Done! Binary: ./$(BINARY)"

# Install to $GOPATH/bin
install:
	@echo "Installing $(BINARY) $(VERSION)..."
	go install $(LDFLAGS)
	@echo "Done! Installed to $(shell go env GOPATH)/bin/$(BINARY)"

# Clean build artifacts
clean:
	@echo "Cleaning..."
	rm -f $(BINARY)
	@echo "Done!"

# Run tests
test:
	go test -v ./...

# Show help
help:
	@echo "Available targets:"
	@echo "  build    - Build the binary (default)"
	@echo "  install  - Install to \$$GOPATH/bin"
	@echo "  clean    - Remove build artifacts"
	@echo "  test     - Run tests"
	@echo "  help     - Show this help"
	@echo ""
	@echo "Environment variables:"
	@echo "  VERSION  - Override version (default: git describe)"
