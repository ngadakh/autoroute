# Building a model router that admits what it doesn't know

Every LLM routing project makes the same pitch: send easy prompts to a cheap
model, hard ones to a frontier model, pocket the savings. The pitch is true.
It's also not the hard part. The hard part is everything a demo skips: what
happens when the classifier is unsure, what happens when a provider starts
failing, and what happens when the cheap model gives a confident, well-formed,
*wrong* answer that trips no error and no latency alarm.

AutoRoute is an OpenAI-compatible proxy built to answer those questions
honestly rather than assume them away. This is the build story, milestone by
milestone, including the results that didn't flatter it.

## M0–M1: a spike, then a proxy that does nothing clever

The first milestone wasn't the router — it was proving the routing *brain*
could work at all, in isolation, before wiring it into anything a client would
depend on. `SPIKE.md` embeds a handful of worked-example prompts with an
in-process ONNX model (all-MiniLM-L6-v2, no network call, no external
embedding API) and checks that nearest-centroid classification separates
"what's 2+2" from "refactor this distributed cache eviction policy" the way
you'd expect. Cheap to fail fast on, if it hadn't worked.

M1 was the opposite kind of milestone: an OpenAI-compatible
`/v1/chat/completions` proxy that does the boring stuff right — forwards
requests, relays SSE streams byte-for-byte, exposes `/healthz`/`/readyz`, and
emits Prometheus metrics — with a **fixed model catalogue** and no routing
logic at all. Every request names its model directly. The point of shipping
this before any routing existed: a production proxy has to be correct at the
plumbing layer regardless of what decides the model name, and that layer is
easier to get right when it isn't also carrying classifier logic.

## M2: two layers, and a router that knows when it's unsure

The router that shipped in M2 is deliberately boring at the top and
statistical only when it has to be:

- **L1 heuristics** look at structural signals — token count, code fences,
  task verbs, turn count, tool declarations, JSON response format — and
  decide with certainty when they can. A one-line factual question never
  needs an embedding call.
- **L2** is the fallback: the in-process ONNX classifier from M0, now wired
  into the request path. It scores confidence against the nearest route, and
  two thresholds (`theta_low`/`theta_high`) decide what happens with that
  score — confident, it picks a tier; uncertain, it falls to
  `router.default_tier`.

That second bullet is the actual design decision, and it's the one that
determines everything downstream: **the uncertain middle band always resolves
to the conservative default.** Not a coin flip, not "lean cheap to save
money" — lean toward the tier you'd pick if you had to guess blind. A router
that fails open toward cost is a router that occasionally embarrasses you in
front of a user; a router that fails open toward quality just costs a bit
more, sometimes. That tradeoff is a one-line config value
(`router.default_tier`), not a hardcoded belief, but the shipped default
takes the quality side.

There's also a `CGO_ENABLED=0` build mode that drops L2 entirely and runs
L1-only, falling through to `default_tier` on every L1 miss — the same
degrade path, just permanently instead of per-request. That wasn't a nice-to-
have added later; it's why L1/L2 are cleanly separable packages instead of
one blob, from the start.

## M3: the part that isn't routing at all

M3 shipped no new routing logic. It shipped what happens when routing logic's
dependencies aren't healthy: a circuit breaker per **provider** (several
tiers sharing an account share its health state, deliberately), and — for
`"auto"`-routed requests specifically — a fallback chain that climbs from the
assigned tier toward more capable ones on a transient failure, never back
down to something cheaper. A direct-named request stays exactly one attempt:
naming a specific model should get you that model or an honest error, never a
silent substitution to something you didn't ask for.

This is the milestone that's easy to skip in a routing project and expensive
to skip in a production one. Grafana dashboards, a minimal Helm chart, every
fallback hop and breaker trip as its own metric — none of it is interesting
in a blog post, all of it is the difference between "works in the demo" and
"survives an upstream outage."

## M4: running the numbers, and not liking all of them

By M4 the honest question was unavoidable: does any of this actually save
money without giving up accuracy? `eval/` replays
[RouterBench](https://huggingface.co/datasets/withmartian/routerbench) — 36,497
real prompts across 86 benchmark categories, 11 models already scored for
cost and correctness — through the **exact same** `RouteL1` → `RouteL2` path
that production uses. Not a re-implementation that could quietly drift; the
harness imports the real router package.

The held-out split (4,436 rows the router's thresholds were never tuned
against): **2.5% cheaper than always calling the frontier model, at 80.4%
accuracy versus 81.4%.** That's a real, if modest, win — and it comes with a
number that's less flattering: **L1 heuristics decided 0.0% of held-out
rows**, and **95.6% landed in the conservative default** rather than a
confident L2 tier pick.

That's not a harness bug. RouterBench is academic-exam-style prompts — MMLU,
GSM8K, Winogrande — and the L1 rules and L2 exemplars were authored in M0–M2
for general assistant chat. They don't confidently discriminate this
benchmark's traffic, so the router does exactly what M2 designed it to do
when uncertain: falls to the conservative default and pays the frontier-model
price. `eval/RESULTS.md` says this plainly instead of picking a rosier
number to headline, and `.github/workflows/eval.yml` re-runs the same
comparison weekly as a regression gate — it fails if the held-out numbers
drift from the committed baseline beyond a fixed tolerance, and it never
commits a new result back on its own. A real drift is a decision a person
makes, not something a bot quietly absorbs into main.

The honest framing, in the harness's own words: routing saved money "at the
price of not calling the frontier model for every prompt" — which is either
obviously true or the entire point, depending on how many routing pitches
you've already read.

## M5: catching the failure mode that trips no alarm

Cost and latency dashboards catch a slow model or a failing provider
immediately. They do not catch a cheap-tier model that returns a fast,
cheap, well-formed, *wrong* answer — nothing about that response looks
unhealthy. `docs/ARCHITECTURE.md` names this directly as "the metric every
commercial router quietly fails."

The shadow detector: sample a small fraction of router-decided,
non-streaming cheap-tier responses, and — after the client already has their
answer — replay the same prompt against the frontier model in a background
goroutine that can't block or affect the real request. Score the two answers
with `1 - cosine(embed(cheap), embed(frontier))`, using the same in-process
ONNX embedder already loaded for L2 routing. No second model load, and no L3
judge model — the architecture doc's prose mentions "embedding similarity +
judge," but no judge exists anywhere in this codebase, and embedding
similarity alone is what M5 actually ships. `autoroute_shadow_quality_delta`
records every sample; a Prometheus rule fires if its rolling 10-minute mean
exceeds 0.15 for five minutes straight, visible in Prometheus's own alerts
UI. No Alertmanager wiring — bring your own notification channel, the same
stance M3 already took on Ingress and HPA.

## What this project is actually arguing

Routing to save money is the easy 80%. The other 20% — degrading instead of
crashing when a build has no embedder, climbing instead of falling back to
something cheaper on a transient failure, publishing a real dataset's numbers
even when 95.6% of them land in the boring default path, catching the answer
that's wrong instead of just the one that's slow — is what makes a router
something you'd actually run. AutoRoute is an attempt to build that second
part in the open, with numbers anyone can re-run
(`make eval-setup && make eval`) rather than take on faith.

See [`docs/ARCHITECTURE.md`](ARCHITECTURE.md) for the full design and
[`eval/RESULTS.md`](../eval/RESULTS.md) for the actual eval output this post
draws from.
