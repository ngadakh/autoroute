SHELL := /usr/bin/env bash
ORT_LIB_DIR := third_party/onnxruntime/lib

# ONNX Runtime is a CGo dependency loaded at runtime; the linker/loader needs to
# find it. spike/test targets export the platform's library path automatically.
ifeq ($(shell uname -s),Darwin)
  export DYLD_LIBRARY_PATH := $(abspath $(ORT_LIB_DIR)):$(DYLD_LIBRARY_PATH)
else
  export LD_LIBRARY_PATH := $(abspath $(ORT_LIB_DIR)):$(LD_LIBRARY_PATH)
endif

.PHONY: setup spike test tidy fmt clean

setup: ## fetch ONNX Runtime + model files (idempotent)
	./scripts/setup-spike.sh

spike: ## run the M0 embedding/routing spike
	go run ./cmd/spike-embed

test: ## unit tests (tokenizer etc.)
	go test ./...

tidy:
	go mod tidy

fmt:
	gofmt -l -w .

clean:
	rm -rf bin third_party/onnxruntime models/all-MiniLM-L6-v2

help:
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | \
	  awk 'BEGIN{FS=":.*?## "}{printf "  %-10s %s\n", $$1, $$2}'
