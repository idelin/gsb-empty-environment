package joiner

import (
	"fmt"
	"testing"
	"time"
)

const (
	window   = 5 * time.Second
	lateness = 5 * time.Second
)

type recorder struct{ results []Result }

func (r *recorder) emit(res Result) { r.results = append(r.results, res) }

func (r *recorder) types() []ResultType {
	out := make([]ResultType, len(r.results))
	for i, res := range r.results {
		out[i] = res.Type
	}
	return out
}

func newHarness() (*Joiner, *FakeClock, *recorder) {
	clock := NewFakeClock(time.Unix(0, 0))
	rec := &recorder{}
	j := New(clock, window, lateness, rec.emit)
	return j, clock, rec
}

func left(key string) Event  { return Event{Key: key, Side: Left, Kind: Data, Payload: key} }
func right(key string) Event { return Event{Key: key, Side: Right, Kind: Data, Payload: key} }
func cancel(key string) Event {
	return Event{Key: key, Side: Left, Kind: Cancel, Payload: key}
}

func assertTypes(t *testing.T, rec *recorder, want ...ResultType) {
	t.Helper()
	got := rec.types()
	if len(got) != len(want) {
		t.Fatalf("result count = %d (%v), want %d (%v)", len(got), got, len(want), want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("result[%d] = %v, want %v (all: %v)", i, got[i], want[i], got)
		}
	}
}

func assertNoLeak(t *testing.T, j *Joiner) {
	t.Helper()
	if n := j.StateSize(); n != 0 {
		t.Fatalf("state leak: StateSize() = %d, want 0", n)
	}
}

// Acceptance: L1 at t=0, R1 at t=3 -> paired result immediately.
func TestMatchWithinWindow(t *testing.T) {
	j, clock, rec := newHarness()

	j.Ingest(left("L1"))           // t=0
	clock.Advance(3 * time.Second) // t=3
	j.Ingest(right("L1"))

	assertTypes(t, rec, Matched)
	got := rec.results[0]
	if got.Key != "L1" || got.Left == nil || got.Right == nil {
		t.Fatalf("malformed match result: %+v", got)
	}
	if got.At != clock.Now() {
		t.Fatalf("match emitted at %v, want immediate emit at t=3", got.At)
	}

	// No orphan output when the original t=5 deadline passes.
	clock.Advance(10 * time.Second)
	assertTypes(t, rec, Matched)
	assertNoLeak(t, j)
}

// Acceptance: L2 at t=10, cancel at t=12, no orphan output at t=15.
func TestCancelSuppressesOrphanOutput(t *testing.T) {
	j, clock, rec := newHarness()

	clock.Advance(10 * time.Second) // t=10
	j.Ingest(left("L2"))
	clock.Advance(2 * time.Second) // t=12
	j.Ingest(cancel("L2"))

	// t=15: the original deadline passes silently.
	clock.Advance(3 * time.Second)
	assertTypes(t, rec /* nothing */)

	// Far past every tombstone: still nothing, and no state left behind.
	clock.Advance(time.Hour)
	assertTypes(t, rec)
	assertNoLeak(t, j)
}

// A left event with no match and no cancel emits LeftOnly at the deadline.
func TestTimeoutEmitsLeftOnly(t *testing.T) {
	j, clock, rec := newHarness()

	j.Ingest(left("L1"))
	clock.Advance(window - time.Second)
	assertTypes(t, rec) // not yet

	clock.Advance(time.Second) // t=5, deadline
	assertTypes(t, rec, LeftOnly)
	if rec.results[0].At != time.Unix(5, 0) {
		t.Fatalf("LeftOnly emitted at %v, want t=5", rec.results[0].At)
	}

	clock.Advance(time.Hour)
	assertNoLeak(t, j)
}

// Late R after the orphan output triggers a compensation result.
func TestLateRightCompensates(t *testing.T) {
	j, clock, rec := newHarness()

	j.Ingest(left("L1"))
	clock.Advance(window) // t=5: LeftOnly
	assertTypes(t, rec, LeftOnly)

	clock.Advance(2 * time.Second) // t=7: late R
	j.Ingest(right("L1"))
	assertTypes(t, rec, LeftOnly, LateMatched)
	if rec.results[1].Key != "L1" {
		t.Fatalf("late match key = %q, want L1", rec.results[1].Key)
	}

	clock.Advance(time.Hour)
	assertNoLeak(t, j)
}

