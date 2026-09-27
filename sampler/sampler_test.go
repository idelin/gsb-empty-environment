package sampler

import (
	"testing"
	"time"
)

var base = time.Unix(1_700_000_000, 0).UTC()

type testClock struct{ t time.Time }

func (c *testClock) now() time.Time { return c.t }

func rootSpan(trace string, start, end time.Time, err bool) Span {
	return Span{TraceID: trace, SpanID: "root-" + trace, Start: start, End: end, Err: err}
}

func childSpan(trace, id string, err bool) Span {
	return Span{TraceID: trace, SpanID: id, ParentID: "root-" + trace,
		Start: base, End: base.Add(10 * time.Millisecond), Err: err}
}

func TestErrorSpanTriggersKeep(t *testing.T) {
	clock := &testClock{t: base}
	s := NewSampler(Config{Now: clock.now})

	if _, decided := s.OnSpan(childSpan("t1", "c1", false)); decided {
		t.Fatal("child span should stay buffered")
	}
	if _, decided := s.OnSpan(childSpan("t1", "c2", true)); decided {
		t.Fatal("error child span should stay buffered")
	}
	d, decided := s.OnSpan(rootSpan("t1", base, base.Add(50*time.Millisecond), false))
	if !decided {
		t.Fatal("root span should complete the trace")
	}
	if !d.Keep || d.Reason != ReasonError {
		t.Fatalf("one error span must trigger keep/error, got %+v", d)
	}
	if d.Spans != 3 {
		t.Fatalf("expected 3 spans in decision, got %d", d.Spans)
	}
}

func TestLatencyThreshold(t *testing.T) {
	clock := &testClock{t: base}
	s := NewSampler(Config{Now: clock.now, LatencyThreshold: 500 * time.Millisecond})

	slow, _ := s.OnSpan(rootSpan("slow", base, base.Add(800*time.Millisecond), false))
	if !slow.Keep || slow.Reason != ReasonLatency {
		t.Fatalf("slow trace should be kept for latency, got %+v", slow)
	}
	fast, _ := s.OnSpan(rootSpan("fast", base, base.Add(100*time.Millisecond), false))
	if fast.Keep || fast.Reason != ReasonDrop {
		t.Fatalf("fast trace without budget should drop, got %+v", fast)
	}
}

func TestBudgetExhaustionAndRefill(t *testing.T) {
	clock := &testClock{t: base}
	s := NewSampler(Config{
		Now:             clock.now,
		BudgetPerWindow: 2,
		BudgetWindow:    time.Second,
	})
	keep := func(trace string) Decision {
		d, _ := s.OnSpan(rootSpan(trace, base, base.Add(10*time.Millisecond), false))
		return d
	}
	if d := keep("b1"); !d.Keep || d.Reason != ReasonBudget {
		t.Fatalf("first trace should consume budget, got %+v", d)
	}
	if d := keep("b2"); !d.Keep || d.Reason != ReasonBudget {
		t.Fatalf("second trace should consume budget, got %+v", d)
	}
	if d := keep("b3"); d.Keep || d.Reason != ReasonDrop {
		t.Fatalf("budget exhausted, trace must drop, got %+v", d)
	}
	clock.t = base.Add(2 * time.Second) // next window refills tokens
	if d := keep("b4"); !d.Keep || d.Reason != ReasonBudget {
		t.Fatalf("budget should refill after the window, got %+v", d)
	}
}

func TestBoundedCacheEvictsOldest(t *testing.T) {
	clock := &testClock{t: base}
	var decisions []Decision
	s := NewSampler(Config{
		Now:           clock.now,
		MaxTraces:     2,
		TimeoutPolicy: TimeoutDrop,
		OnDecision:    func(d Decision) { decisions = append(decisions, d) },
	})
	s.OnSpan(childSpan("e1", "c1", false))
	s.OnSpan(childSpan("e2", "c2", false))
	s.OnSpan(childSpan("e3", "c3", false)) // exceeds capacity, evicts e1

	if s.Pending() > 2 {
		t.Fatalf("cache must stay bounded at 2, got %d", s.Pending())
	}
	if len(decisions) != 1 || decisions[0].TraceID != "e1" ||
		decisions[0].Keep || decisions[0].Reason != ReasonEvicted {
		t.Fatalf("oldest trace should be evicted and dropped, got %+v", decisions)
	}
	// The cache never grows past the bound no matter how many arrive.
	for _, id := range []string{"e4", "e5", "e6", "e7"} {
		s.OnSpan(childSpan(id, "c-"+id, false))
		if s.Pending() > 2 {
			t.Fatalf("cache exceeded bound: %d", s.Pending())
		}
	}
}

func TestTimeoutPolicies(t *testing.T) {
	cases := []struct {
		name   string
		policy TimeoutPolicy
		hasErr bool
		keep   bool
	}{
		{"drop with error span", TimeoutDrop, true, false},
		{"keep_all without error", TimeoutKeepAll, false, true},
		{"keep_error with error", TimeoutKeepError, true, true},
		{"keep_error without error", TimeoutKeepError, false, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			clock := &testClock{t: base}
			s := NewSampler(Config{
				Now:           clock.now,
				TraceTimeout:  time.Second,
				TimeoutPolicy: tc.policy,
			})
			s.OnSpan(childSpan("tx", "c1", tc.hasErr)) // never completes
			out := s.Flush(base.Add(2 * time.Second))
			if len(out) != 1 {
				t.Fatalf("expected one timeout decision, got %+v", out)
			}
			d := out[0]
			if d.Keep != tc.keep || d.Reason != ReasonTimeout {
				t.Fatalf("policy %v: got %+v, want keep=%v", tc.policy, d, tc.keep)
			}
			if s.Pending() != 0 {
				t.Fatalf("trace should leave the cache after timeout, pending=%d", s.Pending())
			}
		})
	}
}

