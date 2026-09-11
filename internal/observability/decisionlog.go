package observability

import (
	"encoding/json"
	"io"
	"sync"
	"time"
)

// DecisionEntry is one line of the append-only decision log — the "decision
// store" in docs/ARCHITECTURE.md. Its shape is deliberately flat JSON so the
// future eval harness (M4) can replay it directly.
type DecisionEntry struct {
	Time       time.Time `json:"time"`
	Tier       string    `json:"tier"`
	Layer      string    `json:"layer"`
	Confidence float64   `json:"confidence"`
	Route      string    `json:"route,omitempty"`
	Reason     string    `json:"reason"`
	Degraded   bool      `json:"degraded"`
	LatencyMS  float64   `json:"latency_ms"`
}

// DecisionLog appends one JSON line per routed request to w. Safe for
// concurrent use.
type DecisionLog struct {
	w  io.Writer
	mu sync.Mutex
}

// NewDecisionLog wraps w. w is typically an append-mode *os.File; the caller
// owns its lifecycle (open/close).
func NewDecisionLog(w io.Writer) *DecisionLog {
	return &DecisionLog{w: w}
}

// Append writes one entry as a JSON line. A write error is logged by the
// caller, not returned as a request failure — the decision log is
// observability, never on the critical path for serving a response.
func (d *DecisionLog) Append(entry DecisionEntry) error {
	b, err := json.Marshal(entry)
	if err != nil {
		return err
	}
	b = append(b, '\n')

	d.mu.Lock()
	defer d.mu.Unlock()
	_, err = d.w.Write(b)
	return err
}