// R arriving after the lateness bound is dropped (orphan tombstone expired).
func TestRightBeyondLatenessDropped(t *testing.T) {
	j, clock, rec := newHarness()

	j.Ingest(left("L1"))
	clock.Advance(window + lateness + time.Second) // t=11, tombstone gone
	j.Ingest(right("L1"))                          // buffered as an early R
	clock.Advance(window)                          // its own window expires
	assertTypes(t, rec, LeftOnly, RightOnly)

	clock.Advance(time.Hour)
	assertNoLeak(t, j)
}

// Out of order: R before its L pairs immediately when L arrives.
func TestRightBeforeLeft(t *testing.T) {
	j, clock, rec := newHarness()

	j.Ingest(right("K"))
	clock.Advance(2 * time.Second)
	j.Ingest(left("K"))
	assertTypes(t, rec, Matched)

	clock.Advance(time.Hour)
	assertTypes(t, rec, Matched)
	assertNoLeak(t, j)
}

// Out of order: cancel before its L suppresses the late L entirely.
func TestCancelBeforeLeft(t *testing.T) {
	j, clock, rec := newHarness()

	j.Ingest(cancel("K"))
	clock.Advance(time.Second)
	j.Ingest(left("K")) // dropped: cancel tombstone is active
	clock.Advance(time.Hour)
	assertTypes(t, rec)
	assertNoLeak(t, j)
}

// Interleaved out-of-order traffic across many keys ends with zero state.
func TestOutOfOrderZeroLeak(t *testing.T) {
	j, clock, rec := newHarness()

	j.Ingest(left("A"))        // t=0
	clock.Advance(time.Second) // t=1
	j.Ingest(right("D"))       // R before L: buffered
	clock.Advance(time.Second) // t=2
	j.Ingest(left("B"))
	clock.Advance(time.Second) // t=3
	j.Ingest(right("A"))       // A pairs -> MATCHED
	clock.Advance(time.Second) // t=4
	j.Ingest(left("D"))        // D pairs with buffered R -> MATCHED
	clock.Advance(time.Second) // t=5
	j.Ingest(cancel("E"))      // cancel before L: tombstone
	clock.Advance(time.Second) // t=6
	j.Ingest(left("C"))
	j.Ingest(cancel("B"))          // B cancelled while waiting: silent
	clock.Advance(2 * time.Second) // t=8
	j.Ingest(left("F"))
	clock.Advance(time.Second) // t=9
	j.Ingest(left("E"))        // suppressed by the early cancel
	clock.Advance(time.Second) // t=10
	j.Ingest(left("G"))
	clock.Advance(time.Second)     // t=11: C times out -> LEFT-ONLY
	j.Ingest(right("C"))           // late R -> LATE-MATCHED compensation
	clock.Advance(2 * time.Second) // t=13: F times out -> LEFT-ONLY
	clock.Advance(2 * time.Second) // t=15: G times out -> LEFT-ONLY

	assertTypes(t, rec,
		Matched,     // A at t=3
		Matched,     // D at t=4
		LeftOnly,    // C at t=11
		LateMatched, // C at t=11 (late R)
		LeftOnly,    // F at t=13
		LeftOnly,    // G at t=15
	)

	clock.Advance(time.Hour) // every tombstone expires
	assertTypes(t, rec, Matched, Matched, LeftOnly, LateMatched, LeftOnly, LeftOnly)
	assertNoLeak(t, j)
}

// Real-clock smoke test: the joiner works against wall time too.
func TestRealClockSmoke(t *testing.T) {
	rec := &recorder{}
	j := New(RealClock{}, 60*time.Millisecond, 60*time.Millisecond, rec.emit)

	j.Ingest(left("fast"))
	j.Ingest(right("fast")) // immediate match

	j.Ingest(left("slow"))
	time.Sleep(120 * time.Millisecond) // lets the 60ms timeout fire

	deadline := time.Now().Add(2 * time.Second)
	for {
		j.mu.Lock()
		n := len(rec.results)
		j.mu.Unlock()
		if n >= 2 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for orphan output, got %v", rec.types())
		}
		time.Sleep(5 * time.Millisecond)
	}
	assertTypes(t, rec, Matched, LeftOnly)

	// Eventually all tombstones expire.
	deadline = time.Now().Add(2 * time.Second)
	for j.StateSize() != 0 {
		if time.Now().After(deadline) {
			t.Fatalf("state leak with real clock: StateSize() = %d", j.StateSize())
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func ExampleJoiner() {
	clock := NewFakeClock(time.Unix(0, 0))
	j := New(clock, 5*time.Second, 5*time.Second, func(r Result) {
		fmt.Printf("%s key=%s\n", r.Type, r.Key)
	})
	j.Ingest(Event{Key: "L1", Side: Left})
	clock.Advance(3 * time.Second)
	j.Ingest(Event{Key: "L1", Side: Right})
	// Output:
	// MATCHED key=L1
}
