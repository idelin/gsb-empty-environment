package batch

import (
	"math/rand"
	"reflect"
	"testing"
)

func mustTracker(t *testing.T, total int64) *Tracker {
	t.Helper()
	tr, err := NewTracker(total)
	if err != nil {
		t.Fatalf("NewTracker(%d): %v", total, err)
	}
	return tr
}

func TestDispatchSplitsTotalIntoChunks(t *testing.T) {
	tr := mustTracker(t, 10)
	leases := tr.Dispatch(4, 0)
	if len(leases) != 3 {
		t.Fatalf("got %d leases, want 3", len(leases))
	}
	want := []Interval{{0, 4}, {4, 8}, {8, 10}}
	var got []Interval
	for _, l := range leases {
		got = append(got, l.Range)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ranges = %v, want %v", got, want)
	}
	// Everything is leased now; nothing more to dispatch.
	if again := tr.Dispatch(4, 0); len(again) != 0 {
		t.Fatalf("re-dispatch while active handed out %v", again)
	}
}

func TestConfirmPrefixThenFailRedispatchesRemainder(t *testing.T) {
	tr := mustTracker(t, 100)
	l := tr.Dispatch(100, 1)[0]

	// Worker confirms the prefix [0, 30) then fails.
	if err := tr.ConfirmPrefix(l.ID, 30); err != nil {
		t.Fatalf("ConfirmPrefix: %v", err)
	}
	if err := tr.Fail(l.ID); err != nil {
		t.Fatalf("Fail: %v", err)
	}

	// Re-dispatch must contain exactly the unconfirmed remainder.
	next := tr.Dispatch(100, 0)
	if len(next) != 1 || next[0].Range != (Interval{30, 100}) {
		t.Fatalf("re-dispatch = %v, want [{30 100}]", next)
	}
	if next[0].ID == l.ID {
		t.Fatalf("re-dispatch reused lease ID %d", l.ID)
	}
	if got := tr.Unconfirmed(); !reflect.DeepEqual(got, []Interval{{30, 100}}) {
		t.Fatalf("Unconfirmed = %v", got)
	}
}

func TestOverlappingConfirmationsAreIdempotent(t *testing.T) {
	tr := mustTracker(t, 10)
	l := tr.Dispatch(10, 1)[0]

	for _, iv := range []Interval{{0, 4}, {2, 6}, {3, 5}, {0, 4}, {5, 10}} {
		if err := tr.Confirm(l.ID, iv); err != nil {
			t.Fatalf("Confirm %v: %v", iv, err)
		}
	}
	if got := tr.ConfirmedCount(); got != 10 {
		t.Fatalf("ConfirmedCount = %d, want 10 (overlaps double-counted)", got)
	}
	if got := tr.Confirmed(); !reflect.DeepEqual(got, []Interval{{0, 10}}) {
		t.Fatalf("Confirmed = %v, want [{0 10}]", got)
	}
	if !tr.Complete() {
		t.Fatal("Complete = false after full coverage")
	}
}

func TestOutOfBoundsRejected(t *testing.T) {
	tr := mustTracker(t, 10)
	l := tr.Dispatch(5, 1)[0] // lease [0, 5)

	cases := []Interval{
		{-1, 3},  // before lease start
		{3, 6},   // past lease end
		{0, 11},  // past total
		{5, 5},   // empty
		{4, 2},   // inverted
		{-5, -1}, // fully negative
	}
	for _, iv := range cases {
		if err := tr.Confirm(l.ID, iv); err != ErrOutOfBounds {
			t.Fatalf("Confirm %v = %v, want ErrOutOfBounds", iv, err)
		}
	}
	if err := tr.ConfirmPrefix(l.ID, 6); err != ErrOutOfBounds {
		t.Fatalf("ConfirmPrefix(6) = %v, want ErrOutOfBounds", err)
	}
	if err := tr.ConfirmPrefix(l.ID, -1); err != ErrOutOfBounds {
		t.Fatalf("ConfirmPrefix(-1) = %v, want ErrOutOfBounds", err)
	}
	if got := tr.ConfirmedCount(); got != 0 {
		t.Fatalf("rejected confirms changed state: ConfirmedCount = %d", got)
	}
	if _, err := NewTracker(0); err == nil {
		t.Fatal("NewTracker(0) accepted non-positive total")
	}
}

