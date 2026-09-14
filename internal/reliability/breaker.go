// Package reliability is the dispatch layer: per-provider circuit breakers
// and a fallback chain across candidate models, per the "Failure &
// degradation" section of docs/ARCHITECTURE.md. The router never talks to a
// provider directly — every upstream call goes through here.
package reliability

import (
	"sync"
	"time"
)

// State is a circuit breaker's position in the state diagram in
// docs/ARCHITECTURE.md.
type State int

const (
	Closed State = iota
	Open
	HalfOpen
)

func (s State) String() string {
	switch s {
	case Open:
		return "open"
	case HalfOpen:
		return "half-open"
	default:
		return "closed"
	}
}

// CircuitBreaker tracks one provider's health. Closed -> Open after
// FailureThreshold consecutive failures; Open -> HalfOpen once Cooldown has
// elapsed, granting exactly one probe call; that probe's outcome decides
// HalfOpen -> Closed or HalfOpen -> Open (with the cooldown restarted). A
// success while Closed resets the failure count. No timers or goroutines —
// state transitions happen lazily, evaluated against the wall clock inside
// Allow/RecordFailure, which keeps this trivial to test.
type CircuitBreaker struct {
	FailureThreshold int
	Cooldown         time.Duration

	mu            sync.Mutex
	state         State
	failures      int
	openedAt      time.Time
	probeInFlight bool
}

// NewCircuitBreaker builds a Closed breaker.
func NewCircuitBreaker(failureThreshold int, cooldown time.Duration) *CircuitBreaker {
	return &CircuitBreaker{FailureThreshold: failureThreshold, Cooldown: cooldown}
}

// Allow reports whether a call may proceed right now: always true when
// Closed; true for exactly one caller (the probe) once Cooldown has elapsed
// past the Open transition, false otherwise (still cooling down, or a probe
// is already in flight).
func (b *CircuitBreaker) Allow() bool {
	b.mu.Lock()
	defer b.mu.Unlock()

	switch b.state {
	case Closed:
		return true
	case HalfOpen:
		return false // a probe is already in flight
	default: // Open
		if time.Since(b.openedAt) < b.Cooldown {
			return false
		}
		b.state = HalfOpen
		b.probeInFlight = true
		return true
	}
}

// RecordSuccess reports a successful call. In Closed it resets the failure
// counter; as the HalfOpen probe it closes the breaker.
func (b *CircuitBreaker) RecordSuccess() {
	b.mu.Lock()
	defer b.mu.Unlock()

	b.failures = 0
	b.state = Closed
	b.probeInFlight = false
}

// RecordFailure reports a failed call. In Closed it counts toward
// FailureThreshold; as the HalfOpen probe (or a failure reported while
// already Open) it (re)opens the breaker and restarts the cooldown. Returns
// true the moment the breaker transitions to Open — the caller's cue to
// count a breaker trip, distinct from every subsequent failure while it
// stays Open.
func (b *CircuitBreaker) RecordFailure() (justTripped bool) {
	b.mu.Lock()
	defer b.mu.Unlock()

	switch b.state {
	case HalfOpen:
		b.trip()
		return true
	case Open:
		b.trip() // restart the cooldown on a failure reported mid-window
		return false
	default: // Closed
		b.failures++
		if b.failures >= b.FailureThreshold {
			b.trip()
			return true
		}
		return false
	}
}

// trip opens the breaker. Caller must hold mu.
func (b *CircuitBreaker) trip() {
	b.state = Open
	b.openedAt = time.Now()
	b.failures = 0
	b.probeInFlight = false
}

// State reports the current state for metrics.
func (b *CircuitBreaker) State() State {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.state
}
