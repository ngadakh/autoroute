#!/usr/bin/env bash
# Fetches the ONNX Runtime shared library and the all-MiniLM-L6-v2 model files
# needed by cmd/spike-embed. Idempotent; safe to re-run. Nothing here touches
# system paths - everything lands under third_party/ and models/ (both gitignored).
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
ORT_VERSION="${ORT_VERSION:-1.29.1}"
MODEL_REPO="Xenova/all-MiniLM-L6-v2"
MODEL_DIR="$ROOT/models/all-MiniLM-L6-v2"
ORT_DIR="$ROOT/third_party/onnxruntime"

mkdir -p "$MODEL_DIR" "$ROOT/third_party"

# --- ONNX Runtime -----------------------------------------------------------
lib_glob=("$ORT_DIR"/lib/libonnxruntime*.dylib "$ORT_DIR"/lib/libonnxruntime.so*)
have_lib=0
for f in "${lib_glob[@]}"; do [ -e "$f" ] && have_lib=1; done

if [ "$have_lib" -eq 0 ]; then
  case "$(uname -s)-$(uname -m)" in
    Darwin-arm64)   pkg="onnxruntime-osx-arm64-${ORT_VERSION}" ;;
    Darwin-x86_64)  pkg="onnxruntime-osx-x86_64-${ORT_VERSION}" ;;
    Linux-x86_64)   pkg="onnxruntime-linux-x64-${ORT_VERSION}" ;;
    Linux-aarch64)  pkg="onnxruntime-linux-aarch64-${ORT_VERSION}" ;;
    *) echo "unsupported platform: $(uname -s)-$(uname -m)" >&2; exit 1 ;;
  esac
  url="https://github.com/microsoft/onnxruntime/releases/download/v${ORT_VERSION}/${pkg}.tgz"
  echo "downloading $url"
  tmp="$(mktemp -d)"
  curl -fsSL "$url" -o "$tmp/ort.tgz"
  rm -rf "$ORT_DIR"
  mkdir -p "$ORT_DIR"
  tar -xzf "$tmp/ort.tgz" -C "$ORT_DIR" --strip-components=1
  rm -rf "$tmp"
  echo "onnxruntime -> $ORT_DIR/lib"
else
  echo "onnxruntime already present"
fi

# --- model files ----------------------------------------------------------
for f in onnx/model.onnx vocab.txt config.json tokenizer.json; do
  out="$MODEL_DIR/$(basename "$f")"
  if [ ! -f "$out" ]; then
    echo "downloading $f"
    curl -fsSL "https://huggingface.co/${MODEL_REPO}/resolve/main/${f}" -o "$out"
  fi
done
echo "model -> $MODEL_DIR"

echo
echo "setup complete. next: make spike"
