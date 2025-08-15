# Makefile for mcp-memory-libsql-go

SHELL := /bin/sh

# Variables
BINARY_NAME=mcp-memory-libsql-go
MAIN_PACKAGE=./cmd/${BINARY_NAME}
BINARY_LOCATION=$(shell pwd)/bin/$(BINARY_NAME)
INTEGRATION_TESTER=./cmd/integration-tester
INTEGRATION_TESTER_BINARY=$(shell pwd)/bin/integration-tester
VERSION ?= $(shell git describe --tags --always --dirty)
REVISION ?= $(shell git rev-parse HEAD)
BUILD_DATE = $(shell date -u +'%Y-%m-%dT%H:%M:%SZ')
LDFLAGS = -ldflags "-X github.com/ZanzyTHEbar/${BINARY_NAME}/internal/buildinfo.Version=$(VERSION) -X github.com/ZanzyTHEbar/${BINARY_NAME}/internal/buildinfo.Revision=$(REVISION) -X github.com/ZanzyTHEbar/${BINARY_NAME}/internal/buildinfo.BuildDate=$(BUILD_DATE)"

# Docker config
IMAGE ?= $(BINARY_NAME)
TAG ?= local
DOCKER_IMAGE := $(IMAGE):$(TAG)
ENV_FILE ?=
ENV_FILE_ARG := $(if $(ENV_FILE),--env-file $(ENV_FILE),)
PORT_SSE ?= 8080
PORT_METRICS ?= 9090
PROFILES ?= memory
PROFILE_FLAGS := $(foreach p,$(PROFILES),--profile $(p))

# Default target
.PHONY: all
all: build

# Build the binary
.PHONY: build
build:
	CGO_ENABLED=1 go build $(LDFLAGS) -o $(BINARY_LOCATION) $(MAIN_PACKAGE)

# Build the integration tester into bin/
.PHONY: build-integration
build-integration:
	mkdir -p $(shell pwd)/bin
	CGO_ENABLED=1 go build $(LDFLAGS) -o $(INTEGRATION_TESTER_BINARY) $(INTEGRATION_TESTER)

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

# Build the docker image
.PHONY: docker docker-build
docker: docker-build
docker-build:
	docker build \
		--build-arg VERSION=$(VERSION) \
		--build-arg REVISION=$(REVISION) \
		--build-arg BUILD_DATE=$(BUILD_DATE) \
		-t $(DOCKER_IMAGE) .

.PHONY: docker-rebuild
docker-rebuild:
	docker build --no-cache \
		--build-arg VERSION=$(VERSION) \
		--build-arg REVISION=$(REVISION) \
		--build-arg BUILD_DATE=$(BUILD_DATE) \
		-t $(DOCKER_IMAGE) .

# Ensure local data directory exists
.PHONY: data
data:
	mkdir -p ./data ./data/projects

# Run the docker image (SSE default)
.PHONY: docker-run
docker-run: docker-run-sse

# Run the docker image with sse transport
.PHONY: docker-run-sse
docker-run-sse: data
	docker run --rm -it $(ENV_FILE_ARG) \
		-p $(PORT_SSE):$(PORT_SSE) -p $(PORT_METRICS):$(PORT_METRICS) \
		-v $(shell pwd)/data:/data \
		-e MODE=$(MODE) \
		-e PORT=$(PORT_SSE) \
		-e METRICS_PORT=$(PORT_METRICS) \
		$(DOCKER_IMAGE) -transport sse -addr :$(PORT_SSE) -sse-endpoint /sse

# Run the docker image with stdio transport
.PHONY: docker-run-stdio
docker-run-stdio: data
	docker run --rm -it $(ENV_FILE_ARG) \
		-v $(shell pwd)/data:/data \
		$(DOCKER_IMAGE) -transport stdio

# Run the docker image with multi-project mode (SSE)
.PHONY: docker-run-multi
docker-run-multi: data
	docker run --rm -it $(ENV_FILE_ARG) \
		-p $(PORT_SSE):$(PORT_SSE) -p $(PORT_METRICS):$(PORT_METRICS) \
		-v $(shell pwd)/data:/data \
		-e MODE=multi \
		-e PORT=$(PORT_SSE) \
		-e METRICS_PORT=$(PORT_METRICS) \
		$(DOCKER_IMAGE) -transport sse -addr :$(PORT_SSE) -sse-endpoint /sse -projects-dir /data/projects

# Compose helpers
.PHONY: compose-up compose-down compose-logs compose-ps
compose-up:
	docker compose $(PROFILE_FLAGS) up --build -d

compose-down:
	docker compose down $(if $(WITH_VOLUMES),-v,)

compose-logs:
	docker compose logs -f --tail=200 $(if $(SERVICE),$(SERVICE),)

compose-ps:
	docker compose ps

# Legacy docker-compose aliases (optional)
.PHONY: docker-compose
docker-compose: compose-up

# Production run profile (Ollama, SSE, multi-project, hybrid, pooling, metrics)
.PHONY: env-prod prod prod-down prod-logs prod-ps

