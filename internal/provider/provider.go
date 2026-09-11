// Package provider adapts upstream chat-completions APIs to a single interface
// the proxy can relay through. Adapters are deliberately thin: they perform the
// upstream call and hand back the raw HTTP response for the caller to stream to
// the client. Routing, retries and circuit breaking live above this layer.
package provider

import (
	"context"
	"fmt"
	"net/http"

	"github.com/ngadakh/autoroute/internal/config"
)

// Provider performs one upstream chat-completions request.
type Provider interface {
	// Name is the catalogue provider name (for logs and metrics).
	Name() string
	// Do sends body (already model-rewritten, may set "stream": true) to the
	// upstream and returns its HTTP response. The caller relays status, the
	// relevant headers and the body/stream, and must Close the body.
	Do(ctx context.Context, body []byte) (*http.Response, error)
}

// Set is the collection of live providers, keyed by catalogue name.
type Set map[string]Provider

// Build constructs a provider for every entry in the catalogue. httpClient is
// shared by all OpenAI-type providers; pass nil for http.DefaultClient.
func Build(cat *config.Catalogue, httpClient *http.Client) (Set, error) {
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	set := make(Set, len(cat.Providers))
	for name, p := range cat.Providers {
		switch p.Type {
		case config.ProviderOpenAI:
			set[name] = newOpenAI(p, httpClient)
		case config.ProviderMock:
			set[name] = newMock(name)
		default:
			return nil, fmt.Errorf("provider %q: unsupported type %q", name, p.Type)
		}
	}
	return set, nil
}

// Get returns the provider for a catalogue name.
func (s Set) Get(name string) (Provider, bool) {
	p, ok := s[name]
	return p, ok
}
