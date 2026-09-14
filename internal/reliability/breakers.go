package reliability

import (
	"sync"
	"time"
)

// Breakers is a registry of CircuitBreakers keyed by provider name — one
// breaker per provider, not per tier, per docs/ARCHITECTURE.md: several
// tiers can share (and fail over between) the same provider account.
type Breakers struct {
	failureThreshold int
	cooldown         time.Duration

	mu         sync.Mutex
	byProvider map[string]*CircuitBreaker
}

// NewBreakers builds a registry; individual breakers are created lazily with
// the given tuning on first use.
func NewBreakers(failureThreshold int, cooldown time.Duration) *Breakers {
	return &Breakers{
		failureThreshold: failureThreshold,
		cooldown:         cooldown,
		byProvider:       make(map[string]*CircuitBreaker),
	}
}

// Get returns the breaker for a provider, creating it on first use.
func (b *Breakers) Get(provider string) *CircuitBreaker {
	b.mu.Lock()
	defer b.mu.Unlock()

	if cb, ok := b.byProvider[provider]; ok {
		return cb
	}
	cb := NewCircuitBreaker(b.failureThreshold, b.cooldown)
	b.byProvider[provider] = cb
	return cb
}
