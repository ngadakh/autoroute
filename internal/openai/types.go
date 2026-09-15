// Package openai holds the small slice of the OpenAI chat-completions schema the
// proxy needs. AutoRoute is a relay, not a re-implementation: it parses only the
// fields it acts on and otherwise forwards request and response bytes untouched.
package openai

import (
	"encoding/json"
	"fmt"
)

// Message is one chat turn.
type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// Request is the subset of POST /v1/chat/completions AutoRoute inspects.
type Request struct {
	Model          string            `json:"model"`
	Messages       []Message         `json:"messages"`
	Stream         bool              `json:"stream"`
	Tools          []json.RawMessage `json:"tools,omitempty"`
	ResponseFormat json.RawMessage   `json:"response_format,omitempty"`
}

// Peek parses just the fields the proxy routes on, without discarding the rest.
// It returns the parsed Request plus the original body for forwarding.
func Peek(body []byte) (Request, error) {
	var r Request
	if err := json.Unmarshal(body, &r); err != nil {
		return Request{}, fmt.Errorf("invalid chat completion request: %w", err)
	}
	if r.Model == "" {
		return Request{}, fmt.Errorf("request is missing \"model\"")
	}
	if len(r.Messages) == 0 {
		return Request{}, fmt.Errorf("request has no messages")
	}
	return r, nil
}

// WithModel returns body with its top-level "model" field replaced by upstream,
// leaving every other field byte-identical.
func WithModel(body []byte, upstream string) ([]byte, error) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(body, &fields); err != nil {
		return nil, err
	}
	m, err := json.Marshal(upstream)
	if err != nil {
		return nil, err
	}
	fields["model"] = m
	return json.Marshal(fields)
}

// LastUserMessage returns the content of the final user turn — what the L1/L2
// router classifies. Empty string if there is none.
func (r Request) LastUserMessage() string {
	for i := len(r.Messages) - 1; i >= 0; i-- {
		if r.Messages[i].Role == "user" {
			return r.Messages[i].Content
		}
	}
	return ""
}

// HasTools reports whether the request declares any function/tool definitions
// — an L1 routing signal (tool-calling requests skip the cheap tier).
func (r Request) HasTools() bool { return len(r.Tools) > 0 }

// responseFormat is the tiny slice of the OpenAI response_format object the
// router needs: just whether the client structurally demands JSON.
type responseFormat struct {
	Type string `json:"type"`
}

// WantsStructuredJSON reports whether response_format asks for JSON output.
// This is a structural signal (the client set a schema constraint), distinct
// from a prompt that merely mentions "json" in free text — the latter is left
// to the L2 "structured-extraction" route so the two don't overlap.
func (r Request) WantsStructuredJSON() bool {
	if len(r.ResponseFormat) == 0 {
		return false
	}
	var rf responseFormat
	if err := json.Unmarshal(r.ResponseFormat, &rf); err != nil {
		return false
	}
	return rf.Type == "json_object" || rf.Type == "json_schema"
}

// --- response types (used by the mock provider; real upstreams are relayed raw) ---

// Response is a non-streaming chat completion.
type Response struct {
	ID      string   `json:"id"`
	Object  string   `json:"object"`
	Created int64    `json:"created"`
	Model   string   `json:"model"`
	Choices []Choice `json:"choices"`
	Usage   Usage    `json:"usage"`
}

// Choice is one completion in a Response.
type Choice struct {
	Index        int     `json:"index"`
	Message      Message `json:"message"`
	FinishReason string  `json:"finish_reason"`
}

// Usage is the token accounting block.
type Usage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

// Answer extracts the first choice's message content from a non-streaming
// chat-completion response body — used by the M5 shadow detector to pull
// comparable text out of both the cheap and frontier responses. Reports
// false for malformed JSON or a response with no choices, rather than
// erroring: the caller (a best-effort shadow sample) just skips scoring.
func Answer(body []byte) (string, bool) {
	var resp Response
	if err := json.Unmarshal(body, &resp); err != nil {
		return "", false
	}
	if len(resp.Choices) == 0 {
		return "", false
	}
	return resp.Choices[0].Message.Content, true
}
