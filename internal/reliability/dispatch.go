package reliability

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/ngadakh/autoroute/internal/config"
	"github.com/ngadakh/autoroute/internal/observability"
	"github.com/ngadakh/autoroute/internal/openai"
	"github.com/ngadakh/autoroute/internal/provider"
)

// Dispatcher walks an ordered list of candidate models, trying each in turn
// behind that candidate's provider breaker, until one succeeds or the list is
// exhausted. It has no notion of tiers or "auto" — the caller (internal/proxy)
// decides the candidate list; a single-element list behaves exactly like a
// plain one-shot forward (no fallback), which is how a direct-named,
// non-routed request is dispatched.
type Dispatcher struct {
	Providers provider.Set
	Breakers  *Breakers
	Metrics   *observability.Metrics
}

// NewDispatcher builds a Dispatcher.
func NewDispatcher(providers provider.Set, breakers *Breakers, metrics *observability.Metrics) *Dispatcher {
	return &Dispatcher{Providers: providers, Breakers: breakers, Metrics: metrics}
}

// retryable reports whether an HTTP status from a candidate is worth falling
// back on — transient provider trouble, not a request the next model would
// also reject.
func retryable(status int) bool {
	switch status {
	case http.StatusTooManyRequests, http.StatusInternalServerError,
		http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout:
		return true
	default:
		return false
	}
}

// Dispatch tries candidates in order against rawBody (the original,
// unmodified request body — Dispatch rewrites the "model" field per hop,
// since each candidate has its own upstream id). Returns the first
// successful response and the candidate that served it, or the last
// error/response once every candidate has been tried or skipped.
func (d *Dispatcher) Dispatch(ctx context.Context, candidates []config.Model, rawBody []byte) (*http.Response, config.Model, error) {
	var lastErr error
	var lastModel config.Model

	for i, cand := range candidates {
		next := "" // label for the fallback metric; empty if this is the last candidate
		if i+1 < len(candidates) {
			next = candidates[i+1].Name
		}

		prov, ok := d.Providers.Get(cand.Provider)
		if !ok {
			lastErr, lastModel = fmt.Errorf("provider %q not configured", cand.Provider), cand
			d.fallback(cand.Name, next)
			continue
		}

		breaker := d.Breakers.Get(cand.Provider)
		if !breaker.Allow() {
			lastErr, lastModel = fmt.Errorf("provider %q circuit open", cand.Provider), cand
			d.fallback(cand.Name, next)
			continue
		}

		upstreamBody, err := openai.WithModel(rawBody, cand.Upstream)
		if err != nil {
			// A body-rewrite failure is a request-shape problem, not a
			// provider health problem — every candidate would hit it too, so
			// stop the chain rather than burn through it uselessly.
			return nil, cand, err
		}

		start := time.Now()
		resp, err := prov.Do(ctx, upstreamBody)
		elapsed := time.Since(start)
		if err != nil {
			d.Metrics.ObserveUpstream(cand.Provider, cand.Upstream, 0, elapsed, err)
			d.recordFailure(breaker, cand.Provider)
			lastErr, lastModel = err, cand
			d.fallback(cand.Name, next)
			continue
		}
		d.Metrics.ObserveUpstream(cand.Provider, cand.Upstream, resp.StatusCode, elapsed, nil)
		if retryable(resp.StatusCode) {
			d.recordFailure(breaker, cand.Provider)
			if next == "" {
				// Exhausted: this is the best we've got. Return it as-is,
				// body intact, for the caller to relay — an honest failure,
				// not a fabricated one, once there's truly nowhere else to go.
				return resp, cand, nil
			}
			drainAndClose(resp.Body)
			d.fallback(cand.Name, next)
			continue
		}

		breaker.RecordSuccess()
		d.observeBreaker(cand.Provider)
		return resp, cand, nil
	}

	if lastErr == nil {
		// candidates was empty
		return nil, config.Model{}, fmt.Errorf("no candidate models to dispatch")
	}
	return nil, lastModel, lastErr
}

func (d *Dispatcher) fallback(from, to string) {
	if to == "" {
		return // exhausted; nothing to fall back to
	}
	d.Metrics.ObserveFallback(from, to)
}

func (d *Dispatcher) observeBreaker(providerName string) {
	d.Metrics.SetBreakerState(providerName, int(d.Breakers.Get(providerName).State()))
}

func (d *Dispatcher) recordFailure(breaker *CircuitBreaker, providerName string) {
	if breaker.RecordFailure() {
		d.Metrics.IncBreakerTrip(providerName)
	}
	d.observeBreaker(providerName)
}

func drainAndClose(body io.ReadCloser) {
	_, _ = io.Copy(io.Discard, io.LimitReader(body, 64<<10))
	_ = body.Close()
}
