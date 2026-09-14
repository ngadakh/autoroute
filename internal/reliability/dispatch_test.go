package reliability

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/ngadakh/autoroute/internal/config"
	"github.com/ngadakh/autoroute/internal/observability"
	"github.com/ngadakh/autoroute/internal/provider"
)

// scriptedProvider returns one canned outcome per call, in order; the last
// outcome repeats if Do is called more times than scripted (keeps tests
// simple when a candidate is hit more than once across sub-tests sharing a
// registry).
type scriptedProvider struct {
	name  string
	calls int
	steps []func() (*http.Response, error)
}

func (p *scriptedProvider) Name() string { return p.name }

func (p *scriptedProvider) Do(_ context.Context, _ []byte) (*http.Response, error) {
	i := p.calls
	if i >= len(p.steps) {
		i = len(p.steps) - 1
	}
	p.calls++
	return p.steps[i]()
}

func ok200() (*http.Response, error) {
	return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{}`))}, nil
}

func status(code int) func() (*http.Response, error) {
	return func() (*http.Response, error) {
		return &http.Response{StatusCode: code, Body: io.NopCloser(strings.NewReader(`{}`))}, nil
	}
}

func transportErr() (*http.Response, error) {
	return nil, errors.New("connection refused")
}

func newDispatcher(providers provider.Set) *Dispatcher {
	return NewDispatcher(providers, NewBreakers(5, 30*time.Second), observability.New("test"))
}

func model(name, prov, upstream string) config.Model {
	return config.Model{Name: name, Provider: prov, Upstream: upstream}
}

func TestDispatchFirstCandidateSucceedsNoFallback(t *testing.T) {
	cheap := &scriptedProvider{name: "cheap-prov", steps: []func() (*http.Response, error){ok200}}
	d := newDispatcher(provider.Set{"cheap-prov": cheap})

	resp, used, err := d.Dispatch(context.Background(), []config.Model{model("fast", "cheap-prov", "u1")}, []byte(`{"model":"auto"}`))
	if err != nil {
		t.Fatal(err)
	}
	if used.Name != "fast" || resp.StatusCode != 200 {
		t.Fatalf("used=%+v status=%d", used, resp.StatusCode)
	}
	if cheap.calls != 1 {
		t.Fatalf("calls = %d, want 1 (no fallback)", cheap.calls)
	}
}

func TestDispatchFallsBackOnFailure(t *testing.T) {
	dead := &scriptedProvider{name: "dead", steps: []func() (*http.Response, error){transportErr}}
	alive := &scriptedProvider{name: "alive", steps: []func() (*http.Response, error){ok200}}
	d := newDispatcher(provider.Set{"dead": dead, "alive": alive})

	candidates := []config.Model{model("fast", "dead", "u1"), model("smart", "alive", "u2")}
	resp, used, err := d.Dispatch(context.Background(), candidates, []byte(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	if used.Name != "smart" {
		t.Fatalf("used = %q, want smart (fell back)", used.Name)
	}
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d", resp.StatusCode)
	}

	got := testutil.ToFloat64(d.Metrics.RouteFallbacks.WithLabelValues("fast", "smart"))
	if got != 1 {
		t.Fatalf(`autoroute_fallback_total{from="fast",to="smart"} = %v, want 1`, got)
	}
}

func TestDispatchRetryableStatusFallsBack(t *testing.T) {
	overloaded := &scriptedProvider{name: "overloaded", steps: []func() (*http.Response, error){status(503)}}
	alive := &scriptedProvider{name: "alive", steps: []func() (*http.Response, error){ok200}}
	d := newDispatcher(provider.Set{"overloaded": overloaded, "alive": alive})

	candidates := []config.Model{model("fast", "overloaded", "u1"), model("smart", "alive", "u2")}
	resp, used, err := d.Dispatch(context.Background(), candidates, []byte(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	if used.Name != "smart" || resp.StatusCode != 200 {
		t.Fatalf("used=%+v status=%d", used, resp.StatusCode)
	}
}

func TestDispatchNonRetryableStatusStopsChain(t *testing.T) {
	badRequest := &scriptedProvider{name: "p", steps: []func() (*http.Response, error){status(400)}}
	neverCalled := &scriptedProvider{name: "never", steps: []func() (*http.Response, error){ok200}}
	d := newDispatcher(provider.Set{"p": badRequest, "never": neverCalled})

	candidates := []config.Model{model("fast", "p", "u1"), model("smart", "never", "u2")}
	resp, used, err := d.Dispatch(context.Background(), candidates, []byte(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	if used.Name != "fast" || resp.StatusCode != 400 {
		t.Fatalf("used=%+v status=%d, want the 400 relayed as-is, no fallback", used, resp.StatusCode)
	}
	if neverCalled.calls != 0 {
		t.Fatalf("second candidate was called %d times, want 0 (400 shouldn't trigger fallback)", neverCalled.calls)
	}
}

func TestDispatchAllCandidatesFailReturnsLastError(t *testing.T) {
	dead1 := &scriptedProvider{name: "dead1", steps: []func() (*http.Response, error){transportErr}}
	dead2 := &scriptedProvider{name: "dead2", steps: []func() (*http.Response, error){transportErr}}
	d := newDispatcher(provider.Set{"dead1": dead1, "dead2": dead2})

	candidates := []config.Model{model("fast", "dead1", "u1"), model("smart", "dead2", "u2")}
	resp, used, err := d.Dispatch(context.Background(), candidates, []byte(`{}`))
	if err == nil {
		t.Fatal("expected an error when every candidate fails")
	}
	if resp != nil {
		t.Fatalf("resp = %+v, want nil (no fabricated success)", resp)
	}
	if used.Name != "smart" {
		t.Fatalf("used = %q, want the last-tried candidate for logging", used.Name)
	}
}

func TestDispatchLastCandidateRetryableStatusReturnsItAsIs(t *testing.T) {
	// Doc: "never a hard 5xx" only holds while there's somewhere left to go;
	// once truly exhausted, Dispatch returns the last response honestly
	// rather than fabricating a different outcome.
	overloaded := &scriptedProvider{name: "p", steps: []func() (*http.Response, error){status(503)}}
	d := newDispatcher(provider.Set{"p": overloaded})

	resp, used, err := d.Dispatch(context.Background(), []config.Model{model("fast", "p", "u1")}, []byte(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	if used.Name != "fast" || resp.StatusCode != 503 {
		t.Fatalf("used=%+v status=%d, want the exhausted 503 relayed", used, resp.StatusCode)
	}
}

func TestDispatchOpenBreakerSkipsWithoutCalling(t *testing.T) {
	dead := &scriptedProvider{name: "dead", steps: []func() (*http.Response, error){transportErr}}
	alive := &scriptedProvider{name: "alive", steps: []func() (*http.Response, error){ok200}}
	breakers := NewBreakers(1, time.Minute) // trips after 1 failure, long cooldown
	d := NewDispatcher(provider.Set{"dead": dead, "alive": alive}, breakers, observability.New("test"))

	candidates := []config.Model{model("fast", "dead", "u1"), model("smart", "alive", "u2")}

	// First call trips the "dead" breaker.
	if _, _, err := d.Dispatch(context.Background(), candidates, []byte(`{}`)); err != nil {
		t.Fatal(err)
	}
	if dead.calls != 1 {
		t.Fatalf("dead.calls = %d, want 1", dead.calls)
	}

	// Second call: breaker is open, "dead" must not be called again.
	resp, used, err := d.Dispatch(context.Background(), candidates, []byte(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	if used.Name != "smart" || resp.StatusCode != 200 {
		t.Fatalf("used=%+v status=%d", used, resp.StatusCode)
	}
	if dead.calls != 1 {
		t.Fatalf("dead.calls = %d, want still 1 (breaker should skip fast, not call)", dead.calls)
	}
}

func TestDispatchSingleElementChainBehavesLikeDirectForward(t *testing.T) {
	dead := &scriptedProvider{name: "dead", steps: []func() (*http.Response, error){transportErr}}
	d := newDispatcher(provider.Set{"dead": dead})

	_, _, err := d.Dispatch(context.Background(), []config.Model{model("relay", "dead", "u1")}, []byte(`{}`))
	if err == nil {
		t.Fatal("expected an error — no fallback candidates, same as M1 passthrough")
	}
	if dead.calls != 1 {
		t.Fatalf("calls = %d, want exactly 1 (no retry/fallback for a single-element chain)", dead.calls)
	}
}
