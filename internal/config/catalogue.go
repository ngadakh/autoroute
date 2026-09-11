// Package config loads the model catalogue: the set of upstream providers and
// the client-facing model names that map onto them, plus per-model pricing used
// later for cost accounting. In M1 there is no routing — a request names a
// catalogue model directly and the proxy forwards it.
package config

import (
	"fmt"
	"os"

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
	Providers map[string]*Provider `yaml:"providers"`
	Models    []Model              `yaml:"models"`

	byModelName map[string]Model
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
