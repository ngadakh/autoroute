#!/usr/bin/env python3
"""Converts RouterBench's 0-shot pickle into the slim CSV eval/ reads.

RouterBench (withmartian/routerbench on HuggingFace) ships as a pandas
pickle with 37 columns per row, including the full generated text of every
model's response. The Go eval harness only needs per-model score + cost, so
this drops the response text and flattens the rest into plain CSV -
encoding/csv on the Go side needs no parquet dependency.

One-time, offline conversion step - never runs on the request path. This is
the only Python in the repo; see scripts/setup-routerbench.sh, which creates
a throwaway venv for it.

Usage: convert-routerbench.py <in.pkl> <out.csv>
"""
import argparse
import ast
import csv
import sys

import pandas as pd

# The 11 models RouterBench scored. Order fixes the CSV column order.
MODELS = [
    "WizardLM/WizardLM-13B-V1.2",
    "claude-instant-v1",
    "claude-v1",
    "claude-v2",
    "gpt-3.5-turbo-1106",
    "gpt-4-1106-preview",
    "meta/code-llama-instruct-34b-chat",
    "meta/llama-2-70b-chat",
    "mistralai/mistral-7b-chat",
    "mistralai/mixtral-8x7b-chat",
    "zero-one-ai/Yi-34B-Chat",
]


def parse_prompt(raw):
    """RouterBench stores `prompt` as the repr of a Python list of turns."""
    try:
        parts = ast.literal_eval(raw)
    except (ValueError, SyntaxError):
        return str(raw)
    if isinstance(parts, list):
        return "\n".join(str(p) for p in parts)
    return str(parts)


def main():
    ap = argparse.ArgumentParser(description=__doc__)
    ap.add_argument("pickle_path")
    ap.add_argument("csv_path")
    args = ap.parse_args()

    df = pd.read_pickle(args.pickle_path)

    header = ["sample_id", "prompt", "eval_name", "oracle_model_to_route_to"]
    header += [f"score:{m}" for m in MODELS]
    header += [f"cost:{m}" for m in MODELS]

    with open(args.csv_path, "w", newline="", encoding="utf-8") as f:
        w = csv.writer(f)
        w.writerow(header)
        for _, row in df.iterrows():
            out = [
                row["sample_id"],
                parse_prompt(row["prompt"]),
                row["eval_name"],
                row["oracle_model_to_route_to"],
            ]
            out += [row[m] for m in MODELS]
            out += [row[f"{m}|total_cost"] for m in MODELS]
            w.writerow(out)

    print(f"wrote {len(df)} rows -> {args.csv_path}", file=sys.stderr)


if __name__ == "__main__":
    main()
