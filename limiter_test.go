package eventlimiter

import (
	"testing"
	"time"
)

var epoch = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

func at(sec int) time.Time { return epoch.Add(time.Duration(sec) * time.Second) }

// Acceptance boundary: rule = at most 3 requests per 10 seconds.
// t=1,2,8 pass; late t=0 arrives -> t=0,1,2 take the 3 slots, so the
// previously accepted t=8 must be revoked with a correction notice.
func TestLateArrivalRevokesLaterRequest(t *testing.T) {
	l := New(3, 10*time.Second)

	for _, sec := range []int{1, 2, 8} {
		v, corr := l.Submit(idAt(sec), at(sec))
		if v != Accepted {
			t.Fatalf("t=%d: got %s, want ACCEPTED", sec, v)
		}
		if len(corr) != 0 {
			t.Fatalf("t=%d: unexpected corrections: %v", sec, corr)
		}
	}

	// Late request with event time t=0 arrives after t=1,2,8.
	v, corr := l.Submit("req-0", at(0))
	if v != Accepted {
		t.Fatalf("late t=0: got %s, want ACCEPTED", v)
	}
	if len(corr) != 1 {
		t.Fatalf("late t=0: got %d corrections, want 1: %v", len(corr), corr)
	}
	c := corr[0]
	if c.ID != "req-8" || c.From != Accepted || c.To != Rejected {
		t.Fatalf("correction = %+v, want req-8 ACCEPTED->REJECTED", c)
	}

	// Final state: t=0,1,2 hold the quota; t=8 is rejected.
	want := map[string]Verdict{
		"req-0": Accepted, "req-1": Accepted,
		"req-2": Accepted, "req-8": Rejected,
	}
	for id, w := range want {
		got, ok := l.VerdictOf(id)
		if !ok || got != w {
			t.Fatalf("VerdictOf(%q) = %s,%v; want %s,true", id, got, ok, w)
		}
	}
}

func idAt(sec int) string {
	return "req-" + string(rune('0'+sec))
}

func TestExactSlidingWindowBoundary(t *testing.T) {
	l := New(3, 10*time.Second)

	// Exactly 3 inside the window: all pass.
	for _, sec := range []int{0, 5, 9} {
		if v, _ := l.Submit(idAt(sec), at(sec)); v != Accepted {
			t.Fatalf("t=%d: got %s, want ACCEPTED", sec, v)
		}
	}
	// 4th request inside the same 10s span: rejected.
	if v, _ := l.Submit("req-9b", at(9)); v != Rejected {
		t.Fatalf("4th in-window request: got %s, want REJECTED", v)
	}
	// t=10 is exactly 10s after t=0, so t=0 falls out of the (t-10, t) window.
	if v, _ := l.Submit("req-10", at(10)); v != Accepted {
		t.Fatalf("t=10: got %s, want ACCEPTED (t=0 expired)", v)
	}
}

func TestHistoryKeptOrderedByEventTime(t *testing.T) {
	l := New(10, time.Minute)
	l.Submit("late", at(3))
	l.Submit("early", at(1))
	l.Submit("mid", at(2))

	h := l.History()
	wantOrder := []string{"early", "mid", "late"}
	if len(h) != len(wantOrder) {
		t.Fatalf("history len = %d, want %d", len(h), len(wantOrder))
	}
	for i, id := range wantOrder {
		if h[i].ID != id {
			t.Fatalf("history[%d] = %q, want %q", i, h[i].ID, id)
		}
	}
}

func TestJudgedByEventTimeNotArrivalTime(t *testing.T) {
	l := New(1, 10*time.Second)

	// Physically arrives first, but its event time is later.
	if v, _ := l.Submit("future", at(9)); v != Accepted {
		t.Fatalf("future: got %s, want ACCEPTED", v)
	}
	// Arrives second but happened earlier: takes the earlier slot and
	// evicts "future" from the single-slot window.
	v, corr := l.Submit("past", at(0))
	if v != Accepted {
		t.Fatalf("past: got %s, want ACCEPTED", v)
	}
	if len(corr) != 1 || corr[0].ID != "future" || corr[0].To != Rejected {
		t.Fatalf("corrections = %v, want future revoked", corr)
	}
}

func TestRevocationCanBeRevertedByEvenLaterArrival(t *testing.T) {
	l := New(1, 10*time.Second)

	l.Submit("a", at(5)) // accepted
	v, corr := l.Submit("b", at(1))
	if v != Accepted || len(corr) != 1 || corr[0].ID != "a" {
		t.Fatalf("after b: v=%s corr=%v, want a revoked", v, corr)
	}
	// An even later arrival at t=-6 pushes b out (inside b's window) while
	// falling outside a's window, rehabilitating a.
	v, corr = l.Submit("c", at(-6))
	if v != Accepted {
		t.Fatalf("c: got %s, want ACCEPTED", v)
	}
	if len(corr) != 2 {
		t.Fatalf("after c: corr=%v, want 2 corrections", corr)
	}
	// b: ACCEPTED -> REJECTED, a: REJECTED -> ACCEPTED.
	byID := map[string]Correction{}
	for _, c := range corr {
		byID[c.ID] = c
	}
	if byID["b"].To != Rejected || byID["a"].To != Accepted {
		t.Fatalf("corrections = %v, want b rejected and a re-accepted", corr)
	}
}
