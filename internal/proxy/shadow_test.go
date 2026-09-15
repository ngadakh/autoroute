package proxy_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/ngadakh/autoroute/internal/embed"
	"github.com/ngadakh/autoroute/internal/router"
	"github.com/ngadakh/autoroute/internal/shadow"
)

// shadowCatalogue: cheap and frontier tiers point at two different mock
// providers (mock.go's reply text includes the provider name), so their
// echoed answers are genuinely different strings — enough to drive a real,
// non-trivial shadow delta rather than always comparing identical text.
const shadowCatalogue = `
providers:
  cheapprov: {type: mock}
  frontierprov: {type: mock}
models:
  - {name: fast, provider: cheapprov, upstream: mock-fast}
  - {name: genius, provider: frontierprov, upstream: mock-genius}
router:
  enabled: true
  trigger_model: auto
  default_tier: frontier
  passthrough_default: genius
  theta_low: 0.2
  theta_high: 0.6
  tiers: {cheap: fast, frontier: genius}
`

const shadowPrompt = "Who is the prime minister of India?"

// oneHot returns an embed.Dim-length vector with a single 1 at index i —
// two different indices are orthogonal, giving Score() a delta of exactly 1.
func oneHot(i int) []float32 {
	v := make([]float32, embed.Dim)
	v[i] = 1
	return v
}

func pollMetricsContains(t *testing.T, url, want string, timeout time.Duration) bool {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		resp, err := http.Get(url)
		if err == nil {
			b, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			if strings.Contains(string(b), want) {
				return true
			}
		}
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestShadowSamplesCheapRoutedNonStreamingRequest(t *testing.T) {
	s := build(t, shadowCatalogue)
	s.Router = router.NewPipeline(nil, nil, 0.2, 0.6, router.TierFrontier)
	s.TierModels = map[router.Tier]string{router.TierCheap: "fast", router.TierFrontier: "genius"}
	s.TriggerModel = "auto"
	s.PassthroughDefault = "genius"

	cheapAnswer := "[mock:cheapprov] echo: " + shadowPrompt
	frontierAnswer := "[mock:frontierprov] echo: " + shadowPrompt
	s.Shadow = &shadow.Sampler{Rate: 1, Embedder: fakeEmbedder{
		cheapAnswer:    oneHot(0),
		frontierAnswer: oneHot(1),
	}}
	s.ShadowAlertThreshold = 0.15

	s.SetReady(true)
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()

	body := `{"model":"auto","messages":[{"role":"user","content":"` + shadowPrompt + `"}]}`
	resp, err := http.Post(ts.URL+"/v1/chat/completions", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("client response status = %d, want 200 (shadow sampling must never affect the real response)", resp.StatusCode)
	}
	got, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(got), cheapAnswer) {
		t.Fatalf("client response = %s, want the cheap tier's answer unaffected by shadowing", got)
	}

	// The shadow call is async (its own goroutine) — poll briefly.
	if !pollMetricsContains(t, ts.URL+"/metrics", "autoroute_shadow_quality_delta_count 1", 2*time.Second) {
		metrics, _ := io.ReadAll(mustGet(t, ts.URL+"/metrics"))
		t.Fatalf("expected exactly one shadow sample recorded within 2s:\n%s", metrics)
	}
	// Orthogonal one-hot vectors -> delta exactly 1 -> the _sum equals the count.
	if !pollMetricsContains(t, ts.URL+"/metrics", "autoroute_shadow_quality_delta_sum 1", 100*time.Millisecond) {
		t.Fatal("expected shadow_quality_delta_sum = 1 (orthogonal embeddings -> delta 1)")
	}
}

func TestShadowRateZeroNeverSamples(t *testing.T) {
	s := build(t, shadowCatalogue)
	s.Router = router.NewPipeline(nil, nil, 0.2, 0.6, router.TierFrontier)
	s.TierModels = map[router.Tier]string{router.TierCheap: "fast", router.TierFrontier: "genius"}
	s.TriggerModel = "auto"
	s.PassthroughDefault = "genius"
	s.Shadow = &shadow.Sampler{Rate: 0, Embedder: fakeEmbedder{}}

	s.SetReady(true)
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()

	body := `{"model":"auto","messages":[{"role":"user","content":"` + shadowPrompt + `"}]}`
	if code := post(t, ts.URL+"/v1/chat/completions", body); code != 200 {
		t.Fatalf("status = %d, want 200", code)
	}

	metrics, _ := io.ReadAll(mustGet(t, ts.URL+"/metrics"))
	if !strings.Contains(string(metrics), "autoroute_shadow_quality_delta_count 0") {
		t.Fatalf("expected zero shadow samples with Rate: 0, got:\n%s", metrics)
	}
}

func TestShadowSkipsStreamingRequests(t *testing.T) {
	s := build(t, shadowCatalogue)
	s.Router = router.NewPipeline(nil, nil, 0.2, 0.6, router.TierFrontier)
	s.TierModels = map[router.Tier]string{router.TierCheap: "fast", router.TierFrontier: "genius"}
	s.TriggerModel = "auto"
	s.PassthroughDefault = "genius"
	// Rate: 1 — if streaming exclusion weren't working, this would sample
	// every eligible request.
	s.Shadow = &shadow.Sampler{Rate: 1, Embedder: fakeEmbedder{}}

	s.SetReady(true)
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()

	body := `{"model":"auto","stream":true,"messages":[{"role":"user","content":"` + shadowPrompt + `"}]}`
	if code := post(t, ts.URL+"/v1/chat/completions", body); code != 200 {
		t.Fatalf("status = %d, want 200", code)
	}

	metrics, _ := io.ReadAll(mustGet(t, ts.URL+"/metrics"))
	if !strings.Contains(string(metrics), "autoroute_shadow_quality_delta_count 0") {
		t.Fatalf("expected zero shadow samples for a streaming request, got:\n%s", metrics)
	}
}
