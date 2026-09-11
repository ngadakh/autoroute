# AutoRoute

An OpenAI-compatible LLM router that picks the cheapest model likely to answer a
prompt well — built to run in production, not as a research demo.

> **Status: M1 — proxy skeleton.** The OpenAI-compatible edge is up (forwarding,
> streaming, health, metrics, Docker). Routing itself lands in M2; until then a
> request names a catalogue model and the proxy forwards it 1:1.
> M0 de-risking spike: [`SPIKE.md`](SPIKE.md).

## Why

Model routing is well-trodden (RouteLLM, Not Diamond, Martian, Arch-Router, vLLM
Semantic Router). What is missing from the open-source options is the boring
production layer: health checks, circuit breakers, graceful degradation,
first-class metrics, a Helm chart, and an **honest, reproducible eval harness**
whose numbers you can re-run yourself. That is what this project is.

Design and rationale: [`docs/ARCHITECTURE.md`](docs/ARCHITECTURE.md) · visual
version: <https://claude.ai/code/artifact/acb0c124-dea8-43e5-ad7e-a7f5aa1e82c3>

## Quickstart

```sh
make run     # start the proxy on :8080 with mock providers (no API key needed)
```

```sh
# non-streaming
curl localhost:8080/v1/chat/completions -H 'content-type: application/json' \
  -d '{"model":"fast","messages":[{"role":"user","content":"hello"}]}'

# streaming (SSE)
curl -N localhost:8080/v1/chat/completions -H 'content-type: application/json' \
  -d '{"model":"smart","stream":true,"messages":[{"role":"user","content":"hi"}]}'

curl localhost:8080/healthz          # liveness
curl localhost:8080/readyz           # readiness (503 while draining)
curl localhost:8080/metrics          # Prometheus
curl localhost:8080/v1/models        # catalogue
```

To use a real provider, uncomment the `openai` models in
[`configs/catalogue.yaml`](configs/catalogue.yaml) and export `OPENAI_API_KEY`.

### Endpoints

| Method | Path | Purpose |
|---|---|---|
| `POST` | `/v1/chat/completions` | OpenAI-compatible; forwards to the catalogue model, relays streams |
| `GET` | `/v1/models` | catalogue as an OpenAI model list |
| `GET` | `/healthz` | liveness — always 200 while the process serves |
| `GET` | `/readyz` | readiness — 503 before startup and during shutdown drain |
| `GET` | `/metrics` | Prometheus (`autoroute_http_*`, `autoroute_upstream_*`) |

### Other targets

```sh
make test    # all unit tests, race detector on
make build   # static binary -> bin/autoroute
make docker  # distroless container image
make demo    # proxy + Prometheus via docker compose (:8080, :9090)
```

## The M0 spike (embedding router)

The routing brain was prototyped separately first — see [`SPIKE.md`](SPIKE.md).

```sh
make setup   # fetch ONNX Runtime + all-MiniLM-L6-v2 (~150 MB, into gitignored dirs)
make spike   # embed the worked-example prompts, print routing decisions + latency
```

`make setup`/`make spike` need a C toolchain (ONNX Runtime CGo binding). The proxy
does not — `cmd/autoroute` builds CGO-free.

## Layout

```
cmd/autoroute/          the proxy (M1)
cmd/spike-embed/        M0 embedding/routing spike
internal/config/        model catalogue (providers + client-facing models)
internal/openai/        minimal chat-completions schema (peek + model rewrite)
internal/provider/      upstream adapters — openai-compatible, mock
internal/proxy/         HTTP edge: routes, relay, health, instrumentation
internal/observability/ Prometheus metrics
internal/embed/         WordPiece tokenizer + in-process ONNX embedder (spike)
internal/router/        route exemplars + nearest-centroid L2 classifier (spike)
deploy/compose/         docker-compose demo (proxy + Prometheus)
docs/ARCHITECTURE.md    full design
```

Planned: `internal/reliability`, `internal/shadow`, `eval/`, `deploy/helm`.

## Roadmap

| | Branch | State |
|---|---|---|
| M0 | `m0-spike` | ✅ in-process ONNX embedding + nearest-centroid router |
| **M1** | `m1-proxy-skeleton` | **⬅ OpenAI-compatible proxy: forward, stream, health, metrics, Docker** |
| M2 | `m2-layered-router` | L1 heuristics + L2 embedding + confidence band; decision log |
| M3 | `m3-reliability-observability` | breakers, fallback chain, full metrics, Grafana, Helm |
| M4 | `m4-eval-harness` | RouterBench replay, published numbers, break-even |
| M5 | `m5-shadow-detector` | shadow sampling + quality-delta metric |
| M6 | `m6-flagship-polish` | README, blog, demo |

## License

TBD (will be Apache-2.0).
