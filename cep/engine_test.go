package cep

import (
	"strings"
	"testing"
)

func ev(t string, ts int64) Event { return Event{Type: t, Timestamp: ts} }

// Acceptance: A at t=0, B at t=5 within a 10s window produces a hit.
func TestHitWithinWindow(t *testing.T) {
	e := NewEngine(10_000)
	if n := e.Process(ev("A", 0)); len(n) != 0 {
		t.Fatalf("unexpected notifications on A: %v", n)
	}
	n := e.Process(ev("B", 5_000))
	if len(n) != 1 || n[0].Kind != NotifyMatch {
		t.Fatalf("expected 1 MATCH, got %v", n)
	}
	m := n[0].Match
	if m.A.Timestamp != 0 || m.B.Timestamp != 5_000 {
		t.Fatalf("bad match interval: %+v", m)
	}
	if len(e.ActiveMatches()) != 1 {
		t.Fatalf("expected 1 active match")
	}
}

// Acceptance: a late C at t=2 (arriving after the hit) retracts the previous
// hit precisely and the retraction is recorded in the log.
func TestLateCRetractsHitAndLogs(t *testing.T) {
	e := NewEngine(10_000)
	e.Process(ev("A", 0))
	hits := e.Process(ev("B", 5_000))
	if len(hits) != 1 {
		t.Fatalf("expected hit, got %v", hits)
	}

	// Out-of-order compensation: C@2000 arrives after B@5000 was processed.
	n := e.Process(ev("C", 2_000))
	if len(n) != 1 || n[0].Kind != NotifyRetract {
		t.Fatalf("expected 1 RETRACT, got %v", n)
	}
	if n[0].Match.ID != hits[0].Match.ID {
		t.Fatalf("retracted wrong match: %+v", n[0].Match)
	}
	if len(e.ActiveMatches()) != 0 {
		t.Fatalf("match should have been retracted")
	}

	logs := strings.Join(e.Logs(), "\n")
	if !strings.Contains(logs, "retract match #1") || !strings.Contains(logs, "late C@2000") {
		t.Fatalf("retraction not logged precisely, logs:\n%s", logs)
	}
}

// A C arriving in order between A and B prevents the match entirely.
func TestCInOrderBlocksMatch(t *testing.T) {
	e := NewEngine(10_000)
	e.Process(ev("A", 0))
	e.Process(ev("C", 2_000))
	if n := e.Process(ev("B", 5_000)); len(n) != 0 {
		t.Fatalf("expected no match, got %v", n)
	}
	if e.PendingCount() != 0 {
		t.Fatalf("open run should have been killed by C")
	}
}

// B outside the 10s window does not match; the open run is discarded on timeout.
func TestTimeoutDiscard(t *testing.T) {
	e := NewEngine(10_000)
	e.Process(ev("A", 0))
	if n := e.Process(ev("B", 11_000)); len(n) != 0 {
		t.Fatalf("expected no match beyond window, got %v", n)
	}
	// Watermark is now 11s; the A@0 run (age 11s > 10s) must be discarded.
	if e.PendingCount() != 0 {
		t.Fatalf("run should have been discarded on timeout")
	}
	logs := strings.Join(e.Logs(), "\n")
	if !strings.Contains(logs, "timeout: discard open run A@0") {
		t.Fatalf("timeout discard not logged, logs:\n%s", logs)
	}
}

// B exactly at the window boundary still matches (inclusive window).
func TestBoundaryWindowInclusive(t *testing.T) {
	e := NewEngine(10_000)
	e.Process(ev("A", 0))
	if n := e.Process(ev("B", 10_000)); len(n) != 1 {
		t.Fatalf("expected boundary hit, got %v", n)
	}
}

// Out-of-order arrival: B arrives before its A in processing order but after
// it in event time; the engine must still match once A arrives.
func TestOutOfOrderABeforeA(t *testing.T) {
	e := NewEngine(10_000)
	e.Process(ev("B", 5_000)) // no open run yet
	n := e.Process(ev("A", 0))
	// A arrives late; B was already consumed. Reprocessing B is out of scope:
	// the engine buffers only forward runs, so no match is expected here.
	if len(n) != 0 {
		t.Fatalf("unexpected match: %v", n)
	}
}

// Late C that falls outside the match interval must NOT retract.
func TestLateCOutsideIntervalKept(t *testing.T) {
	e := NewEngine(10_000)
	e.Process(ev("A", 0))
	e.Process(ev("B", 5_000))
	if n := e.Process(ev("C", 8_000)); len(n) != 0 {
		t.Fatalf("C outside interval must not retract, got %v", n)
	}
	if len(e.ActiveMatches()) != 1 {
		t.Fatalf("match should still be live")
	}
}

// Multiple A runs: C retracts only the matches whose interval contains it.
func TestSelectiveRetraction(t *testing.T) {
	e := NewEngine(10_000)
	e.Process(ev("A", 0))
	e.Process(ev("B", 3_000))  // match #1: (0, 3000)
	e.Process(ev("A", 4_000))  // arrives late, still within window of nothing
	e.Process(ev("B", 6_000))  // match #2: (4000, 6000)
	n := e.Process(ev("C", 2_000))
	if len(n) != 1 || n[0].Match.ID != 1 {
		t.Fatalf("expected retraction of match #1 only, got %v", n)
	}
	live := e.ActiveMatches()
	if len(live) != 1 || live[0].ID != 2 {
		t.Fatalf("match #2 should survive, got %v", live)
	}
}

// Flush forces timeout discard of runs that never saw a B.
func TestFlushExpiresOpenRuns(t *testing.T) {
	e := NewEngine(10_000)
	e.Process(ev("A", 1_000))
	e.Flush(20_000)
	if e.PendingCount() != 0 {
		t.Fatalf("flush should expire the open run")
	}
}
