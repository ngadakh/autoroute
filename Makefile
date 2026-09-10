SHELL := /usr/bin/env bash
ORT_LIB_DIR := third_party/onnxruntime/lib
VERSION ?= dev
IMAGE ?= autoroute:$(VERSION)

# ONNX Runtime is a CGo dependency loaded at runtime by the spike; the loader
# needs to find it. Proxy targets (run/build/docker) have no cgo dependency.
ifeq ($(shell uname -s),Darwin)
  export DYLD_LIBRARY_PATH := $(abspath $(ORT_LIB_DIR)):$(DYLD_LIBRARY_PATH)
else
  export LD_LIBRARY_PATH := $(abspath $(ORT_LIB_DIR)):$(LD_LIBRARY_PATH)
endif

.PHONY: run build docker demo setup spike test vet fmt tidy clean help

run: ## run the proxy locally against configs/catalogue.yaml (mock providers)
	go run ./cmd/autoroute -catalogue configs/catalogue.yaml

build: ## build the static proxy binary into bin/autoroute
	CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X main.version=$(VERSION)" -o bin/autoroute ./cmd/autoroute

docker: ## build the container image
	docker build --build-arg VERSION=$(VERSION) -t $(IMAGE) .

demo: ## proxy + Prometheus via docker compose
	docker compose -f deploy/compose/docker-compose.yaml up --build

setup: ## fetch ONNX Runtime + model files for the spike (idempotent)
	./scripts/setup-spike.sh

spike: ## run the M0 embedding/routing spike
	go run ./cmd/spike-embed

test: ## run all unit tests with the race detector
	CGO_ENABLED=1 go test -race ./...

vet: ## go vet
	go vet ./...

fmt: ## gofmt the tree in place
	gofmt -l -w .

tidy:
	go mod tidy

clean:
	rm -rf bin third_party/onnxruntime models/all-MiniLM-L6-v2

help:
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | \
	  awk 'BEGIN{FS=":.*?## "}{printf "  \033[36m%-8s\033[0m %s\n", $$1, $$2}'
