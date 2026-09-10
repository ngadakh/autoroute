# AutoRoute

An OpenAI-compatible LLM router that picks the cheapest model likely to answer a
prompt well — built to run in production, not as a research demo.

> **Status: M0 (de-risking spike) complete.** See [`SPIKE.md`](SPIKE.md) for
> measured results. The proxy itself does not exist yet — M1 is next.

## Why

Model routing is well-trodden (RouteLLM, Not Diamond, Martian, Arch-Router, vLLM
Semantic Router). What is missing from the open-source options is the boring
production layer: health checks, circuit breakers, graceful degradation,
first-class metrics, a Helm chart, and an **honest, reproducible eval harness**
whose numbers you can re-run yourself. That is what this project is.

Design and rationale: [`docs/`](docs/) · architecture overview:
<https://claude.ai/code/artifact/acb0c124-dea8-43e5-ad7e-a7f5aa1e82c3>

## How routing works

A layered pipeline; the common case makes **zero LLM calls**.

| Layer | Mechanism | Added latency |
|---|---|---|
| **L1** | heuristics — tokens, code fences, task verbs, turns, tools | ~µs |
| **L2** | one in-process embedding (ONNX, CPU) → nearest route centroid → tier | ~2–15 ms |
| **L3** | *optional, gated* — a small judge model, only for the uncertain confidence band | ~300 ms |

Category → model-tier mapping is config-driven (add a model without retraining).

## Spike quickstart

```sh
make setup   # fetch ONNX Runtime + all-MiniLM-L6-v2 into third_party/ and models/ (gitignored)
make spike   # embed the worked-example prompts, print routing decisions + latency
make test    # unit tests
```

`make setup` downloads ~150 MB and touches nothing outside the repo.
Requires Go 1.27+ and a C toolchain (for the ONNX Runtime CGo binding).

## Layout

```
cmd/spike-embed/     M0 spike entrypoint
internal/embed/      WordPiece tokenizer + in-process ONNX embedder
internal/router/     route exemplars + nearest-centroid L2 classifier
scripts/             setup-spike.sh (model/runtime fetch)
```

Planned (not yet built): `internal/proxy`, `internal/reliability`,
`internal/observability`, `internal/shadow`, `eval/`, `deploy/helm`.

## License

TBD (will be Apache-2.0).