func TestLateSpanFollowsStoredDecision(t *testing.T) {
	clock := &testClock{t: base}
	s := NewSampler(Config{Now: clock.now})

	first, _ := s.OnSpan(rootSpan("t1", base, base.Add(10*time.Millisecond), false))
	if first.Keep {
		t.Fatalf("trace without error/latency/budget should drop, got %+v", first)
	}
	late, decided := s.OnSpan(childSpan("t1", "late", true)) // error arrives too late
	if !decided || !late.Late {
		t.Fatalf("late span should be answered from the stored decision, got %+v", late)
	}
	if late.Keep != first.Keep || late.Reason != first.Reason {
		t.Fatalf("late span must follow the original outcome: first=%+v late=%+v", first, late)
	}
	if s.Pending() != 0 {
		t.Fatalf("late span must not re-buffer the trace, pending=%d", s.Pending())
	}
}

func TestDecidedCacheIsBounded(t *testing.T) {
	clock := &testClock{t: base}
	s := NewSampler(Config{Now: clock.now, MaxDecisions: 1})

	s.OnSpan(rootSpan("d1", base, base.Add(10*time.Millisecond), false))
	s.OnSpan(rootSpan("d2", base, base.Add(10*time.Millisecond), false)) // evicts d1's decision

	if s.Decided() != 1 {
		t.Fatalf("decision cache must stay bounded at 1, got %d", s.Decided())
	}
	if _, decided := s.OnSpan(childSpan("d1", "late", false)); decided {
		t.Fatal("decision evicted from the bounded cache: span buffers as a new trace")
	}
}

func TestShardKeyConsistency(t *testing.T) {
	for _, id := range []string{"t1", "trace-xyz", "0000000000000001"} {
		a, b := ShardKey(id, 8), ShardKey(id, 8)
		if a != b || a >= 8 {
			t.Fatalf("shard key must be deterministic and in range: %d vs %d", a, b)
		}
	}
	// Spans of one trace arriving in different orders (as if split across
	// shards) must still yield the same decision, keyed by trace ID.
	decide := func(order []Span) Decision {
		clock := &testClock{t: base}
		s := NewSampler(Config{Now: clock.now})
		var d Decision
		for _, sp := range order {
			if out, ok := s.OnSpan(sp); ok {
				d = out
			}
		}
		return d
	}
	errChild := childSpan("shared", "err", true)
	okChild := childSpan("shared", "ok", false)
	root := rootSpan("shared", base, base.Add(20*time.Millisecond), false)

	d1 := decide([]Span{errChild, okChild, root})
	d2 := decide([]Span{okChild, errChild, root})
	if d1.Keep != d2.Keep || d1.Reason != d2.Reason {
		t.Fatalf("same trace must decide consistently regardless of arrival order: %+v vs %+v", d1, d2)
	}
	if !d1.Keep || d1.Reason != ReasonError {
		t.Fatalf("expected keep/error, got %+v", d1)
	}
}

func TestReplayFixture(t *testing.T) {
	f, err := LoadFixture("../fixtures/mixed.json")
	if err != nil {
		t.Fatal(err)
	}
	recs, err := Replay(f)
	if err != nil {
		t.Fatal(err)
	}
	type outcome struct {
		keep   bool
		reason Reason
	}
	want := map[string]outcome{
		"t1": {true, ReasonBudget},   // first trace consumes the budget token
		"t2": {false, ReasonDrop},    // budget exhausted
		"t3": {true, ReasonError},    // one error span is enough
		"t4": {true, ReasonLatency},  // 800ms > 500ms threshold
		"t5": {false, ReasonEvicted}, // cache bound (2) evicts oldest incomplete
		"t6": {false, ReasonTimeout}, // never completes, keep_error sees no error
		"t7": {false, ReasonTimeout},
	}
	got := map[string]Decision{}
	var late *Decision
	for _, r := range recs {
		if r.Decision.Late {
			d := r.Decision
			late = &d
			continue
		}
		if _, dup := got[r.Decision.TraceID]; dup {
			t.Fatalf("trace %s decided twice", r.Decision.TraceID)
		}
		got[r.Decision.TraceID] = r.Decision
	}
	for id, w := range want {
		d, ok := got[id]
		if !ok {
			t.Fatalf("missing decision for %s", id)
		}
		if d.Keep != w.keep || d.Reason != w.reason {
			t.Errorf("trace %s: got keep=%v reason=%s, want keep=%v reason=%s",
				id, d.Keep, d.Reason, w.keep, w.reason)
		}
	}
	if late == nil || late.TraceID != "t2" || late.Keep || late.Reason != ReasonDrop {
		t.Fatalf("late span for t2 must observe the stored drop, got %+v", late)
	}
}
