package router

import (
	"regexp"
	"strings"

	"github.com/ngadakh/autoroute/internal/openai"
)

// L1Signals are the cheap, pure-Go features the heuristic layer inspects —
// tokenise, regex, feature checks, no embedding, no network call. See the
// per-request budget table in docs/ARCHITECTURE.md (~0.3ms for L1).
type L1Signals struct {
	Text         string // last user turn, lowercased for matching
	Words        int
	Turns        int
	HasCodeFence bool
	HasTools     bool
	WantsJSON    bool // structural: response_format asks for JSON
}

// ExtractSignals pulls L1Signals out of a parsed chat-completions request.
func ExtractSignals(req openai.Request) L1Signals {
	text := req.LastUserMessage()
	return L1Signals{
		Text:         strings.ToLower(strings.TrimSpace(text)),
		Words:        len(strings.Fields(text)),
		Turns:        len(req.Messages),
		HasCodeFence: strings.Contains(text, "```"),
		HasTools:     req.HasTools(),
		WantsJSON:    req.WantsStructuredJSON(),
	}
}

var (
	questionWords = regexp.MustCompile(`^(who|what|when|where|how|why|which|is|are|does|do|can)\b`)

	// Verbs for a short, contextless rewrite/translation ask — example E.
	rewriteVerbs = regexp.MustCompile(`\b(rewrite|rephrase|paraphrase|translate|shorten|proofread)\b`)

	// Verbs for a bounded, imperative edit to a fenced code snippet — example C.
	codeEditVerbs = regexp.MustCompile(`\b(add|refactor|convert|fix|rename|write a test|write tests|simplify|clean up|make this idiomatic)\b`)
)

// L1Classify runs the ordered heuristic rules. Each rule only fires on a
// prompt shape it can decide with high certainty; the first match wins. If
// nothing fires, ok is false and the caller falls through to L2.
func L1Classify(sig L1Signals) (tier Tier, reason string, ok bool) {
	switch {
	case sig.Turns >= 8:
		// A long-running conversation has accumulated context a cheap model
		// is less likely to track well; be at least mid regardless of the
		// last turn's content.
		return TierMid, "long conversation (>=8 turns)", true

	case sig.HasTools:
		// Tool-calling requests need enough reasoning to pick and sequence
		// calls correctly; never route these cheap.
		return TierMid, "request declares tools", true

	case sig.WantsJSON:
		// Structural response_format constraint, not a free-text "as json"
		// mention (that ambiguous case is left to L2's structured-extraction
		// route, example F in docs/ARCHITECTURE.md).
		return TierMid, "response_format requires JSON", true

	case sig.HasCodeFence && sig.Words <= 60 && codeEditVerbs.MatchString(sig.Text):
		// example C: "Make this idiomatic and add type hints: `def f(x): ...`"
		return TierMid, "fenced code + imperative edit verb, bounded scope", true

	case !sig.HasCodeFence && sig.Words <= 15 && rewriteVerbs.MatchString(sig.Text):
		// example E: "Rewrite this sentence to sound more formal."
		return TierCheap, "short rewrite/translate request, no code", true

	case !sig.HasCodeFence && sig.Words <= 12 && questionWords.MatchString(sig.Text):
		// example A: "Who is the prime minister of India?"
		return TierCheap, "short factual-lookup shape", true
	}

	return "", "", false
}
