# Makefile for mcp-memory-libsql-go

# Variables
BINARY_NAME=mcp-memory-libsql-go
MAIN_PACKAGE=./cmd/mcp-memory-libsql

# Default target
.PHONY: all
all: build

# Build the binary
.PHONY: build
build:
	go build -o $(BINARY_NAME) $(MAIN_PACKAGE)

# Install dependencies
.PHONY: deps
deps:
	go mod tidy

# Run tests
.PHONY: test
test:
	go test ./...

# Run the server
.PHONY: run
run: build
	./$(BINARY_NAME)

# Clean build artifacts
.PHONY: clean
clean:
	rm -f $(BINARY_NAME)

# Install the binary globally
.PHONY: install
install: build
	go install $(MAIN_PACKAGE)

# Help
.PHONY: help
help:
	@echo "Available targets:"
	@echo "  all     - Build the project (default)"
	@echo "  build   - Build the binary"
	@echo "  deps    - Install dependencies"
	@echo "  test    - Run tests"
	@echo "  run     - Build and run the server"
	@echo "  clean   - Clean build artifacts"
	@echo "  install - Install the binary globally"
	@echo "  help    - Show this help message"
