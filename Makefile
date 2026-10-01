# Local settings (GHCR_TOKEN, ...) from .env, see .env.example. Only variables declared there are exported.
ifneq (,$(wildcard .env))
include .env
export $(shell sed -n 's/^\([A-Za-z_][A-Za-z0-9_]*\)=.*/\1/p' .env)
endif

VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X main.version=$(VERSION)

COMPOSE := docker compose
PROFILES := --profile standalone --profile cluster --profile sentinel
REDIS_IMAGES ?= redis:7.4 redis:8 valkey/valkey:8 valkey/valkey:9

.PHONY: build run test lint tidy docker docker-login docker-push swagger up down screenshots test-integration test-matrix

build:
	CGO_ENABLED=0 go build -trimpath -ldflags "$(LDFLAGS)" -o bin/redigate ./cmd/redigate

run:
	go run ./cmd/redigate

test:
	go test -race -count=1 ./...

lint:
	golangci-lint run ./...

tidy:
	go mod tidy

# Generates the OpenAPI specification from annotations above route registrations.
SWAG_DIRS := ./internal/httpapi,./internal/service,./internal/codec

swagger:
	go tool swag fmt -d $(SWAG_DIRS) -g doc.go
	go tool swag init --quiet --parseFuncBody --parseInternal -d $(SWAG_DIRS) -g doc.go \
		--outputTypes json,yaml -o internal/httpapi/openapi

docker:
	docker build --build-arg VERSION=$(VERSION) -t redigate:$(VERSION) .

# GitHub Container Registry. Create a personal access token with the write:packages scope,
# put it into .env (GHCR_TOKEN, see .env.example) and log in once:
#   make docker-login
# then publish the current commit (a tag like v1.2.0 gives a clean version):
#   make docker-push                   # ghcr.io/btrvodka/redigate:<version> and :latest
#   make docker-push VERSION=v1.2.0 LATEST=false
IMAGE ?= ghcr.io/btrvodka/redigate
GHCR_USER ?= btrvodka
PLATFORMS ?= linux/amd64,linux/arm64
LATEST ?= true

docker-login:
	@test -n "$$GHCR_TOKEN" || { echo "GHCR_TOKEN is not set: put it into .env (see .env.example)"; exit 1; }
	@echo "$$GHCR_TOKEN" | docker login ghcr.io -u $(GHCR_USER) --password-stdin

docker-push:
	@case "$(VERSION)" in *dirty*) echo "refusing to publish $(VERSION): commit the changes or set VERSION"; exit 1;; esac
	docker buildx build --platform $(PLATFORMS) --build-arg VERSION=$(VERSION) \
		-t $(IMAGE):$(VERSION) $(if $(filter true,$(LATEST)),-t $(IMAGE):latest) --push .

# Starts a redis cluster (3 masters, 3 replicas) and redigate on 127.0.0.1:8080.
up:
	$(COMPOSE) --profile cluster up -d --build --wait

down:
	$(COMPOSE) $(PROFILES) --profile test down -v --remove-orphans

# Regenerates docs/images: demo data in the local cluster (flushed first), then headless Chrome.
screenshots: up
	node scripts/screenshots.mjs

# Runs integration tests inside the compose network against a fresh deployment.
test-integration:
	$(COMPOSE) $(PROFILES) --profile test down -v --remove-orphans
	$(COMPOSE) --profile test run --rm tests
	$(COMPOSE) $(PROFILES) --profile test down -v --remove-orphans

test-matrix:
	@for image in $(REDIS_IMAGES); do \
		echo "=== $$image"; \
		REDIS_IMAGE=$$image $(MAKE) test-integration || exit 1; \
	done
	@echo "=== RESP2"
	REDIS_PROTOCOL=2 $(MAKE) test-integration
