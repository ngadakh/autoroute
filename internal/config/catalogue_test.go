package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
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

const baseModelsAndProviders = `
providers: {mock: {type: mock}}
models:
  - {name: fast, provider: mock, upstream: mock-fast}
  - {name: smart, provider: mock, upstream: mock-smart}
`

func TestLoadRouterValid(t *testing.T) {
	c, err := Load(write(t, baseModelsAndProviders+`
router:
  enabled: true
  trigger_model: auto
  default_tier: frontier
  passthrough_default: smart
  theta_low: 0.35
  theta_high: 0.7
  tiers: {cheap: fast, frontier: smart}
`))
	if err != nil {
		t.Fatal(err)
	}
	if c.Router == nil || !c.Router.Enabled || c.Router.TriggerModel != "auto" {
		t.Fatalf("router = %+v", c.Router)
	}
	if c.Router.Tiers["frontier"] != "smart" {
		t.Fatalf("tiers = %+v", c.Router.Tiers)
	}
	if c.Router.PassthroughDefault != "smart" {
		t.Fatalf("passthrough_default = %q, want smart", c.Router.PassthroughDefault)
	}
}

func TestLoadRouterAbsentMeansDisabled(t *testing.T) {
	c, err := Load(write(t, baseModelsAndProviders))
	if err != nil {
		t.Fatal(err)
	}
	if c.Router != nil {
		t.Fatalf("router = %+v, want nil when the key is absent", c.Router)
	}
}

func TestLoadRouterRejects(t *testing.T) {
	cases := map[string]string{
		"trigger_model missing": baseModelsAndProviders + `
router: {enabled: true, default_tier: cheap, tiers: {cheap: fast}}`,
		"trigger_model collides with a real model": baseModelsAndProviders + `
router: {enabled: true, trigger_model: fast, default_tier: cheap, tiers: {cheap: fast}}`,
		"default_tier not in tiers": baseModelsAndProviders + `
router: {enabled: true, trigger_model: auto, default_tier: frontier, tiers: {cheap: fast}}`,
		"tier references unknown model": baseModelsAndProviders + `
router: {enabled: true, trigger_model: auto, default_tier: cheap, tiers: {cheap: ghost}}`,
		"theta_low greater than theta_high": baseModelsAndProviders + `
router: {enabled: true, trigger_model: auto, default_tier: cheap, theta_low: 0.8, theta_high: 0.2, tiers: {cheap: fast}}`,
		"no tiers": baseModelsAndProviders + `
router: {enabled: true, trigger_model: auto, default_tier: cheap, tiers: {}}`,
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := Load(write(t, body)); err == nil {
				t.Fatal("expected an error, got nil")
			}
		})
	}
}

func TestLoadRouterDisabledSkipsValidation(t *testing.T) {
	// enabled: false (the default) means a broken tiers map isn't an error —
	// the trigger model just won't resolve to anything.
	if _, err := Load(write(t, baseModelsAndProviders+`
router: {trigger_model: fast, tiers: {cheap: ghost}}
`)); err != nil {
		t.Fatalf("disabled router should skip validation: %v", err)
	}
}

func TestLoadRouterRejectsMissingOrUnknownPassthroughDefault(t *testing.T) {
	cases := map[string]string{
		"missing": baseModelsAndProviders + `
router: {enabled: true, trigger_model: auto, default_tier: cheap, tiers: {cheap: fast}}`,
		"unknown model": baseModelsAndProviders + `
router: {enabled: true, trigger_model: auto, default_tier: cheap, passthrough_default: ghost, tiers: {cheap: fast}}`,
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := Load(write(t, body)); err == nil {
				t.Fatal("expected an error, got nil")
			}
		})
	}
}

func TestLoadReliabilityDefaults(t *testing.T) {
	c, err := Load(write(t, baseModelsAndProviders))
	if err != nil {
		t.Fatal(err)
	}
	if c.Reliability == nil {
		t.Fatal("reliability should default to a non-nil config, even when the key is absent")
	}
	if c.Reliability.BreakerFailureThreshold != defaultBreakerFailureThreshold {
		t.Fatalf("threshold = %d, want default %d", c.Reliability.BreakerFailureThreshold, defaultBreakerFailureThreshold)
	}
	if c.Reliability.Cooldown() != defaultBreakerCooldown {
		t.Fatalf("cooldown = %v, want default %v", c.Reliability.Cooldown(), defaultBreakerCooldown)
	}
}

func TestLoadReliabilityCustom(t *testing.T) {
	c, err := Load(write(t, baseModelsAndProviders+`
reliability: {breaker_failure_threshold: 10, breaker_cooldown: 1m30s}
`))
	if err != nil {
		t.Fatal(err)
	}
	if c.Reliability.BreakerFailureThreshold != 10 {
		t.Fatalf("threshold = %d, want 10", c.Reliability.BreakerFailureThreshold)
	}
	if want := 90 * time.Second; c.Reliability.Cooldown() != want {
		t.Fatalf("cooldown = %v, want %v", c.Reliability.Cooldown(), want)
	}
}

func TestLoadReliabilityRejectsBadCooldown(t *testing.T) {
	if _, err := Load(write(t, baseModelsAndProviders+`
reliability: {breaker_cooldown: "not a duration"}
`)); err == nil {
		t.Fatal("expected an error for an unparseable breaker_cooldown")
	}
	if _, err := Load(write(t, baseModelsAndProviders+`
reliability: {breaker_cooldown: "-5s"}
`)); err == nil {
		t.Fatal("expected an error for a non-positive breaker_cooldown")
	}
}
