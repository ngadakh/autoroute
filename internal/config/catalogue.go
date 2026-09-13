// Package config loads the model catalogue: the set of upstream providers and
// the client-facing model names that map onto them, plus per-model pricing used
// later for cost accounting. In M1 there is no routing — a request names a
// catalogue model directly and the proxy forwards it.
package config

import (
	"fmt"
	"os"
	"time"

	"gopkg.in/yaml.v3"
)

// ProviderType selects a provider adapter implementation.
type ProviderType string

const (
	ProviderOpenAI ProviderType = "openai" // any OpenAI-compatible /v1 endpoint
	ProviderMock   ProviderType = "mock"   // in-process synthetic responses, no network
)

// Provider is one upstream API.
type Provider struct {
	Name      string       `yaml:"-"`
	Type      ProviderType `yaml:"type"`
	BaseURL   string       `yaml:"base_url"`    // openai only, e.g. https://api.openai.com/v1
	APIKeyEnv string       `yaml:"api_key_env"` // openai only, name of the env var holding the key
}

// Model is a client-facing entry. Clients send its Name as the request "model";
// the proxy rewrites that to Upstream and sends it to Provider.
type Model struct {
	Name            string  `yaml:"name"`
	Provider        string  `yaml:"provider"`
	Upstream        string  `yaml:"upstream"`
	PricePerMTokIn  float64 `yaml:"price_per_mtok_in"`
	PricePerMTokOut float64 `yaml:"price_per_mtok_out"`
}

// Catalogue is the whole config file.
type Catalogue struct {
	Providers   map[string]*Provider `yaml:"providers"`
	Models      []Model              `yaml:"models"`
	Router      *RouterConfig        `yaml:"router"`
	Reliability *ReliabilityConfig   `yaml:"reliability"`

	byModelName map[string]Model
}

// RouterConfig configures the M2 layered router. A nil Catalogue.Router (the
// key absent from YAML) means routing is off entirely: TriggerModel is simply
// an unknown model name, same as any other typo — identical to M1 behaviour.
type RouterConfig struct {
	Enabled            bool              `yaml:"enabled"`
	TriggerModel       string            `yaml:"trigger_model"`       // client-facing "model" that opts into routing
	DefaultTier        string            `yaml:"default_tier"`        // conservative default / M2 degrade-to-passthrough target
	PassthroughDefault string            `yaml:"passthrough_default"` // M3: 4th rung of the fallback chain, after cheap->mid->frontier
	ThetaLow           float64           `yaml:"theta_low"`
	ThetaHigh          float64           `yaml:"theta_high"`
	Tiers              map[string]string `yaml:"tiers"` // tier name -> catalogue model name
	Embedding          EmbeddingConfig   `yaml:"embedding"`
}

// ReliabilityConfig tunes the per-provider circuit breakers (internal/reliability).
// Applies regardless of whether the router is enabled — even a direct-named
// request benefits from a provider's breaker failing fast. A nil
// Catalogue.Reliability (key absent) gets these same defaults; see
// (*Catalogue).finalise.
type ReliabilityConfig struct {
	BreakerFailureThreshold int    `yaml:"breaker_failure_threshold"`
	BreakerCooldown         string `yaml:"breaker_cooldown"` // Go duration syntax, e.g. "30s"

	cooldown time.Duration // parsed from BreakerCooldown by validate
}

const (
	defaultBreakerFailureThreshold = 5
	defaultBreakerCooldown         = 30 * time.Second
)

// Cooldown returns the parsed breaker cooldown. Only valid after Load
// succeeds (validate populates it, applying the default if unset).
func (r *ReliabilityConfig) Cooldown() time.Duration { return r.cooldown }

func (r *ReliabilityConfig) validate() error {
	if r.BreakerFailureThreshold <= 0 {
		r.BreakerFailureThreshold = defaultBreakerFailureThreshold
	}
	if r.BreakerCooldown == "" {
		r.cooldown = defaultBreakerCooldown
		return nil
	}
	d, err := time.ParseDuration(r.BreakerCooldown)
	if err != nil {
		return fmt.Errorf("reliability.breaker_cooldown %q: %w", r.BreakerCooldown, err)
	}
	if d <= 0 {
		return fmt.Errorf("reliability.breaker_cooldown must be positive, got %q", r.BreakerCooldown)
	}
	r.cooldown = d
	return nil
}

