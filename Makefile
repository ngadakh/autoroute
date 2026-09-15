SHELL := /usr/bin/env bash
ORT_LIB_DIR := third_party/onnxruntime/lib
VERSION ?= dev
IMAGE ?= autoroute:$(VERSION)

# ONNX Runtime is a CGo dependency loaded at runtime by the spike and by the
# router's optional L2 embedding classifier; the loader needs to find it.
# `build`/`docker` are CGO_ENABLED=0 (no cgo dependency at all, L2 stays
# disabled at runtime); `run`, `test` and `build-router` are CGO_ENABLED=1 and
# pick up the real classifier once `make setup` has fetched it, else degrade
# to L1-only automatically (see internal/router/embedder_stub.go).
ifeq ($(shell uname -s),Darwin)
  export DYLD_LIBRARY_PATH := $(abspath $(ORT_LIB_DIR)):$(DYLD_LIBRARY_PATH)
else
  export LD_LIBRARY_PATH := $(abspath $(ORT_LIB_DIR)):$(LD_LIBRARY_PATH)
endif

.PHONY: run build build-router docker demo setup spike test cover vet fmt tidy clean help eval-setup eval

run: ## run the proxy locally against configs/catalogue.yaml (mock providers)
	go run ./cmd/autoroute -catalogue configs/catalogue.yaml

build: ## build the static proxy binary into bin/autoroute (CGO-free; router runs L1-only)
	CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X main.version=$(VERSION)" -o bin/autoroute ./cmd/autoroute

build-router: ## build the proxy with the real L2 embedding classifier (needs `make setup` first)
	CGO_ENABLED=1 go build -trimpath -ldflags "-s -w -X main.version=$(VERSION)" -o bin/autoroute-router ./cmd/autoroute

docker: ## build the container image
	docker build --build-arg VERSION=$(VERSION) -t $(IMAGE) .

demo: ## proxy + Prometheus via docker compose
	docker compose -f deploy/compose/docker-compose.yaml up --build

setup: ## fetch ONNX Runtime + model files for the spike (idempotent)
	./scripts/setup-spike.sh

spike: ## run the M0 embedding/routing spike
	go run ./cmd/spike-embed

eval-setup: ## fetch + convert RouterBench for the eval harness (idempotent)
	./scripts/setup-routerbench.sh

eval: ## replay RouterBench through the router, write eval/RESULTS.md + chart (needs `make setup` + `make eval-setup`; pass EVAL_ARGS="-check-against eval/results.json" for the CI drift gate)
	CGO_ENABLED=1 go run ./cmd/eval $(EVAL_ARGS)

test: ## run all unit tests with the race detector
	CGO_ENABLED=1 go test -race ./...

cover: ## print per-package + total test coverage (matches what CI gates on)
	CGO_ENABLED=1 go test -coverpkg=./... -coverprofile=/tmp/autoroute-coverage.out ./...
	go tool cover -func=/tmp/autoroute-coverage.out | tail -1

vet: ## go vet
	go vet ./...

fmt: ## gofmt the tree in place
	gofmt -l -w .

tidy:
	go mod tidy

clean:
	rm -rf bin third_party/onnxruntime models/all-MiniLM-L6-v2 eval/data

help:
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | \
	  awk 'BEGIN{FS=":.*?## "}{printf "  \033[36m%-8s\033[0m %s\n", $$1, $$2}'
