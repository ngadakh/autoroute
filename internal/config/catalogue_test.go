package config

import (
	"os"
	"path/filepath"
	"testing"
)

func write(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "catalogue.yaml")
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestLoadValid(t *testing.T) {
	c, err := Load(write(t, `
providers:
  mock: {type: mock}
  oai:
    type: openai
    base_url: https://api.example.com/v1/
    api_key_env: EXAMPLE_KEY
models:
  - {name: fast, provider: mock, upstream: mock-fast, price_per_mtok_in: 0.1, price_per_mtok_out: 0.4}
  - {name: gpt, provider: oai, upstream: gpt-4o-mini}
`))
	if err != nil {
		t.Fatal(err)
	}
	if got := c.ModelNames(); len(got) != 2 {
		t.Fatalf("model names = %v", got)
	}
	m, ok := c.Lookup("fast")
	if !ok || m.Upstream != "mock-fast" || m.PricePerMTokOut != 0.4 {
		t.Fatalf("lookup fast = %+v ok=%v", m, ok)
	}
	if c.Providers["oai"].Name != "oai" {
		t.Fatalf("provider name not backfilled: %+v", c.Providers["oai"])
	}
}

func TestLoadRejects(t *testing.T) {
	cases := map[string]string{
		"unknown provider": `
providers: {mock: {type: mock}}
models: [{name: x, provider: ghost, upstream: y}]`,
		"duplicate model": `
providers: {mock: {type: mock}}
models:
  - {name: x, provider: mock, upstream: a}
  - {name: x, provider: mock, upstream: b}`,
		"missing upstream": `
providers: {mock: {type: mock}}
models: [{name: x, provider: mock}]`,
		"unknown provider type": `
providers: {weird: {type: telepathy}}
models: [{name: x, provider: weird, upstream: y}]`,
		"openai without base_url": `
providers: {oai: {type: openai, api_key_env: K}}
models: [{name: x, provider: oai, upstream: y}]`,
		"no models": `providers: {mock: {type: mock}}`,
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := Load(write(t, body)); err == nil {
				t.Fatal("expected an error, got nil")
			}
		})
	}
}

func TestLoadMissingFile(t *testing.T) {
	if _, err := Load("/no/such/catalogue.yaml"); err == nil {
		t.Fatal("expected error for missing file")
	}
}
