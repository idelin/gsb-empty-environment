package tailsampler

import (
	"fmt"
	"testing"
	"time"
)

var testStart = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

func testConfig(c Clock) Config {
	return Config{
		MaxBufferedTraces: 100,
		MaxBufferedSpans:  1000,
		TraceTimeout:      5 * time.Second,
		DecisionTTL:       time.Minute,
		BudgetPerSecond:   100,
		LatencyThreshold:  2 * time.Second,
		Clock:             c,
	}
}

func rootSpan(id string, start time.Time, dur time.Duration, err bool) Span {
	return Span{TraceID: id, SpanID: id + "-root", Name: "root",
		Start: start, End: start.Add(dur), Error: err}
}

func childSpan(id string, start time.Time, dur time.Duration, err bool) Span {
	return Span{TraceID: id, SpanID: id + "-child", ParentID: id + "-root", Name: "child",
		Start: start, End: start.Add(dur), Error: err}
}

func TestErrorSpanTriggersKeep(t *testing.T) {
	s := NewSampler(testConfig(NewManualClock(testStart)))
	if _, ok := s.OnSpan(childSpan("t1", testStart, 50*time.Millisecond, false)); ok {
		t.Fatal("child span should be buffered, not decided")
	}
	// A single error span among clean ones keeps the whole trace.
	if _, ok := s.OnSpan(childSpan("t1", testStart, 60*time.Millisecond, true)); ok {
		t.Fatal("second child span should be buffered, not decided")
	}
	d, ok := s.OnSpan(rootSpan("t1", testStart, 100*time.Millisecond, false))
	if !ok || !d.Keep || d.Reason != "error" {
		t.Fatalf("got %+v ok=%v, want keep/error", d, ok)
	}
}

func TestSlowTraceKept(t *testing.T) {
	s := NewSampler(testConfig(NewManualClock(testStart)))
	s.OnSpan(childSpan("t1", testStart, time.Second, false))
	d, ok := s.OnSpan(rootSpan("t1", testStart, 3*time.Second, false))
	if !ok || !d.Keep || d.Reason != "slow_trace" {
		t.Fatalf("got %+v ok=%v, want keep/slow_trace", d, ok)
	}
}

func TestFastCleanTraceDropped(t *testing.T) {
	s := NewSampler(testConfig(NewManualClock(testStart)))
	d, ok := s.OnSpan(rootSpan("t1", testStart, 100*time.Millisecond, false))
	if !ok || d.Keep || d.Reason != "no_signal" {
		t.Fatalf("got %+v ok=%v, want drop/no_signal", d, ok)
	}
}

func TestBudgetExhaustedDropsWithoutCaching(t *testing.T) {
	clock := NewManualClock(testStart)
	cfg := testConfig(clock)
	cfg.BudgetPerSecond = 2 // capacity 2 tokens
	s := NewSampler(cfg)

	var kept, budgetDropped int
	for i := 0; i < 6; i++ {
		d, ok := s.OnSpan(rootSpan(fmt.Sprintf("t%d", i), testStart, 100*time.Millisecond, true))
		if !ok {
			t.Fatalf("trace t%d not decided", i)
		}
		switch {
		case d.Keep:
			kept++
		case d.Reason == "budget_exhausted":
			budgetDropped++
		}
	}
	if kept != 2 || budgetDropped != 4 {
		t.Fatalf("kept=%d budgetDropped=%d, want 2 and 4", kept, budgetDropped)
	}
	// Budget exhaustion must not inflate the cache: everything is decided.
	if n := s.BufferedSpans(); n != 0 {
		t.Fatalf("buffered spans = %d, want 0", n)
	}
	// Budget refills over time.
	clock.Advance(time.Second)
	if d, _ := s.OnSpan(rootSpan("t-later", clock.Now(), 100*time.Millisecond, true)); !d.Keep {
		t.Fatalf("after refill got %+v, want keep", d)
	}
}

