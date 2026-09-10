package openai

import (
	"encoding/json"
	"testing"
)

func TestPeek(t *testing.T) {
	r, err := Peek([]byte(`{"model":"fast","messages":[{"role":"user","content":"hi"}],"stream":true}`))
	if err != nil {
		t.Fatal(err)
	}
	if r.Model != "fast" || !r.Stream || r.LastUserMessage() != "hi" {
		t.Fatalf("peek = %+v", r)
	}

	for _, bad := range []string{
		`{}`,
		`{"model":"x"}`,
		`{"messages":[{"role":"user","content":"hi"}]}`,
		`not json`,
	} {
		if _, err := Peek([]byte(bad)); err == nil {
			t.Fatalf("expected error for %q", bad)
		}
	}
}

func TestWithModelIsLossless(t *testing.T) {
	in := []byte(`{"model":"fast","messages":[{"role":"user","content":"hi"}],"temperature":0.7,"tools":[{"x":1}]}`)
	out, err := WithModel(in, "gpt-4o-mini")
	if err != nil {
		t.Fatal(err)
	}

	var got map[string]any
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatal(err)
	}
	if got["model"] != "gpt-4o-mini" {
		t.Fatalf("model = %v", got["model"])
	}
	if got["temperature"] != 0.7 {
		t.Fatalf("temperature lost: %v", got["temperature"])
	}
	if _, ok := got["tools"]; !ok {
		t.Fatal("tools field dropped")
	}
}

func TestLastUserMessage(t *testing.T) {
	r := Request{Messages: []Message{
		{Role: "system", Content: "be nice"},
		{Role: "user", Content: "first"},
		{Role: "assistant", Content: "reply"},
		{Role: "user", Content: "second"},
	}}
	if got := r.LastUserMessage(); got != "second" {
		t.Fatalf("got %q", got)
	}
	if got := (Request{}).LastUserMessage(); got != "" {
		t.Fatalf("empty request should yield empty string, got %q", got)
	}
}