func TestOutOfOrderReceipts(t *testing.T) {
	tr := mustTracker(t, 12)
	leases := tr.Dispatch(3, 0) // [0,3) [3,6) [6,9) [9,12)

	// Receipts arrive out of order, including partial prefixes.
	steps := []struct {
		lease int
		upto  int64
		done  bool
	}{
		{2, 9, false},  // [6,9) fully done
		{0, 2, false},  // [0,3) prefix to 2
		{3, 12, false}, // [9,12) fully done
		{1, 6, false},  // [3,6) fully done
		{0, 3, true},   // [0,3) finishes -> complete
	}
	for i, s := range steps {
		if err := tr.ConfirmPrefix(leases[s.lease].ID, s.upto); err != nil {
			t.Fatalf("step %d: ConfirmPrefix: %v", i, err)
		}
		if got := tr.Complete(); got != s.done {
			t.Fatalf("step %d: Complete = %v, want %v", i, got, s.done)
		}
	}
	if got := tr.Confirmed(); !reflect.DeepEqual(got, []Interval{{0, 12}}) {
		t.Fatalf("Confirmed = %v, want [{0 12}]", got)
	}
}

func TestLeaseTurnoverRejectsStaleReceipts(t *testing.T) {
	tr := mustTracker(t, 10)
	old := tr.Dispatch(10, 1)[0]

	if err := tr.ConfirmPrefix(old.ID, 4); err != nil {
		t.Fatalf("ConfirmPrefix: %v", err)
	}
	if err := tr.Fail(old.ID); err != nil {
		t.Fatalf("Fail: %v", err)
	}

	// Late receipt on the old generation must be rejected.
	if err := tr.ConfirmPrefix(old.ID, 8); err != ErrStaleLease {
		t.Fatalf("stale ConfirmPrefix = %v, want ErrStaleLease", err)
	}
	if err := tr.Confirm(old.ID, Interval{4, 8}); err != ErrStaleLease {
		t.Fatalf("stale Confirm = %v, want ErrStaleLease", err)
	}
	if err := tr.Confirm(9999, Interval{4, 8}); err != ErrUnknownLease {
		t.Fatalf("unknown lease = %v, want ErrUnknownLease", err)
	}

	// New generation covers only the unconfirmed remainder.
	next := tr.Dispatch(10, 0)
	if len(next) != 1 || next[0].Range != (Interval{4, 10}) {
		t.Fatalf("re-dispatch = %v, want [{4 10}]", next)
	}
	if err := tr.ConfirmPrefix(next[0].ID, 10); err != nil {
		t.Fatalf("new generation ConfirmPrefix: %v", err)
	}
	if !tr.Complete() {
		t.Fatal("Complete = false after new generation finished")
	}
}

func TestDuplicateReceiptOnFinishedLeaseIsNoOp(t *testing.T) {
	tr := mustTracker(t, 10)
	l := tr.Dispatch(10, 1)[0]
	if err := tr.ConfirmPrefix(l.ID, 10); err != nil {
		t.Fatalf("ConfirmPrefix: %v", err)
	}
	// At-least-once delivery replays the receipt: must stay idempotent.
	for i := 0; i < 3; i++ {
		if err := tr.ConfirmPrefix(l.ID, 10); err != nil {
			t.Fatalf("duplicate ConfirmPrefix: %v", err)
		}
		if err := tr.Confirm(l.ID, Interval{0, 10}); err != nil {
			t.Fatalf("duplicate Confirm: %v", err)
		}
	}
	if got := tr.ConfirmedCount(); got != 10 {
		t.Fatalf("ConfirmedCount = %d, want 10", got)
	}
	// But out-of-bounds is still rejected even on a finished lease.
	if err := tr.ConfirmPrefix(l.ID, 11); err != ErrOutOfBounds {
		t.Fatalf("ConfirmPrefix(11) = %v, want ErrOutOfBounds", err)
	}
}

