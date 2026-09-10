package proxy_test

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ngadakh/autoroute/internal/config"
	"github.com/ngadakh/autoroute/internal/observability"
	"github.com/ngadakh/autoroute/internal/provider"
	"github.com/ngadakh/autoroute/internal/proxy"
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
	return proxy.New(cat, providers, observability.New("test"), nil, "test")
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
