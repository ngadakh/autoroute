# AutoRoute

An OpenAI-compatible LLM router that picks the cheapest model likely to answer a
prompt well — built to run in production, not as a research demo.

> **Status: M3 — reliability & observability.** Every upstream call — routed or
> direct — now goes through a per-provider circuit breaker; a routed (`"auto"`)
> request that hits a failing tier falls back up the chain
> (`cheap → mid → frontier → passthrough_default`) instead of failing the
> client. Grafana dashboard and a Helm chart included.
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

# routed: "auto" resolves to a tier via L1 heuristics (+ L2 embedding classifier
# if this build has it — see "Two build modes" below), not a hardcoded name
curl localhost:8080/v1/chat/completions -H 'content-type: application/json' \
  -d '{"model":"auto","messages":[{"role":"user","content":"Who is the prime minister of India?"}]}'

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
| `GET` | `/metrics` | Prometheus (`autoroute_http_*`, `autoroute_upstream_*`, `autoroute_fallback_*`, `autoroute_circuit_breaker_*`) |

### Other targets

```sh
make test    # all unit tests, race detector on
make cover   # print per-package + total test coverage (CI gates on 50%, -coverpkg=./...)
make build   # static binary -> bin/autoroute (CGO-free; router runs L1-only)
make build-router # binary with the real L2 embedding classifier (needs `make setup`)
make docker  # distroless container image (CGO-free build)
make demo    # proxy + Prometheus + Grafana via docker compose (:8080, :9090, :3000)
```

## The layered router (M2)

`configs/catalogue.yaml`'s `router:` block turns on routing: a request naming
`router.trigger_model` (default `auto`) is classified into a tier —
`cheap`/`mid`/`frontier` — instead of naming a catalogue model directly.
`router.tiers` maps each tier to a real catalogue model. Any other model name
is unaffected — still direct M1-style passthrough.

The pipeline is L1 heuristics (tokens, code fences, task verbs, turns, tools,
JSON) first; a request it can decide with high certainty never touches an
embedding call. A miss falls through to L2 — an in-process ONNX embedding
classifier — whose confidence against the nearest route decides `theta_low`/
`theta_high` bands: confident → that tier; uncertain → `router.default_tier`
(the conservative default). Every decision is counted
(`autoroute_route_decisions_total{tier,layer}`) and appended as a JSON line to
the decision log (`-decision-log`, default `data/decisions.jsonl`).

### Two build modes

L2 needs the ONNX Runtime CGo binding; L1 doesn't. So:

- **`make build`** (default, `CGO_ENABLED=0`, what `make docker` uses) — no cgo
  dependency at all. The router still runs: L1 heuristics, and every L1 miss
  resolves straight to `default_tier` — a real instance of
  degrade-to-passthrough, not a crippled mode.
- **`make build-router`** (`CGO_ENABLED=1`, after `make setup` has fetched the
  ONNX Runtime + model) — adds the real L2 embedding classifier.

`make run`/`make test` are always `CGO_ENABLED=1` and pick up L2 automatically
once `make setup` has run, otherwise degrade the same way.

## Reliability & observability (M3)

Every outgoing call — a direct-named request or a routed one — goes through
`internal/reliability`: a circuit breaker per **provider** (not per tier), so
several tiers sharing a provider account share its health state too. After
`reliability.breaker_failure_threshold` (default 5) consecutive failures a
provider is skipped fast for `reliability.breaker_cooldown` (default 30s)
instead of hanging every request on a call likely to fail; a single probe
after cooldown decides whether to close again.

A **routed** (`"auto"`) request additionally gets a fallback chain: on a
transient failure (transport error, or 429/500/502/503/504) it climbs from its
assigned tier toward more capable ones — `cheap → mid → frontier`, then
`router.passthrough_default` as the final safety net — never back down to a
cheaper tier. A **direct-named** request (M1-style) is still exactly one
attempt: the chain is inherently tier-shaped, so naming a specific model gets
you that model or an honest error, not a silent substitution — it does still
benefit from the breaker's fail-fast behavior. Every hop is counted
(`autoroute_fallback_total{from,to}`), and breaker state/trips are their own
metrics (`autoroute_circuit_breaker_state`, `autoroute_circuit_breaker_trips_total`).

`make demo` now also brings up Grafana (`:3000`, admin/admin) with a
pre-provisioned "AutoRoute" dashboard covering every metric from M1–M3
(`deploy/grafana/`). `deploy/helm/autoroute/` is a minimal chart — Deployment,
Service, ConfigMap for the catalogue, `/healthz`/`/readyz` probes, Prometheus
scrape annotations:

```sh
helm lint deploy/helm/autoroute
helm install autoroute deploy/helm/autoroute
```

No Ingress, HPA, PodDisruptionBudget, or ServiceMonitor CRD — bring your own
if you need them; see `deploy/helm/autoroute/templates/NOTES.txt`.

## The M0 spike (embedding router)

The routing brain was prototyped separately first — see [`SPIKE.md`](SPIKE.md).

```sh
make setup   # fetch ONNX Runtime + all-MiniLM-L6-v2 (~150 MB, into gitignored dirs)
make spike   # embed the worked-example prompts, print routing decisions + latency
```

## Layout

```
cmd/autoroute/          the proxy
cmd/spike-embed/        M0 embedding/routing spike
internal/config/        model catalogue + router + reliability config
internal/openai/        minimal chat-completions schema (peek + model rewrite)
internal/provider/      upstream adapters — openai-compatible, mock
internal/proxy/         HTTP edge: routes, relay, routing, dispatch, health, instrumentation
internal/observability/ Prometheus metrics + the router decision log
internal/embed/         WordPiece tokenizer + in-process ONNX embedder (cgo-isolated)
internal/router/        L1 heuristics + L2 nearest-centroid classifier + pipeline
internal/reliability/   per-provider circuit breakers + the fallback-chain dispatcher
deploy/compose/         docker-compose demo (proxy + Prometheus + Grafana)
deploy/grafana/         provisioned datasource + AutoRoute dashboard
deploy/helm/autoroute/  Helm chart
docs/ARCHITECTURE.md    full design
```

Planned: `internal/shadow`, `eval/`.

## Roadmap

| | Branch | State |
|---|---|---|
| M0 | `m0-spike` | ✅ in-process ONNX embedding + nearest-centroid router |
| M1 | `m1-proxy-skeleton` | ✅ OpenAI-compatible proxy: forward, stream, health, metrics, Docker |
| M2 | `m2-layered-router` | ✅ L1 heuristics + L2 embedding + confidence band; decision log; degrade-to-passthrough |
| **M3** | `m3-reliability-observability` | **⬅ breakers, fallback chain, full metrics, Grafana, Helm** |
| M4 | `m4-eval-harness` | RouterBench replay, published numbers, break-even |
| M5 | `m5-shadow-detector` | shadow sampling + quality-delta metric |
| M6 | `m6-flagship-polish` | README, blog, demo |

## License

TBD (will be Apache-2.0).