func TestFailWithoutProgressRedispatchesWholeRange(t *testing.T) {
	tr := mustTracker(t, 6)
	l := tr.Dispatch(6, 1)[0]
	if err := tr.ConfirmPrefix(l.ID, 0); err != nil { // nothing done
		t.Fatalf("ConfirmPrefix(0): %v", err)
	}
	if err := tr.Fail(l.ID); err != nil {
		t.Fatalf("Fail: %v", err)
	}
	next := tr.Dispatch(6, 0)
	if len(next) != 1 || next[0].Range != (Interval{0, 6}) {
		t.Fatalf("re-dispatch = %v, want [{0 6}]", next)
	}
}

// TestRandomizedAgainstModel drives the tracker with random operations and
// cross-checks the interval-union state against a naive per-item model.
func TestRandomizedAgainstModel(t *testing.T) {
	const total = 50
	rng := rand.New(rand.NewSource(1))
	for trial := 0; trial < 20; trial++ {
		tr := mustTracker(t, total)
		model := make([]bool, total)
		var active []uint64

		for step := 0; step < 400; step++ {
			switch rng.Intn(4) {
			case 0: // dispatch
				for _, l := range tr.Dispatch(int64(1+rng.Intn(9)), 3) {
					active = append(active, l.ID)
				}
			case 1: // confirm prefix of a random active lease
				if len(active) == 0 {
					continue
				}
				i := rng.Intn(len(active))
				id := active[i]
				l := tr.leases[id]
				upto := l.Range.Start + rng.Int63n(l.Range.Len()+1)
				if err := tr.ConfirmPrefix(id, upto); err != nil {
					t.Fatalf("ConfirmPrefix(%d, %d): %v", id, upto, err)
				}
				for j := l.Range.Start; j < upto; j++ {
					model[j] = true
				}
			case 2: // fail a random active lease
				if len(active) == 0 {
					continue
				}
				i := rng.Intn(len(active))
				if err := tr.Fail(active[i]); err != nil {
					t.Fatalf("Fail: %v", err)
				}
				active = append(active[:i], active[i+1:]...)
			case 3: // stale receipt must be rejected and change nothing
				if err := tr.ConfirmPrefix(uint64(1000+step), 10); err != ErrUnknownLease {
					t.Fatalf("bogus lease = %v, want ErrUnknownLease", err)
				}
			}

			// Cross-check union against model after every step.
			want := []Interval{}
			for i := int64(0); i < total; i++ {
				if !model[i] {
					continue
				}
				j := i
				for j+1 < total && model[j+1] {
					j++
				}
				want = append(want, Interval{i, j + 1})
				i = j
			}
			if got := tr.Confirmed(); !reflect.DeepEqual(got, want) {
				t.Fatalf("trial %d step %d: Confirmed = %v, want %v", trial, step, got, want)
			}
			all := true
			for _, ok := range model {
				all = all && ok
			}
			if got := tr.Complete(); got != all {
				t.Fatalf("trial %d step %d: Complete = %v, want %v", trial, step, got, all)
			}
			if got := tr.ConfirmedCount(); got != countTrue(model) {
				t.Fatalf("trial %d step %d: ConfirmedCount = %d, want %d", trial, step, got, countTrue(model))
			}
		}
	}
}

func countTrue(b []bool) int64 {
	var n int64
	for _, v := range b {
		if v {
			n++
		}
	}
	return n
}
