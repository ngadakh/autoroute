#!/usr/bin/env bash
# Fetches RouterBench's 0-shot dataset and converts it to the CSV eval/
# reads. Idempotent; safe to re-run. Everything lands under eval/data/
# (gitignored, ~100MB pickle + ~15MB csv - not something to commit).
#
# Needs python3 + pip for the one-time pandas conversion (see
# scripts/convert-routerbench.py) - the only Python this repo uses, and only
# offline, never on the request path.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
DATA_DIR="$ROOT/eval/data"
PICKLE="$DATA_DIR/routerbench_0shot.pkl"
CSV="$DATA_DIR/routerbench_0shot.csv"
VENV="$DATA_DIR/.venv"

mkdir -p "$DATA_DIR"

if [ ! -f "$PICKLE" ]; then
  echo "downloading routerbench_0shot.pkl (~100MB, from huggingface.co/datasets/withmartian/routerbench)"
  curl -fsSL "https://huggingface.co/datasets/withmartian/routerbench/resolve/main/routerbench_0shot.pkl" -o "$PICKLE"
else
  echo "routerbench_0shot.pkl already present"
fi

if [ ! -f "$CSV" ]; then
  if [ ! -d "$VENV" ]; then
    echo "creating a venv for the one-time pandas conversion"
    python3 -m venv "$VENV"
    "$VENV/bin/pip" install -q --upgrade pip
    "$VENV/bin/pip" install -q pandas
  fi
  echo "converting pickle -> csv"
  "$VENV/bin/python3" "$ROOT/scripts/convert-routerbench.py" "$PICKLE" "$CSV"
else
  echo "routerbench_0shot.csv already present"
fi

echo
echo "setup complete. next: make eval"
