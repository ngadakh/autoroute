# AutoRoute

[![build](https://github.com/ngadakh/autoroute/actions/workflows/build.yml/badge.svg)](https://github.com/ngadakh/autoroute/actions/workflows/build.yml)
[![test](https://github.com/ngadakh/autoroute/actions/workflows/test.yml/badge.svg)](https://github.com/ngadakh/autoroute/actions/workflows/test.yml)
[![lint](https://github.com/ngadakh/autoroute/actions/workflows/lint.yml/badge.svg)](https://github.com/ngadakh/autoroute/actions/workflows/lint.yml)
[![fmt](https://github.com/ngadakh/autoroute/actions/workflows/fmt.yml/badge.svg)](https://github.com/ngadakh/autoroute/actions/workflows/fmt.yml)
[![License: Apache-2.0](https://img.shields.io/badge/License-Apache_2.0-blue.svg)](LICENSE)

An OpenAI-compatible LLM router that picks the cheapest model likely to answer a
prompt well — built to run in production, not as a research demo.

![demo: three prompts routed to three tiers, then the metrics that prove it](docs/demo.gif)

Three prompts above, three tiers, no hardcoded model name — `auto` resolves to
`fast`/`smart`/`genius` via the real L1/L2 router, and `autoroute_route_decisions_total`
confirms which layer decided each one. Regenerate with `vhs docs/demo.tape`
(or `scripts/record-demo.sh` if `vhs` can't run a headless browser in your
environment).

## Why

Model routing is well-trodden (RouteLLM, Not Diamond, Martian, Arch-Router, vLLM
Semantic Router). What is missing from the open-source options is the boring
production layer: health checks, circuit breakers, graceful degradation,
first-class metrics, a Helm chart, and an **honest, reproducible eval harness**
whose numbers you can re-run yourself. That is what this project is — the
build story and the numbers that didn't flatter it are in
[`docs/BLOG.md`](docs/BLOG.md).

Design and rationale: [`docs/ARCHITECTURE.md`](docs/ARCHITECTURE.md) · visual
version: [`docs/architecture.html`](docs/architecture.html)

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
  -d '{"model":"auto","messages":[{"role":"user","content":"What is the capital of France?"}]}'

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
| `GET` | `/metrics` | Prometheus (`autoroute_http_*`, `autoroute_upstream_*`, `autoroute_fallback_*`, `autoroute_circuit_breaker_*`, `autoroute_shadow_quality_delta`) |

### Other targets

```sh
make test    # all unit tests, race detector on
make cover   # print per-package + total test coverage (CI gates on 50%, -coverpkg=./...)
make build   # static binary -> bin/autoroute (CGO-free; router runs L1-only)
make build-router # binary with the real L2 embedding classifier (needs `make setup`)
make docker  # distroless container image (CGO-free build)
make demo    # proxy + Prometheus + Grafana via docker compose (:8080, :9090, :3000)
```

Tagged releases publish the same image to GHCR — no local build needed:

```sh
docker pull ghcr.io/ngadakh/autoroute:latest
```

## How it's built

Four pieces, each shipped as its own milestone and still separable: a router
that knows when it's unsure, a reliability layer that assumes upstreams will
fail, an eval harness that publishes real numbers instead of picking flattering
ones, and a detector for the failure mode that trips no alarm at all. The
build story behind each is in [`docs/BLOG.md`](docs/BLOG.md); this section is
the reference version.

### The layered router

`configs/catalogue.yaml`'s `router:` block turns on routing: naming the
trigger model (`auto` by default) gets a request classified into a tier —
`cheap`/`mid`/`frontier` — instead of naming a model directly. Any other
model name still passes straight through, unrouted.

L1 heuristics (token count, code fences, task verbs, and more) decide with
certainty when they can, skipping the embedding call entirely. A miss falls
through to L2, an embedding classifier whose confidence against the nearest
route decides the tier — or falls back to a conservative default when it
isn't sure. Every decision is logged for later replay and eval.

#### Two build modes

L2 needs the ONNX Runtime CGo binding; L1 doesn't, so there are two build
modes:

- **`make build`** (default, what `make docker` uses) — no cgo dependency;
  L1-only, and every miss falls to the default tier rather than crashing.
- **`make build-router`** (after `make setup`) — adds the real L2 embedding
  classifier.

`make run`/`make test` pick up L2 automatically once `make setup` has run,
degrading the same way otherwise.

### Reliability & observability

Every outgoing call goes through a circuit breaker per **provider** (not per
tier), so several tiers sharing a provider account share its health state
too. A run of consecutive failures trips it open — the provider is skipped
fast instead of hanging every request — until a single probe after a
cooldown decides whether to close again.

A **routed** (`"auto"`) request also gets a fallback chain: on a transient
failure it climbs from its assigned tier toward more capable ones, then a
final passthrough safety net — never back down to something cheaper. A
**direct-named** request stays exactly one attempt: naming a specific model
gets you that model or an honest error, never a silent substitution.

`make demo` brings up a pre-provisioned Grafana dashboard (`:3000`,
admin/admin) alongside the proxy and Prometheus. `deploy/helm/autoroute/` is
a minimal Helm chart — health probes and Prometheus scrape annotations, no
Ingress/HPA/ServiceMonitor; bring your own if you need them:

```sh
helm lint deploy/helm/autoroute
helm install autoroute deploy/helm/autoroute
```

### Eval harness

`eval/` replays [RouterBench](https://huggingface.co/datasets/withmartian/routerbench)
— real prompts already scored for cost and correctness across 11 models —
through the same router code the proxy runs in production, not a
reimplementation that could quietly drift from what ships. No API keys
needed: it looks a routed prompt's chosen model up in RouterBench's own
table.

```sh
make eval-setup   # fetch RouterBench (~100MB) + convert pickle -> csv (needs python3/pip)
make eval         # replay it, write eval/RESULTS.md + eval/RESULTS_chart.svg + eval/results.json
```

Rows are split by benchmark category so nothing is tuned against its own
eval set, and a weekly CI job re-runs the comparison as a regression gate
against the committed baseline — a real drift is a human decision, never
something a bot quietly absorbs.

See [`eval/RESULTS.md`](eval/RESULTS.md) for the actual numbers.

### Shadow detector

A worse-but-well-formed cheap-tier answer trips no error or latency alarm —
nothing else catches it. For a sampled fraction of cheap-tier, non-streaming
responses: after the client already has its answer, replay the same prompt
against the frontier model in the background and score how different the
two are by embedding similarity — no second model load, no judge. A
Prometheus rule alerts if the rolling mean delta climbs too high.

```yaml
shadow:
  enabled: true
  sample_rate: 0.05     # fraction of eligible requests sampled
  alert_threshold: 0.15 # logged warning above this; also the Prometheus rule's threshold
```

Needs the same L2 embedder as routing, so like L2 confidence it's silently
disabled (logged, not fatal) in the default CGO-free build even with
`shadow.enabled: true`. Run `make run` after `make setup`, or `make
build-router`, to see it fire for real.

## The embedding router spike

The routing brain — an ONNX embedding model running inside the same binary
as the proxy (no sidecar process, no embedding API call) plus
nearest-centroid classification over route exemplars — was prototyped
separately first, before `cmd/autoroute` existed, to de-risk it in
isolation:

```sh
make setup   # fetch ONNX Runtime + all-MiniLM-L6-v2 (~150 MB, into gitignored dirs)
make spike   # embed the worked-example prompts, print routing decisions + latency
```

## License

[Apache-2.0](LICENSE).
