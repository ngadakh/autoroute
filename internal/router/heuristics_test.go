package router

import (
	"encoding/json"
	"testing"

	"github.com/ngadakh/autoroute/internal/openai"
)

// Table-driven against the worked examples in docs/ARCHITECTURE.md. A/C/E are
// documented as decided at L1; B/D/F are documented as decided at L2 (or L3,
// not yet built) and must NOT fire at L1 — they fall through to the embedding
// classifier instead.
func TestL1ClassifyWorkedExamples(t *testing.T) {
	cases := []struct {
		name      string
		req       openai.Request
		wantTier  Tier
		wantFires bool
	}{
		{
			name:      "A factual-lookup",
			req:       msg("Who is the prime minister of India?"),
			wantTier:  TierCheap,
			wantFires: true,
		},
		{
			name:      "B formal-proof falls through to L2",
			req:       msg("Prove that the sum of the first n odd numbers is n squared."),
			wantFires: false,
		},
		{
			name:      "C scoped code edit",
			req:       msg("Make this idiomatic and add type hints: ```def f(x): return [i for i in x if i%2==0]```"),
			wantTier:  TierMid,
			wantFires: true,
		},
		{
			name:      "D open-judgement falls through to L2",
			req:       msg("Here is my B2B pricing plan, switching seats to usage. Is this a good idea?"),
			wantFires: false,
		},
		{
			name:      "E short rewrite",
			req:       msg("Rewrite this sentence to sound more formal."),
			wantTier:  TierCheap,
			wantFires: true,
		},
		{
			name:      "F structured extraction (free text) falls through to L2",
			req:       msg("Extract every date mentioned in this text and return them as JSON."),
			wantFires: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			sig := ExtractSignals(tc.req)
			tier, reason, ok := L1Classify(sig)
			if ok != tc.wantFires {
				t.Fatalf("L1Classify fired=%v (tier=%q reason=%q), want fired=%v", ok, tier, reason, tc.wantFires)
			}
			if ok && tier != tc.wantTier {
				t.Fatalf("tier = %q, want %q (reason: %s)", tier, tc.wantTier, reason)
			}
		})
	}
}

func TestL1ClassifyStructuralSignals(t *testing.T) {
	t.Run("tools present forces at least mid", func(t *testing.T) {
		req := msg("hi")
		req.Tools = []json.RawMessage{json.RawMessage(`{"type":"function"}`)}
		tier, _, ok := L1Classify(ExtractSignals(req))
		if !ok || tier != TierMid {
			t.Fatalf("tier=%q ok=%v, want mid/true", tier, ok)
		}
	})

	t.Run("structured json response_format forces at least mid", func(t *testing.T) {
		req := msg("give me the data")
		req.ResponseFormat = json.RawMessage(`{"type":"json_object"}`)
		tier, _, ok := L1Classify(ExtractSignals(req))
		if !ok || tier != TierMid {
			t.Fatalf("tier=%q ok=%v, want mid/true", tier, ok)
		}
	})

	t.Run("long conversation forces at least mid", func(t *testing.T) {
		req := msg("ok")
		for i := 0; i < 8; i++ {
			req.Messages = append(req.Messages, openai.Message{Role: "user", Content: "x"})
		}
		tier, _, ok := L1Classify(ExtractSignals(req))
		if !ok || tier != TierMid {
			t.Fatalf("tier=%q ok=%v, want mid/true", tier, ok)
		}
	})
}

func msg(text string) openai.Request {
	return openai.Request{
		Model:    "auto",
		Messages: []openai.Message{{Role: "user", Content: text}},
	}
}
