package proxy_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ngadakh/autoroute/internal/config"
	"github.com/ngadakh/autoroute/internal/embed"
	"github.com/ngadakh/autoroute/internal/observability"
	"github.com/ngadakh/autoroute/internal/provider"
	"github.com/ngadakh/autoroute/internal/proxy"
	"github.com/ngadakh/autoroute/internal/reliability"
	"github.com/ngadakh/autoroute/internal/router"
)

func build(t *testing.T, catalogueYAML string) *proxy.Server {
	t.Helper()
	p := filepath.Join(t.TempDir(), "catalogue.yaml")
	if err := os.WriteFile(p, []byte(catalogueYAML), 0o600); err != nil {
		t.Fatal(err)
	}
	cat, err := config.Load(p)
	if err != nil {
		t.Fatal(err)
	}
	providers, err := provider.Build(cat, http.DefaultClient)
	if err != nil {
		t.Fatal(err)
	}
	metrics := observability.New("test")
	s := proxy.New(cat, providers, metrics, nil, "test")
	breakers := reliability.NewBreakers(cat.Reliability.BreakerFailureThreshold, cat.Reliability.Cooldown())
	s.Dispatcher = reliability.NewDispatcher(providers, breakers, metrics)
	return s
}

// newServer returns a running test server with readiness already flipped on.
func newServer(t *testing.T, catalogueYAML string) *httptest.Server {
	t.Helper()
	s := build(t, catalogueYAML)
	s.SetReady(true)
	ts := httptest.NewServer(s.Handler())
	t.Cleanup(ts.Close)
	return ts
}

const mockCatalogue = `
providers: {mock: {type: mock}}
models:
  - {name: fast, provider: mock, upstream: mock-fast}
  - {name: smart, provider: mock, upstream: mock-smart}
`

func TestHealthAndReady(t *testing.T) {
	ts := newServer(t, mockCatalogue)
	for _, path := range []string{"/healthz", "/readyz"} {
		resp, err := http.Get(ts.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != 200 {
			t.Fatalf("%s = %d", path, resp.StatusCode)
		}
	}
}

func TestReadyzReflectsDrain(t *testing.T) {
	s := build(t, mockCatalogue)
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()

	if code := get(t, ts.URL+"/readyz"); code != 503 {
		t.Fatalf("pre-ready /readyz = %d, want 503", code)
	}
	s.SetReady(true)
	if code := get(t, ts.URL+"/readyz"); code != 200 {
		t.Fatalf("ready /readyz = %d, want 200", code)
	}
	s.SetReady(false)
	if code := get(t, ts.URL+"/readyz"); code != 503 {
		t.Fatalf("draining /readyz = %d, want 503", code)
	}
}

func TestChatCompletionsMock(t *testing.T) {
	ts := newServer(t, mockCatalogue)
	body := `{"model":"fast","messages":[{"role":"user","content":"ping"}]}`
	resp, err := http.Post(ts.URL+"/v1/chat/completions", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	var got struct {
		Model   string `json:"model"`
		Choices []struct {
			Message struct{ Content string } `json:"message"`
		} `json:"choices"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if got.Model != "mock-fast" {
		t.Fatalf("model = %q, want mock-fast (upstream id)", got.Model)
	}
	if len(got.Choices) != 1 || !strings.Contains(got.Choices[0].Message.Content, "ping") {
		t.Fatalf("choices = %+v", got.Choices)
	}
}

func TestChatCompletionsStreaming(t *testing.T) {
	ts := newServer(t, mockCatalogue)
	body := `{"model":"smart","stream":true,"messages":[{"role":"user","content":"one two"}]}`
	resp, err := http.Post(ts.URL+"/v1/chat/completions", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/event-stream") {
		t.Fatalf("content-type = %q", ct)
	}
	raw, _ := io.ReadAll(resp.Body)
	out := string(raw)
	if !strings.Contains(out, `"delta":{"role":"assistant"}`) {
		t.Fatalf("missing role delta:\n%s", out)
	}
	if !strings.Contains(out, `"finish_reason":"stop"`) || !strings.HasSuffix(strings.TrimSpace(out), "data: [DONE]") {
		t.Fatalf("stream not terminated correctly:\n%s", out)
	}
}

func TestUnknownModelAndBadBody(t *testing.T) {
	ts := newServer(t, mockCatalogue)
	if code := post(t, ts.URL+"/v1/chat/completions",
		`{"model":"ghost","messages":[{"role":"user","content":"x"}]}`); code != 404 {
		t.Fatalf("unknown model = %d, want 404", code)
	}
	if code := post(t, ts.URL+"/v1/chat/completions", `{ not json`); code != 400 {
		t.Fatalf("bad body = %d, want 400", code)
	}
}

// TestOpenAIProviderForwarding checks the real relay path: model rewrite, auth
// header, and body pass-through to an OpenAI-compatible upstream.
func TestOpenAIProviderForwarding(t *testing.T) {
	t.Setenv("TEST_UPSTREAM_KEY", "sk-test-123")

	var gotAuth, gotModel, gotTemp string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat/completions" {
			t.Errorf("upstream path = %s", r.URL.Path)
		}
		gotAuth = r.Header.Get("Authorization")
		var payload map[string]any
		json.NewDecoder(r.Body).Decode(&payload)
		gotModel, _ = payload["model"].(string)
		gotTemp = fmt.Sprint(payload["temperature"])
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"id":"x","object":"chat.completion","choices":[]}`)
	}))
	defer upstream.Close()

	cat := fmt.Sprintf(`
providers:
  oai: {type: openai, base_url: %s, api_key_env: TEST_UPSTREAM_KEY}
models:
  - {name: relay, provider: oai, upstream: real-model-id}
`, upstream.URL)
	ts := newServer(t, cat)

	code := post(t, ts.URL+"/v1/chat/completions",
		`{"model":"relay","temperature":0.3,"messages":[{"role":"user","content":"hi"}]}`)
	if code != 200 {
		t.Fatalf("status = %d", code)
	}
	if gotAuth != "Bearer sk-test-123" {
		t.Fatalf("auth header = %q", gotAuth)
	}
	if gotModel != "real-model-id" {
		t.Fatalf("upstream model = %q, want real-model-id (rewritten)", gotModel)
	}
	if gotTemp != "0.3" {
		t.Fatalf("temperature not forwarded: %q", gotTemp)
	}
}

