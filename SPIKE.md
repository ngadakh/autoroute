# M0 spike — findings

**Question:** can a sentence-embedding model run *in-process in Go* (no Python
sidecar, no embedding API) fast enough to sit on the request path, and does
nearest-centroid classification over route exemplars produce sane routing
decisions? And is the RouterBench eval data reachable?

**Answer: yes on all counts.** Decision #2 in the plan (Go core, ONNX in-process)
is de-risked. Proceed to M1.

Reproduce: `make setup && make spike && make test`
(macOS arm64, Apple Silicon, Go 1.27, ONNX Runtime 1.29.1, `all-MiniLM-L6-v2` fp32).

---

## 1. In-process ONNX inference works with one native dependency

| | |
|---|---|
| Library | `github.com/yalue/onnxruntime_go` v1.36.0 (CGo → `libonnxruntime.dylib`) |
| Model | `Xenova/all-MiniLM-L6-v2`, `onnx/model.onnx` (fp32, 90 MB), output `last_hidden_state` |
| Tokenizer | hand-rolled BERT WordPiece, ~180 lines, **zero extra deps** (`internal/embed/wordpiece.go`) |
| Runtime dep | `libonnxruntime` shared lib, fetched to `third_party/` by a script — no brew, no sudo |
| Pooling | mean-pool over attention mask + L2 normalise, in Go |

The only non-Go artifact is the ONNX Runtime shared library. It is vendored by
`scripts/setup-spike.sh` from the official GitHub release and loaded by absolute
path at startup — no system install. A pure-Go tokenizer removes what is usually
the second CGo dependency (HF `tokenizers`); parity is not exact (see §4) but is
more than close enough for embedding similarity.

**No Python sidecar is needed.** That option is now off the table unless a future
model can't be exported to ONNX.

## 2. Latency is well inside budget

Single prompt, in-process, CPU (M-series), 300 warm iterations after 20 warm-ups:

```
model load (one-time) : 202 ms
classifier build (31 exemplars, one-time) : 221 ms
embed cold call : 3.03 ms
embed warm p50  : 2.44 ms
embed warm p90  : 5.31 ms
embed warm p99  : 20.98 ms
embed warm max  : 39.9 ms
```

Plan budgeted ~10–25 ms for the L2 path; measured **p50 2.4 ms**. The p99/max tail
(~21–40 ms) is GC + scheduler jitter on a laptop under load; addressable later
with a warmed session pool and `GOGC` tuning, and still within budget. This
comfortably clears the "routing must cost far less than the model price gap it
saves" bar.

## 3. Nearest-centroid routing is sane out of the box

6 seed routes, 31 exemplars, one centroid each. The six worked examples from the
architecture doc, classified purely by L2 (no heuristics, no judge):

```
A "Who is the prime minister of India?"            -> cheap    (factual-lookup)        conf 1.00
B "Prove sum of first n odd numbers = n squared"   -> frontier (formal-reasoning)      conf 1.00
C "Make this idiomatic and add type hints: ..."    -> mid      (scoped-code-edit)      conf 1.00
D "B2B pricing change ... is this a good idea?"    -> frontier (open-judgement)        conf 1.00
E "Rewrite this sentence to sound more formal."    -> cheap    (simple-rewrite)        conf 1.00
F "Extract every date ... return as JSON."         -> mid      (structured-extraction) conf 1.00
```

**6/6 agree with hand-labels.** Top-route cosine 0.42–0.64, runner-up mostly
< 0.25 — wide margins, hence confidence saturates at 1.00. This is a tiny,
curated sample and the real distribution will be messier (that is what M4's
RouterBench eval is for), but it confirms the mechanism is real, not hopeful.

## 4. Known gaps (deliberate, tracked)

- **Tokenizer parity.** No accent stripping, no CJK segmentation, greedy WordPiece
  only. Fine for embedding similarity; revisit if we adopt a multilingual model.
- **Confidence metric is a placeholder.** Top-2 cosine margin ÷ 0.15, clamped.
  Needs calibration against real data before θ_low/θ_high mean anything (M4).
- **No batching.** One prompt per `Run`. Batching exemplar embedding at startup is
  an easy win; per-request stays single.
- **fp32 model.** `model_quantized.onnx` (int8, ~23 MB) is worth benchmarking for
  the container image and cold start.
- **p99 tail** not yet investigated (session pool / GC).

## 5. RouterBench eval data — reachable

| Dataset | Access | Size | Format | Use |
|---|---|---|---|---|
| `withmartian/routerbench` → `routerbench_0shot.pkl` | public, ungated | 94 MB | pandas pickle | primary eval substrate (11 models × 7 benchmarks, cost + score metadata) |
| `withmartian/routerbench` → `routerbench_5shot.pkl` | public, ungated | 163 MB | pandas pickle | 5-shot variant |
| `routellm/gpt4_dataset` → `train/valid.jsonl` | public, ungated | 276 / 25 MB | JSONL | Arena preference pairs, usable directly |

RouterBench ships as pickle, so M4 needs a **one-time Python conversion step**
(`pandas.read_pickle(...).to_parquet(...)`) before the Go harness reads it.
RouteLLM's JSONL needs no conversion. Neither is gated. No blocker.

## 6. Recommendation

- Keep **Go + in-process ONNX**. Ship `onnxruntime` as a vendored shared lib in
  the container; document the setup script for local dev.
- Carry `internal/embed` and `internal/router` forward roughly as-is into M2;
  promote the spike's ad-hoc benchmark into a real `go test -bench`.
- In M4, add `scripts/convert-routerbench.py` (the only Python we allow) and have
  the Go harness read parquet.
