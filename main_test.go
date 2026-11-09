package main

import (
	"encoding/json"
	"math"
	"reflect"
	"strings"
	"testing"
)

func ev(parts ...any) []json.RawMessage {
	raw := make([]json.RawMessage, len(parts))
	for i, p := range parts {
		b, err := json.Marshal(p)
		if err != nil {
			panic(err)
		}
		raw[i] = b
	}
	return raw
}

func replay(t *testing.T, events ...[]json.RawMessage) []int64 {
	t.Helper()
	b, err := json.Marshal(events)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	got, err := Replay(strings.NewReader(string(b)))
	if err != nil {
		t.Fatalf("Replay: %v", err)
	}
	return got
}

// In-order lease then ack pushes the contiguous prefix straight through.
func TestInOrderAck(t *testing.T) {
	got := replay(t,
		ev("lease", 0, 10, 1),
		ev("ack", 0, 10, 1),
	)
	want := []int64{0, 10}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
}

// An ack for a stale generation is ignored, and a hole blocks the prefix
// until the missing range is acknowledged; the prefix then jumps across.
func TestStaleAckAndCrossHole(t *testing.T) {
	got := replay(t,
		ev("lease", 0, 100, 1),
		ev("ack", 10, 20, 1), // uncommitted, hole at [0,10)
		ev("ack", 0, 10, 2),  // wrong generation: ignored
		ev("ack", 0, 10, 1),  // prefix reaches 20
		ev("ack", 20, 100, 1),
	)
	want := []int64{0, 0, 0, 20, 100}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
}

// Reassignment clears uncommitted acks on the bumped sub-range only; an
// old-generation ack arriving late is dropped, and the prefix can only extend
// once the bumped range is acknowledged under its new generation.
func TestReassignmentClearsUncommittedAcks(t *testing.T) {
	got := replay(t,
		ev("lease", 0, 100, 1),
		ev("ack", 10, 90, 1),   // uncommitted: [0,10) is still a hole
		ev("lease", 60, 70, 2), // clears ack on [60,70); [10,60)+[70,90) remain
		ev("ack", 60, 70, 1),   // late stale receipt against gen 2: ignored
		ev("ack", 0, 10, 1),    // reaches 60, blocked at the cleared hole
		ev("ack", 60, 70, 2),   // hole filled under gen 2: prefix jumps to 90
	)
	want := []int64{0, 0, 0, 0, 60, 90}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
}

// A lease whose generation is not strictly newer changes nothing, including
// the uncommitted acks it overlaps.
func TestOlderLeaseIsNoOp(t *testing.T) {
	got := replay(t,
		ev("lease", 0, 100, 3),
		ev("ack", 0, 40, 3),
		ev("lease", 10, 90, 2), // older: ignored, acks stay
		ev("lease", 10, 90, 3), // equal: ignored too
		ev("ack", 40, 100, 3),
	)
	want := []int64{0, 40, 40, 40, 100}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
}

// Overlapping leases with differing generations keep per-sub-range
// generations, and acks only land where the generation matches.
func TestOverlappingLeases(t *testing.T) {
	got := replay(t,
		ev("lease", 0, 100, 1),
		ev("lease", 30, 70, 2), // [0,30)=1 [30,70)=2 [70,..)=1
		ev("ack", 0, 100, 1),   // confirms only [0,30) and [70,100)
		ev("ack", 30, 70, 2),   // fills the middle: full prefix
	)
	want := []int64{0, 0, 30, 100}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
}

// Committed offsets are frozen: later leases and stale acks cannot roll the
// checkpoint back or reopen committed ranges.
func TestCommittedPrefixIsFrozen(t *testing.T) {
	got := replay(t,
		ev("lease", 0, 100, 1),
		ev("ack", 0, 60, 1),
		ev("lease", 0, 60, 5), // committed region immune
		ev("ack", 0, 60, 1),   // stale against gen 5, but already committed
		ev("lease", 60, 100, 5),
		ev("ack", 60, 100, 5),
	)
	want := []int64{0, 60, 60, 60, 60, 100}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
}

