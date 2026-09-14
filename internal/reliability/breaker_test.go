package reliability

import (
	"testing"
	"time"
)

func TestBreakerClosedAllowsAndResetsOnSuccess(t *testing.T) {
	b := NewCircuitBreaker(3, time.Minute)
	if !b.Allow() {
		t.Fatal("closed breaker should allow")
	}
	b.RecordFailure()
	b.RecordFailure()
	b.RecordSuccess() // resets the counter
	b.RecordFailure()
	b.RecordFailure()
	if b.State() != Closed {
		t.Fatalf("state = %v, want Closed (failures reset by the success)", b.State())
	}
}

func TestBreakerTripsAtThreshold(t *testing.T) {
	b := NewCircuitBreaker(3, time.Minute)
	if b.RecordFailure(); b.State() != Closed {
		t.Fatalf("after 1 failure: state = %v, want Closed", b.State())
	}
	if b.RecordFailure(); b.State() != Closed {
		t.Fatalf("after 2 failures: state = %v, want Closed", b.State())
	}
	tripped := b.RecordFailure()
	if !tripped {
		t.Fatal("3rd failure should report justTripped=true")
	}
	if b.State() != Open {
		t.Fatalf("after 3 failures: state = %v, want Open", b.State())
	}
	if b.Allow() {
		t.Fatal("open breaker should not allow before cooldown elapses")
	}
}

func TestBreakerHalfOpenProbeSuccessCloses(t *testing.T) {
	b := NewCircuitBreaker(1, 10*time.Millisecond)
	b.RecordFailure() // trips open
	time.Sleep(15 * time.Millisecond)

	if !b.Allow() {
		t.Fatal("cooldown elapsed: should grant exactly one probe")
	}
	if b.State() != HalfOpen {
		t.Fatalf("state after probe granted = %v, want HalfOpen", b.State())
	}
	if b.Allow() {
		t.Fatal("a second concurrent caller must not also get a probe")
	}
	b.RecordSuccess()
	if b.State() != Closed {
		t.Fatalf("state after successful probe = %v, want Closed", b.State())
	}
	if !b.Allow() {
		t.Fatal("closed breaker should allow again")
	}
}

func TestBreakerHalfOpenProbeFailureReopens(t *testing.T) {
	b := NewCircuitBreaker(1, 10*time.Millisecond)
	b.RecordFailure() // trips open
	time.Sleep(15 * time.Millisecond)
	if !b.Allow() {
		t.Fatal("expected a probe to be granted")
	}

	tripped := b.RecordFailure() // probe fails
	if !tripped {
		t.Fatal("a failed probe should report justTripped=true (re-opening)")
	}
	if b.State() != Open {
		t.Fatalf("state after failed probe = %v, want Open", b.State())
	}
	if b.Allow() {
		t.Fatal("should not allow immediately after re-opening — cooldown restarted")
	}
}

func TestBreakerStateString(t *testing.T) {
	cases := map[State]string{Closed: "closed", Open: "open", HalfOpen: "half-open"}
	for state, want := range cases {
		if got := state.String(); got != want {
			t.Errorf("State(%d).String() = %q, want %q", state, got, want)
		}
	}
}
