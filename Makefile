# Every target runs inside docker; the host needs only docker compose.
COMPOSE  := docker compose
DEV      := $(COMPOSE) --profile dev run --rm dev
LINT     := $(COMPOSE) --profile dev run --rm lint
VERSION  ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
PKG      ?= ./...

export UID := $(shell id -u)
export GID := $(shell id -g)

.PHONY: help test test-race lint fmt vet tidy build check run dry-run integration image clean

help:
	@grep -E '^[a-z-]+:.*## ' $(MAKEFILE_LIST) | awk -F':.*## ' '{printf "  %-12s %s\n", $$1, $$2}'

test: ## run unit tests
	$(DEV) go test $(PKG)

test-race: ## run unit tests with the race detector
	$(COMPOSE) --profile dev run --rm race go test -race $(PKG)

lint: ## golangci-lint
	$(LINT) golangci-lint run ./...

fmt: ## gofmt all sources
	$(DEV) gofmt -l -w ./cmd ./internal

vet: ## go vet
	$(DEV) go vet $(PKG)

tidy: ## go mod tidy
	$(DEV) go mod tidy

build: ## build ./bin/rulegen
	$(DEV) go build -trimpath -ldflags "-s -w -X main.version=$(VERSION)" -o bin/rulegen ./cmd/rulegen

check: ## validate rulegen.yaml
	$(DEV) go run ./cmd/rulegen check -c rulegen.yaml

run: ## run all jobs (writes state/ and dist/, uploads if S3 is configured)
	$(DEV) go run ./cmd/rulegen run -c rulegen.yaml $(ARGS)

dry-run: ## run all jobs without writing or uploading
	$(DEV) go run ./cmd/rulegen run -c rulegen.yaml --dry-run $(ARGS)

integration: ## S3 integration tests against MinIO
	$(COMPOSE) --profile s3 run --rm integration go test -tags integration -count=1 ./internal/publish/...
	$(COMPOSE) --profile s3 down

image: ## build the runtime docker image
	$(COMPOSE) --profile app build rulegen

clean: ## remove build output and caches
	rm -rf bin .cache
	$(COMPOSE) --profile s3 down -v 2>/dev/null || true