// Acknowledgements split by reassignment holes still extend once the hole is
// re-acked, exercising cross-hole advancement with merged ack ranges.
func TestAckMergesAcrossFilledHole(t *testing.T) {
	got := replay(t,
		ev("lease", 0, 1000, 1),
		ev("ack", 100, 1000, 1),  // uncommitted: [0,100) is a hole
		ev("lease", 400, 600, 2), // clears ack [400,600); [100,400)+[600,1000) kept
		ev("ack", 0, 100, 1),     // reaches 400, blocked at the hole
		ev("ack", 400, 600, 2),   // contiguous again: reaches 1000
	)
	want := []int64{0, 0, 0, 400, 1000}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
}

// Coordinates at 10^12 must work without per-offset allocation; empty input
// yields an empty checkpoint array.
func TestHugeCoordinates(t *testing.T) {
	got := replay(t,
		ev("lease", 0, 1_000_000_000_000, 7),
		ev("ack", 0, 1_000_000_000_000, 7),
	)
	want := []int64{0, 1_000_000_000_000}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}

	got = replay(t)
	if len(got) != 0 {
		t.Fatalf("empty input: got %v want []", got)
	}
}

// Partial overlaps at the committed boundary and beyond the lease hi are
// clipped correctly.
func TestBoundaryClipping(t *testing.T) {
	got := replay(t,
		ev("lease", 10, 90, 1),
		ev("ack", 10, 90, 1), // [0,10) still gen 0, unacked: checkpoint 0
		ev("ack", 0, 10, 0),  // default generation 0 confirmed: reaches 90
		ev("ack", 90, 120, 0),
	)
	want := []int64{0, 0, 90, 120}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
}

// Repeated re-leasing of small ranges keeps treaps partition-shaped; the
// number of pieces stays proportional to the event count, not the span.
func TestManySmallReleasesStayCompact(t *testing.T) {
	s := newShard()
	const n = 500
	for i := range n {
		lo := int64(i * 2)
		s.lease(lo, lo+2, int64(i+1))
	}
	if got := countNodes(s.gens.root); got > 2*n+1 {
		t.Fatalf("gen pieces %d exceed O(events)", got)
	}
	if got := countNodes(s.acks.root); got != 0 {
		t.Fatalf("acks treap not empty: %d", got)
	}
}

// Stress at scale: interleaved leases and acks over a 10^12 span must stay
// fast and bounded in memory.
func TestLargeScaleStress(t *testing.T) {
	s := newShard()
	const n = 20000
	for i := range n {
		base := int64(i) * 1000
		s.lease(base, base+1000, int64(i%7)+1)
		if i%2 == 0 {
			s.ack(base, base+1000, int64(i%7)+1)
		}
	}
	if got := countNodes(s.gens.root); got > 2*n+1 {
		t.Fatalf("gen pieces %d too large", got)
	}
	if got := countNodes(s.acks.root); got > n/2+1 {
		t.Fatalf("ack pieces %d too large", got)
	}
	if s.committed < 0 || s.committed > math.MaxInt64 {
		t.Fatalf("bad checkpoint %d", s.committed)
	}
}

func TestInvalidEvents(t *testing.T) {
	bad := []string{
		`[["bogus",0,1,1]]`,
		`[["ack",5,1,1]]`,
		`[["ack",0,1]]`,
	}
	for _, in := range bad {
		if _, err := Replay(strings.NewReader(in)); err == nil {
			t.Fatalf("expected error for %s", in)
		}
	}
}

// A late ack from an older generation must not reopen anything, and an ack
// spanning several gen pieces only lands on matching pieces; the checkpoint
// waits for the right-generation receipts in their original order.
func TestLateOldReceiptAcrossPieces(t *testing.T) {
	got := replay(t,
		ev("lease", 0, 100, 1),
		ev("lease", 20, 40, 2),
		ev("lease", 60, 80, 3),
		ev("ack", 0, 100, 1), // only [0,20) and [40,60) and [80,100)
		ev("ack", 20, 40, 1), // stale: nothing
		ev("ack", 60, 80, 1), // stale: nothing
		ev("ack", 0, 20, 1),  // duplicate ack, still 20
		ev("ack", 20, 40, 2), // reaches 60
		ev("ack", 60, 80, 3), // reaches 100
	)
	want := []int64{0, 0, 0, 20, 20, 20, 20, 60, 100}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
}

func countNodes[V any](root *node[V]) int {
	if root == nil {
		return 0
	}
	return 1 + countNodes(root.left) + countNodes(root.right)
}