# Generate a production env file used by compose
env-prod:
	@echo "Writing .env.prod..."
	@{ \
	  echo "EMBEDDINGS_PROVIDER=ollama"; \
	  echo "OLLAMA_HOST=http://ollama:11434"; \
	  echo "OLLAMA_EMBEDDINGS_MODEL=nomic-embed-text"; \
	  echo "EMBEDDING_DIMS=768"; \
	  echo "EMBEDDINGS_ADAPT_MODE=pad_or_truncate"; \
	  echo; \
	  echo "HYBRID_SEARCH=true"; \
	  echo "HYBRID_TEXT_WEIGHT=0.4"; \
	  echo "HYBRID_VECTOR_WEIGHT=0.6"; \
	  echo "HYBRID_RRF_K=60"; \
	  echo; \
	  echo "DB_MAX_OPEN_CONNS=16"; \
	  echo "DB_MAX_IDLE_CONNS=8"; \
	  echo "DB_CONN_MAX_IDLE_SEC=60"; \
	  echo "DB_CONN_MAX_LIFETIME_SEC=300"; \
	  echo; \
	  echo "METRICS_PROMETHEUS=true"; \
	  echo "METRICS_PORT=:9090"; \
	  echo; \
	  echo "TRANSPORT=sse"; \
	  echo "PORT=:8080"; \
	  echo "SSE_ENDPOINT=/sse"; \
	  echo; \
	  echo "# Multi-project auth toggles"; \
	  echo "MULTI_PROJECT_AUTH_REQUIRED=false"; \
	  echo "MULTI_PROJECT_AUTO_INIT_TOKEN=true"; \
	  echo "MULTI_PROJECT_DEFAULT_TOKEN=dev-token"; \
	  echo; \
	  echo "# Optional remote DB settings (leave blank for local files)"; \
	  echo "LIBSQL_URL="; \
	  echo "LIBSQL_AUTH_TOKEN="; \
	} > .env.prod

prod: docker-build data env-prod
	# Default prod: multi-project SSE, auth off, embeddings=ollama
	docker compose --env-file .env.prod --profile ollama --profile multi up --build -d

prod-down: env-prod
	docker compose --env-file .env.prod --profile ollama --profile multi down $(if $(WITH_VOLUMES),-v,)

prod-logs: env-prod
	docker compose --env-file .env.prod logs -f --tail=200 memory-multi

prod-ps: env-prod
	docker compose --env-file .env.prod ps

# VoyageAI profile env file
.PHONY: env-voyage voyage-up voyage-down
env-voyage:
	@echo "Writing .env.voyage..."
	@{ \
	  echo "EMBEDDINGS_PROVIDER=voyageai"; \
	  echo "VOYAGEAI_EMBEDDINGS_MODEL=voyage-3-lite"; \
	  echo "EMBEDDING_DIMS=1024"; \
	  echo "EMBEDDINGS_ADAPT_MODE=pad_or_truncate"; \
	  echo "TRANSPORT=sse"; \
	  echo "PORT=:8080"; \
	  echo "SSE_ENDPOINT=/sse"; \
	  echo "METRICS_PROMETHEUS=true"; \
	  echo "METRICS_PORT=:9090"; \
	  echo "HYBRID_SEARCH=true"; \
	} > .env.voyage

voyage-up: docker-build data env-voyage
	docker compose --env-file .env.voyage --profile voyageai up --build -d

voyage-down: env-voyage
	docker compose --env-file .env.voyage --profile voyageai down $(if $(WITH_VOLUMES),-v,)


# End-to-end docker test workflow
.PHONY: docker-test
docker-test: docker-build data
	# 1) Stand up (compose with configured profiles)
	docker compose $(PROFILE_FLAGS) up --build -d
	# 2) Wait for health
	@echo "Waiting for health..."; \
	for i in $$(seq 1 30); do \
	  if curl -fsS http://127.0.0.1:$(PORT_METRICS)/healthz >/dev/null 2>&1; then echo "Healthy"; break; fi; \
	  sleep 1; \
	  if [ $$i -eq 30 ]; then echo "Health check timed out"; exit 1; fi; \
	done
	# 3) Run integration tester against live SSE endpoint
	go run $(INTEGRATION_TESTER) -sse-url http://127.0.0.1:$(PORT_SSE)/sse -project default -timeout 45s | tee integration-report.json
	# 4) Tear down
	docker compose $(PROFILE_FLAGS) down
	# 5) Audit/report
	@echo "--- Integration Test Report (integration-report.json) ---"; \
	cat integration-report.json | jq '.' || cat integration-report.json

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
	@echo "  all - Build the project (default)"
	@echo "  build - Build the binary"
	@echo "  deps - Install dependencies"
	@echo "  test - Run tests"
	@echo "  run - Build and run the server"
	@echo "  clean - Clean build artifacts"
	@echo "  docker - Build the docker image (alias of docker-build)"
	@echo "  docker-build - Build the docker image"
	@echo "  docker-rebuild - Build the docker image with --no-cache"
	@echo "  docker-run - Run container (SSE, mounts ./data)"
	@echo "  docker-run-stdio - Run container (stdio)"
	@echo "  docker-run-multi - Run container (SSE, multi-project mode)"
	@echo "  docker-test - Run end-to-end docker test workflow"
	@echo "  compose-up - docker compose up (use PROFILES=single|multi|ollama|localai)"
	@echo "  compose-down - docker compose down (WITH_VOLUMES=1 to remove volumes)"
	@echo "  compose-logs - docker compose logs (SERVICE=memory)"
	@echo "  compose-ps - docker compose ps"
	@echo "  install - Install the binary globally"
	@echo "  help    - Show this help message"

