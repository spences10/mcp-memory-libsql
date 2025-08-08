SHELL := /bin/sh

APP := mcp-memory-libsql-go
BIN := ./cmd/$(APP)

.PHONY: build test docker

build:
	CGO_ENABLED=1 go build -o bin/$(APP) $(BIN)

test:
	go test ./...

docker:
	docker build -t $(APP):local .

# Makefile for mcp-memory-libsql-go

# Variables
BINARY_NAME=mcp-memory-libsql-go
MAIN_PACKAGE=./cmd/${BINARY_NAME}
BINARY_LOCATION=$(shell pwd)/bin/$(BINARY_NAME)
VERSION ?= $(shell git describe --tags --always --dirty)
REVISION ?= $(shell git rev-parse HEAD)
BUILD_DATE = $(shell date -u +'%Y-%m-%dT%H:%M:%SZ')
LDFLAGS = -ldflags "-X github.com/ZanzyTHEbar/${BINARY_NAME}/internal/buildinfo.Version=$(VERSION) -X github.com/ZanzyTHEbar/${BINARY_NAME}/internal/buildinfo.Revision=$(REVISION) -X github.com/ZanzyTHEbar/${BINARY_NAME}/internal/buildinfo.BuildDate=$(BUILD_DATE)"

# Default target
.PHONY: all
all: build

# Build the binary
.PHONY: build
build:
	go build $(LDFLAGS) -o $(BINARY_LOCATION) $(MAIN_PACKAGE)

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
	$(BINARY_LOCATION)

# Clean build artifacts
.PHONY: clean
clean:
	rm -f $(BINARY_LOCATION)

# Install the binary globally
.PHONY: install
install:
	@echo "Installing $(BINARY_NAME) globally..."
	@chmod +x install.sh
	./install.sh $(BINARY_LOCATION)

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
