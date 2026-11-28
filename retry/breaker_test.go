package retry

import (
	"testing"
	"time"
)

func TestCircuitBreakerLifecycle(t *testing.T) {
	now := time.Unix(0, 0)
	cb := NewCircuitBreaker(BreakerConfig{
		FailureThreshold: 3, OpenDuration: time.Second, HalfOpenMaxAttempts: 1,
	})
	if cb.Allow(now) != true {
		t.Fatal("fresh breaker must allow")
	}
	cb.RecordFailure(now)
	cb.RecordFailure(now)
	if cb.State(now) != Closed {
		t.Fatal("breaker should stay closed below threshold")
	}
	cb.RecordFailure(now)
	if cb.State(now) != Open {
		t.Fatal("breaker must open at threshold")
	}
	if cb.Allow(now) {
		t.Fatal("open breaker must reject")
	}
	// After open duration it transitions to HalfOpen and admits one probe.
	later := now.Add(time.Second + time.Millisecond)
	if !cb.Allow(later) {
		t.Fatal("breaker must admit a probe after open duration")
	}
	if cb.State(later) != HalfOpen {
		t.Fatal("expected HalfOpen")
	}
	// A successful probe closes the breaker.
	cb.RecordSuccess()
	if cb.State(later) != Closed {
		t.Fatal("successful probe must close breaker")
	}
}

func TestCircuitBreakerHalfOpenReopens(t *testing.T) {
	now := time.Unix(0, 0)
	cb := NewCircuitBreaker(BreakerConfig{
		FailureThreshold: 2, OpenDuration: time.Second, HalfOpenMaxAttempts: 1,
	})
	cb.RecordFailure(now)
	cb.RecordFailure(now)
	if cb.State(now) != Open {
		t.Fatal("expected open")
	}
	probe := now.Add(time.Second + time.Millisecond)
	cb.Allow(probe)
	cb.RecordFailure(probe)
	if cb.State(probe) != Open {
		t.Fatal("failed half-open probe must re-open")
	}
}

func TestCircuitBreakerDisabled(t *testing.T) {
	cb := NewCircuitBreaker(BreakerConfig{FailureThreshold: 0})
	for i := 0; i < 100; i++ {
		cb.RecordFailure(time.Unix(0, 0))
	}
	if !cb.Allow(time.Unix(0, 0)) {
		t.Fatal("disabled breaker must always allow")
	}
}