// EmbeddingConfig locates the ONNX model, tokenizer and runtime for the L2
// classifier. Leaving ModelPath/VocabPath empty keeps L2 disabled even in a
// cgo build — the router then runs L1-only, resolving every L1 miss straight
// to DefaultTier (the degrade-to-passthrough path).
type EmbeddingConfig struct {
	ModelPath         string `yaml:"model_path"`
	VocabPath         string `yaml:"vocab_path"`
	MaxSeqLen         int    `yaml:"max_seq_len"`
	SharedLibraryPath string `yaml:"shared_library_path"`
}

// Load reads and validates a catalogue YAML file.
func Load(path string) (*Catalogue, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read catalogue: %w", err)
	}
	var c Catalogue
	if err := yaml.Unmarshal(raw, &c); err != nil {
		return nil, fmt.Errorf("parse catalogue: %w", err)
	}
	if err := c.finalise(); err != nil {
		return nil, err
	}
	return &c, nil
}

func (c *Catalogue) finalise() error {
	if len(c.Models) == 0 {
		return fmt.Errorf("catalogue has no models")
	}
	c.byModelName = make(map[string]Model, len(c.Models))

	for name, p := range c.Providers {
		p.Name = name
		switch p.Type {
		case ProviderOpenAI:
			if p.BaseURL == "" {
				return fmt.Errorf("provider %q: base_url is required for type openai", name)
			}
		case ProviderMock:
			// nothing to validate
		default:
			return fmt.Errorf("provider %q: unknown type %q", name, p.Type)
		}
	}

	for _, m := range c.Models {
		if m.Name == "" || m.Provider == "" || m.Upstream == "" {
			return fmt.Errorf("model %+v: name, provider and upstream are required", m)
		}
		if _, ok := c.Providers[m.Provider]; !ok {
			return fmt.Errorf("model %q references unknown provider %q", m.Name, m.Provider)
		}
		if _, dup := c.byModelName[m.Name]; dup {
			return fmt.Errorf("duplicate model name %q", m.Name)
		}
		c.byModelName[m.Name] = m
	}

	if c.Router != nil {
		if err := c.Router.validate(c.byModelName); err != nil {
			return err
		}
	}

	if c.Reliability == nil {
		c.Reliability = &ReliabilityConfig{}
	}
	if err := c.Reliability.validate(); err != nil {
		return err
	}
	return nil
}

func (r *RouterConfig) validate(models map[string]Model) error {
	if !r.Enabled {
		return nil
	}
	if r.TriggerModel == "" {
		return fmt.Errorf("router.trigger_model is required when router.enabled is true")
	}
	if _, collide := models[r.TriggerModel]; collide {
		return fmt.Errorf("router.trigger_model %q collides with a catalogue model name", r.TriggerModel)
	}
	if r.ThetaLow < 0 || r.ThetaHigh > 1 || r.ThetaLow > r.ThetaHigh {
		return fmt.Errorf("router thresholds must satisfy 0 <= theta_low <= theta_high <= 1, got %v/%v", r.ThetaLow, r.ThetaHigh)
	}
	if len(r.Tiers) == 0 {
		return fmt.Errorf("router.tiers must map at least one tier to a catalogue model")
	}
	if _, ok := r.Tiers[r.DefaultTier]; r.DefaultTier == "" || !ok {
		return fmt.Errorf("router.default_tier %q must be a key in router.tiers", r.DefaultTier)
	}
	for tier, modelName := range r.Tiers {
		if _, ok := models[modelName]; !ok {
			return fmt.Errorf("router.tiers[%q] = %q: no such catalogue model", tier, modelName)
		}
	}
	if r.PassthroughDefault == "" {
		return fmt.Errorf("router.passthrough_default is required when router.enabled is true")
	}
	if _, ok := models[r.PassthroughDefault]; !ok {
		return fmt.Errorf("router.passthrough_default %q: no such catalogue model", r.PassthroughDefault)
	}
	return nil
}

// Lookup returns the catalogue model for a client-facing name.
func (c *Catalogue) Lookup(name string) (Model, bool) {
	m, ok := c.byModelName[name]
	return m, ok
}

// ModelNames lists every client-facing model name.
func (c *Catalogue) ModelNames() []string {
	out := make([]string, 0, len(c.Models))
	for _, m := range c.Models {
		out = append(out, m.Name)
	}
	return out
}