func TestUpstreamTransportErrorIs502(t *testing.T) {
	// base_url points at a port nothing listens on.
	cat := `
providers:
  dead: {type: openai, base_url: "http://127.0.0.1:1/v1", api_key_env: NOPE}
models:
  - {name: relay, provider: dead, upstream: m}
`
	ts := newServer(t, cat)
	if code := post(t, ts.URL+"/v1/chat/completions",
		`{"model":"relay","messages":[{"role":"user","content":"x"}]}`); code != http.StatusBadGateway {
		t.Fatalf("dead upstream = %d, want 502", code)
	}
}

const autoCatalogue = `
providers: {mock: {type: mock}}
models:
  - {name: fast, provider: mock, upstream: mock-fast}
  - {name: smart, provider: mock, upstream: mock-smart}
router:
  enabled: true
  trigger_model: auto
  default_tier: frontier
  passthrough_default: smart
  theta_low: 0.2
  theta_high: 0.6
  tiers: {cheap: fast, mid: smart, frontier: smart}
`

// fakeEmbedder returns a fixed vector for known text and errors otherwise —
// enough to drive router.Classifier in tests with no ONNX Runtime / cgo.
type fakeEmbedder map[string][]float32

func (f fakeEmbedder) Embed(text string) ([]float32, error) {
	if v, ok := f[text]; ok {
		return v, nil
	}
	return nil, errors.New("fakeEmbedder: no vector for text")
}