func TestBoundedCacheEvictsOldest(t *testing.T) {
	clock := NewManualClock(testStart)
	cfg := testConfig(clock)
	cfg.MaxBufferedSpans = 4
	s := NewSampler(cfg)

	// Three incomplete traces, two spans each: the third one overflows the
	// span budget and forces the oldest trace to be decided early.
	s.OnSpan(childSpan("t1", testStart, 10*time.Millisecond, false))
	s.OnSpan(childSpan("t1", testStart, 20*time.Millisecond, false))
	s.OnSpan(childSpan("t2", testStart, 10*time.Millisecond, false))
	s.OnSpan(childSpan("t2", testStart, 20*time.Millisecond, false))
	s.OnSpan(childSpan("t3", testStart, 10*time.Millisecond, false))

	if got := s.Stats.ByReason["cache_pressure_timeout"]; got != 1 {
		t.Fatalf("cache_pressure_timeout = %d, want 1", got)
	}
	if n := s.BufferedSpans(); n > cfg.MaxBufferedSpans {
		t.Fatalf("buffered spans = %d exceeds bound %d", n, cfg.MaxBufferedSpans)
	}
	// The evicted trace's decision is cached; a late span follows it.
	d, ok := s.OnSpan(childSpan("t1", testStart, 30*time.Millisecond, false))
	if !ok || d.Keep || d.Reason != "cache_pressure_timeout" {
		t.Fatalf("late span after eviction got %+v ok=%v", d, ok)
	}
}

func TestTimeoutPolicyForIncompleteTraces(t *testing.T) {
	clock := NewManualClock(testStart)
	s := NewSampler(testConfig(clock))

	s.OnSpan(childSpan("t-clean", testStart, 100*time.Millisecond, false))
	s.OnSpan(childSpan("t-error", testStart, 100*time.Millisecond, true))
	s.OnSpan(childSpan("t-slow", testStart, 3*time.Second, false))

	clock.Advance(6 * time.Second) // past TraceTimeout
	got := map[string]Decision{}
	for _, dt := range s.Tick() {
		got[dt.TraceID] = dt.Decision
	}
	if d := got["t-clean"]; d.Keep || d.Reason != "timeout" {
		t.Fatalf("t-clean: got %+v, want drop/timeout", d)
	}
	if d := got["t-error"]; !d.Keep || d.Reason != "timeout_error" {
		t.Fatalf("t-error: got %+v, want keep/timeout_error", d)
	}
	if d := got["t-slow"]; !d.Keep || d.Reason != "timeout_slow" {
		t.Fatalf("t-slow: got %+v, want keep/timeout_slow", d)
	}
	if n := s.BufferedTraces(); n != 0 {
		t.Fatalf("buffered traces = %d, want 0 after sweep", n)
	}
}

func TestLateSpanFollowsDecision(t *testing.T) {
	clock := NewManualClock(testStart)
	s := NewSampler(testConfig(clock))

	first, ok := s.OnSpan(rootSpan("t1", testStart, 100*time.Millisecond, true))
	if !ok || !first.Keep {
		t.Fatalf("initial decision %+v ok=%v, want keep", first, ok)
	}
	clock.Advance(10 * time.Second)
	late, ok := s.OnSpan(childSpan("t1", testStart, 50*time.Millisecond, false))
	if !ok || late != first {
		t.Fatalf("late span got %+v ok=%v, want same as %+v", late, ok, first)
	}
	if n := s.BufferedSpans(); n != 0 {
		t.Fatalf("late span was buffered: buffered=%d, want 0", n)
	}

	// After the decision TTL expires, a span for the trace buffers again.
	clock.Advance(2 * time.Minute)
	s.Tick() // expire cached decisions
	if _, ok := s.OnSpan(childSpan("t1", testStart, 50*time.Millisecond, false)); ok {
		t.Fatal("after TTL expiry the span should be buffered, not decided")
	}
}
