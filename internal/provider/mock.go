package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/ngadakh/autoroute/internal/openai"
)

// mockProvider returns deterministic synthetic completions with no network call.
// It lets the proxy be run, tested and demoed without any provider API key.
type mockProvider struct{ name string }

func newMock(name string) *mockProvider { return &mockProvider{name: name} }

func (m *mockProvider) Name() string { return m.name }

func (m *mockProvider) Do(ctx context.Context, body []byte) (*http.Response, error) {
	req, err := openai.Peek(body)
	if err != nil {
		return jsonResponse(http.StatusBadRequest, map[string]any{
			"error": map[string]string{"message": err.Error(), "type": "invalid_request_error"},
		}), nil
	}

	reply := fmt.Sprintf("[mock:%s] echo: %s", m.name, truncate(req.LastUserMessage(), 200))
	promptTok := len(strings.Fields(req.LastUserMessage())) + 4
	replyTok := len(strings.Fields(reply))

	if req.Stream {
		return m.stream(ctx, req.Model, reply), nil
	}

	resp := openai.Response{
		ID:      "chatcmpl-mock-" + shortID(),
		Object:  "chat.completion",
		Created: time.Now().Unix(),
		Model:   req.Model,
		Choices: []openai.Choice{{
			Index:        0,
			Message:      openai.Message{Role: "assistant", Content: reply},
			FinishReason: "stop",
		}},
		Usage: openai.Usage{
			PromptTokens:     promptTok,
			CompletionTokens: replyTok,
			TotalTokens:      promptTok + replyTok,
		},
	}
	return jsonResponse(http.StatusOK, resp), nil
}

// stream emits the reply as OpenAI-style SSE chunks, one word at a time.
func (m *mockProvider) stream(ctx context.Context, model, reply string) *http.Response {
	pr, pw := io.Pipe()
	go func() {
		id := "chatcmpl-mock-" + shortID()
		created := time.Now().Unix()

		write := func(delta map[string]any, finish any) bool {
			chunk := map[string]any{
				"id": id, "object": "chat.completion.chunk",
				"created": created, "model": model,
				"choices": []map[string]any{{
					"index": 0, "delta": delta, "finish_reason": finish,
				}},
			}
			b, _ := json.Marshal(chunk)
			if _, err := fmt.Fprintf(pw, "data: %s\n\n", b); err != nil {
				return false
			}
			return true
		}

		if !write(map[string]any{"role": "assistant"}, nil) {
			return
		}
		for _, word := range strings.Fields(reply) {
			select {
			case <-ctx.Done():
				_ = pw.CloseWithError(ctx.Err())
				return
			case <-time.After(15 * time.Millisecond):
			}
			if !write(map[string]any{"content": word + " "}, nil) {
				return
			}
		}
		write(map[string]any{}, "stop")
		io.WriteString(pw, "data: [DONE]\n\n")
		_ = pw.Close()
	}()

	return &http.Response{
		StatusCode: http.StatusOK,
		Header: http.Header{
			"Content-Type":  {"text/event-stream"},
			"Cache-Control": {"no-cache"},
		},
		Body: pr,
	}
}

func jsonResponse(status int, v any) *http.Response {
	b, _ := json.Marshal(v)
	return &http.Response{
		StatusCode: status,
		Header:     http.Header{"Content-Type": {"application/json"}},
		Body:       io.NopCloser(strings.NewReader(string(b))),
	}
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

func shortID() string {
	return fmt.Sprintf("%08x", time.Now().UnixNano()&0xffffffff)
}