func TestAutoRoutingL1Decides(t *testing.T) {
	s := build(t, autoCatalogue)
	// No classifier wired at all — this prompt must resolve at L1, without
	// ever touching L2.
	s.Router = router.NewPipeline(nil, nil, 0.2, 0.6, router.TierFrontier)
	s.TierModels = map[router.Tier]string{router.TierCheap: "fast", router.TierMid: "smart", router.TierFrontier: "smart"}
	s.TriggerModel = "auto"
	s.PassthroughDefault = "smart"
	s.SetReady(true)
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()

	body := `{"model":"auto","messages":[{"role":"user","content":"What is the capital of France?"}]}`
	resp, err := http.Post(ts.URL+"/v1/chat/completions", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	var got struct {
		Model string `json:"model"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if got.Model != "mock-fast" {
		t.Fatalf("resolved upstream = %q, want mock-fast (cheap tier, L1 factual-lookup)", got.Model)
	}
}

func TestAutoRoutingDegradesWithoutClassifier(t *testing.T) {
	s := build(t, autoCatalogue)
	// Simulates the CGO_ENABLED=0 build: no classifier/embedder available.
	s.Router = router.NewPipeline(nil, nil, 0.2, 0.6, router.TierFrontier)
	s.TierModels = map[router.Tier]string{router.TierCheap: "fast", router.TierMid: "smart", router.TierFrontier: "smart"}
	s.TriggerModel = "auto"
	s.PassthroughDefault = "smart"
	var log bytes.Buffer
	s.DecisionLog = observability.NewDecisionLog(&log)
	s.SetReady(true)
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()

	// A prompt that doesn't match any L1 rule, so it must fall to L2 — which
	// is unavailable — and degrade to the default tier, never a 5xx.
	body := `{"model":"auto","messages":[{"role":"user","content":"walk me through the pros and cons of switching database engines"}]}`
	code := post(t, ts.URL+"/v1/chat/completions", body)
	if code != 200 {
		t.Fatalf("status = %d, want 200 (degrade-to-passthrough, not an error)", code)
	}
	if !strings.Contains(log.String(), `"layer":"degraded-no-l2"`) {
		t.Fatalf("decision log missing degraded entry: %s", log.String())
	}
}

func TestAutoRoutingL2ConfidentMatch(t *testing.T) {
	s := build(t, autoCatalogue)
	routes := []router.Route{
		{Name: "chit-chat", Tier: router.TierCheap, Exemplars: []string{"exemplar-cheap"}},
		{Name: "deep-reasoning", Tier: router.TierFrontier, Exemplars: []string{"exemplar-frontier"}},
	}
	oneHot := func(i int) []float32 {
		v := make([]float32, embed.Dim)
		v[i] = 1
		return v
	}
	emb := fakeEmbedder{
		"exemplar-cheap":    oneHot(0),
		"exemplar-frontier": oneHot(1),
		// This prompt doesn't match any L1 rule (long, no code, not a
		// question, no rewrite verb), so it reaches L2.
		"summarise the strategic tradeoffs of this multi year vendor contract renewal": oneHot(1),
	}
	clf, err := router.NewClassifier(emb, routes)
	if err != nil {
		t.Fatal(err)
	}
	s.Router = router.NewPipeline(clf, emb, 0.2, 0.6, router.TierCheap)
	s.TierModels = map[router.Tier]string{router.TierCheap: "fast", router.TierMid: "smart", router.TierFrontier: "smart"}
	s.TriggerModel = "auto"
	s.PassthroughDefault = "smart"
	s.SetReady(true)
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()

	body := `{"model":"auto","messages":[{"role":"user","content":"summarise the strategic tradeoffs of this multi year vendor contract renewal"}]}`
	resp, err := http.Post(ts.URL+"/v1/chat/completions", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var got struct {
		Model string `json:"model"`
	}
	json.NewDecoder(resp.Body).Decode(&got)
	if got.Model != "mock-smart" {
		t.Fatalf("resolved upstream = %q, want mock-smart (frontier tier via L2)", got.Model)
	}
}

// A direct model name still bypasses routing entirely, even with a router
// configured — M1-style passthrough stays unchanged.
func TestDirectModelNameSkipsRouting(t *testing.T) {
	s := build(t, autoCatalogue)
	s.Router = router.NewPipeline(nil, nil, 0.2, 0.6, router.TierFrontier)
	s.TierModels = map[router.Tier]string{router.TierCheap: "fast"}
	s.TriggerModel = "auto"
	s.PassthroughDefault = "fast"
	s.SetReady(true)
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()

	body := `{"model":"smart","messages":[{"role":"user","content":"What is the capital of France?"}]}`
	resp, err := http.Post(ts.URL+"/v1/chat/completions", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var got struct {
		Model string `json:"model"`
	}
	json.NewDecoder(resp.Body).Decode(&got)
	if got.Model != "mock-smart" {
		t.Fatalf("resolved upstream = %q, want mock-smart (direct name, unrouted)", got.Model)
	}
}

func TestMetricsExposed(t *testing.T) {
	ts := newServer(t, mockCatalogue)
	post(t, ts.URL+"/v1/chat/completions", `{"model":"fast","messages":[{"role":"user","content":"x"}]}`)

	resp, err := http.Get(ts.URL + "/metrics")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	for _, want := range []string{
		"autoroute_http_requests_total",
		"autoroute_upstream_requests_total",
		`autoroute_build_info{version="test"} 1`,
	} {
		if !strings.Contains(string(b), want) {
			t.Fatalf("metrics missing %q", want)
		}
	}
}

// deadAndAliveCatalogue: "fast" (cheap tier) points at a dead upstream, so an
// "auto" request that L1 routes to cheap must fall back to "smart" (mid tier,
// the mock provider) and still return 200.
const deadAndAliveCatalogue = `
providers:
  dead: {type: openai, base_url: "http://127.0.0.1:1/v1", api_key_env: NOPE}
  mock: {type: mock}
models:
  - {name: fast, provider: dead, upstream: m1}
  - {name: smart, provider: mock, upstream: mock-smart}
router:
  enabled: true
  trigger_model: auto
  default_tier: frontier
  passthrough_default: smart
  theta_low: 0.2
  theta_high: 0.6
  tiers: {cheap: fast, mid: smart, frontier: smart}
`

func TestAutoRoutingFallsBackAcrossTiersOnProviderFailure(t *testing.T) {
	s := build(t, deadAndAliveCatalogue)
	s.Router = router.NewPipeline(nil, nil, 0.2, 0.6, router.TierFrontier)
	s.TierModels = map[router.Tier]string{router.TierCheap: "fast", router.TierMid: "smart", router.TierFrontier: "smart"}
	s.TriggerModel = "auto"
	s.PassthroughDefault = "smart"
	s.SetReady(true)
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()

	// Short factual question -> L1 decides "cheap" -> "fast" -> dead upstream
	// -> must fall back to "smart" and still succeed.
	body := `{"model":"auto","messages":[{"role":"user","content":"What is the capital of France?"}]}`
	resp, err := http.Post(ts.URL+"/v1/chat/completions", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d, want 200 (fell back to a healthy tier)", resp.StatusCode)
	}
	var got struct {
		Model string `json:"model"`
	}
	json.NewDecoder(resp.Body).Decode(&got)
	if got.Model != "mock-smart" {
		t.Fatalf("resolved upstream = %q, want mock-smart (fell back from dead cheap tier)", got.Model)
	}

	metrics, _ := io.ReadAll(mustGet(t, ts.URL+"/metrics"))
	if !strings.Contains(string(metrics), `autoroute_fallback_total{from="fast",to="smart"}`) {
		t.Fatalf("metrics missing fallback counter:\n%s", metrics)
	}
	if !strings.Contains(string(metrics), "autoroute_circuit_breaker_state") {
		t.Fatalf("metrics missing breaker state gauge:\n%s", metrics)
	}
}

// allDeadCatalogue: every candidate a routed request could reach is dead.
const allDeadCatalogue = `
providers:
  dead1: {type: openai, base_url: "http://127.0.0.1:1/v1", api_key_env: NOPE}
  dead2: {type: openai, base_url: "http://127.0.0.1:2/v1", api_key_env: NOPE}
models:
  - {name: fast, provider: dead1, upstream: m1}
  - {name: smart, provider: dead2, upstream: m2}
router:
  enabled: true
  trigger_model: auto
  default_tier: frontier
  passthrough_default: smart
  theta_low: 0.2
  theta_high: 0.6
  tiers: {cheap: fast, mid: smart, frontier: smart}
`

func TestAutoRoutingAllCandidatesFailDegradesCleanly(t *testing.T) {
	s := build(t, allDeadCatalogue)
	s.Router = router.NewPipeline(nil, nil, 0.2, 0.6, router.TierFrontier)
	s.TierModels = map[router.Tier]string{router.TierCheap: "fast", router.TierMid: "smart", router.TierFrontier: "smart"}
	s.TriggerModel = "auto"
	s.PassthroughDefault = "smart"
	s.SetReady(true)
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()

	body := `{"model":"auto","messages":[{"role":"user","content":"What is the capital of France?"}]}`
	code := post(t, ts.URL+"/v1/chat/completions", body)
	if code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502 (every candidate dead — an honest failure, not a hang or panic)", code)
	}
}

func mustGet(t *testing.T, url string) io.ReadCloser {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	return resp.Body
}

// --- helpers ---

func get(t *testing.T, url string) int {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	return resp.StatusCode
}

func post(t *testing.T, url, body string) int {
	t.Helper()
	resp, err := http.Post(url, "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	return resp.StatusCode
}
