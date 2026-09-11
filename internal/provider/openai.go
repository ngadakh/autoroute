package provider

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"os"
	"strings"

	"github.com/ngadakh/autoroute/internal/config"
)

// openAIProvider relays to any OpenAI-compatible /v1/chat/completions endpoint
// (OpenAI, Groq, Together, a local vLLM server, ...).
type openAIProvider struct {
	name    string
	baseURL string
	apiKey  string
	client  *http.Client
}

func newOpenAI(p *config.Provider, client *http.Client) *openAIProvider {
	return &openAIProvider{
		name:    p.Name,
		baseURL: strings.TrimRight(p.BaseURL, "/"),
		apiKey:  os.Getenv(p.APIKeyEnv),
		client:  client,
	}
}

func (o *openAIProvider) Name() string { return o.name }

func (o *openAIProvider) Do(ctx context.Context, body []byte) (*http.Response, error) {
	url := o.baseURL + "/chat/completions"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	if o.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+o.apiKey)
	}

	resp, err := o.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("upstream %s: %w", o.name, err)
	}
	return resp, nil
}
