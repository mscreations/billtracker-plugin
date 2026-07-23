SHELL := /bin/bash

BINARY       := billtracker
BUILD_DIR    := bin
IMAGE        ?= ghcr.io/mscreations/billtracker-plugin
TAG          ?= latest
DOCKERFILE   := deploy/Dockerfile
COVER_FILE   := coverage.out

.PHONY: help build run test test-verbose coverage coverage-html vet fmt tidy \
        docker-build docker-run docker-push clean

help:
	@echo "Targets:"
	@echo "  build          Build the server binary into $(BUILD_DIR)/$(BINARY)"
	@echo "  run            Build and run the server locally (reads .env)"
	@echo "  test           Run go test ./..."
	@echo "  test-verbose   Run tests with -v"
	@echo "  coverage       Run tests with coverage, print summary"
	@echo "  coverage-html  Run tests with coverage, open HTML report"
	@echo "  vet            Run go vet ./..."
	@echo "  fmt            Run gofmt -l on the tree (lists unformatted files)"
	@echo "  tidy           Run go mod tidy"
	@echo "  docker-build   Build the container image (context = repo root, not deploy/)"
	@echo "  docker-run     Run the container image locally, env from .env"
	@echo "  docker-push    Push the container image"
	@echo "  clean          Remove build artifacts"

build:
	go build -o $(BUILD_DIR)/$(BINARY) ./cmd/server

run: build
	@set -a; [ -f .env ] && . ./.env; set +a; ./$(BUILD_DIR)/$(BINARY)

test:
	go test ./...

test-verbose:
	go test -v ./...

coverage:
	go test -coverprofile=$(COVER_FILE) -coverpkg=./... ./...
	go tool cover -func=$(COVER_FILE)

coverage-html: coverage
	go tool cover -html=$(COVER_FILE)

vet:
	go vet ./...

fmt:
	gofmt -l .

tidy:
	go mod tidy

# The Dockerfile COPYs go.mod/go.sum and the full source tree, so the build
# context must be the repo root, not deploy/ - `docker build deploy/` fails
# because go.mod isn't visible in that context.
docker-build:
	docker build -f $(DOCKERFILE) -t $(IMAGE):$(TAG) .

docker-run: docker-build
	docker run --rm -p 8090:8090 --env-file .env $(IMAGE):$(TAG)

docker-push:
	docker push $(IMAGE):$(TAG)

clean:
	rm -rf $(BUILD_DIR) $(COVER_FILE)
