package alertjoin

import (
	"fmt"
	"math/rand"
	"os"
	"reflect"
	"sort"
	"strconv"
	"testing"
)

func TestMatchWithinWindow(t *testing.T) {
	j := NewJoiner(5)
	l := Event{ID: "a", Time: 100, Content: "x"}
	r := Event{ID: "a", Time: 104, Content: "y"}
	if err := j.AddLeft(l); err != nil {
		t.Fatal(err)
	}
	if err := j.AddRight(r); err != nil {
		t.Fatal(err)
	}
	want := []Pair{{Left: l, Right: r}}
	if got := j.Pairs(); !reflect.DeepEqual(got, want) {
		t.Fatalf("pairs = %v, want %v", got, want)
	}
}

func TestBoundaryWindowMatches(t *testing.T) {
	j := NewJoiner(5)
	// |dt| == d must still match.
	if err := j.AddRight(Event{ID: "a", Time: 105, Content: "y"}); err != nil {
		t.Fatal(err)
	}
	if err := j.AddLeft(Event{ID: "a", Time: 100, Content: "x"}); err != nil {
		t.Fatal(err)
	}
	if n := len(j.Pairs()); n != 1 {
		t.Fatalf("pairs = %d, want 1", n)
	}
}

func TestWindowExceededNoMatch(t *testing.T) {
	j := NewJoiner(5)
	if err := j.AddLeft(Event{ID: "a", Time: 100, Content: "x"}); err != nil {
		t.Fatal(err)
	}
	if err := j.AddRight(Event{ID: "a", Time: 106, Content: "y"}); err != nil {
		t.Fatal(err)
	}
	if n := len(j.Pairs()); n != 0 {
		t.Fatalf("pairs = %d, want 0", n)
	}
}

func TestDuplicateIgnored(t *testing.T) {
	j := NewJoiner(5)
	ev := Event{ID: "a", Time: 100, Content: "x"}
	for i := 0; i < 3; i++ {
		if err := j.AddLeft(ev); err != nil {
			t.Fatal(err)
		}
	}
	if err := j.AddRight(Event{ID: "a", Time: 100, Content: "y"}); err != nil {
		t.Fatal(err)
	}
	// Pair emitted exactly once despite repeated left delivery.
	if n := len(j.Pairs()); n != 1 {
		t.Fatalf("pairs = %d, want 1", n)
	}
	if n, _ := j.Buffered(); n != 1 {
		t.Fatalf("left buffered = %d, want 1", n)
	}
}

func TestConflictReported(t *testing.T) {
	j := NewJoiner(5)
	if err := j.AddLeft(Event{ID: "a", Time: 100, Content: "x"}); err != nil {
		t.Fatal(err)
	}
	err := j.AddLeft(Event{ID: "a", Time: 100, Content: "DIFFERENT"})
	if err == nil {
		t.Fatal("expected conflict error, got nil")
	}
	if _, ok := err.(*ConflictError); !ok {
		t.Fatalf("error type = %T, want *ConflictError", err)
	}
	// Timestamp change with same payload is also a conflict.
	if err := j.AddLeft(Event{ID: "a", Time: 101, Content: "x"}); err == nil {
		t.Fatal("expected conflict error for changed timestamp, got nil")
	}
}

func TestReclaimOnlyAfterOppositeWatermarkPasses(t *testing.T) {
	j := NewJoiner(5)
	if err := j.AddLeft(Event{ID: "a", Time: 10, Content: "x"}); err != nil {
		t.Fatal(err)
	}
	// Right watermark reaching time+d exactly must NOT reclaim.
	j.AdvanceRightWatermark(15)
	if n, _ := j.Buffered(); n != 1 {
		t.Fatalf("buffered after wm=15 = %d, want 1", n)
	}
	// Right watermark passing time+d reclaims the left event.
	j.AdvanceRightWatermark(16)
	if n, _ := j.Buffered(); n != 0 {
		t.Fatalf("buffered after wm=16 = %d, want 0", n)
	}
}

func TestOwnWatermarkDoesNotReclaim(t *testing.T) {
	j := NewJoiner(5)
	if err := j.AddLeft(Event{ID: "a", Time: 10, Content: "x"}); err != nil {
		t.Fatal(err)
	}
	// Advancing the LEFT watermark must not reclaim left events.
	j.AdvanceLeftWatermark(1000)
	if n, _ := j.Buffered(); n != 1 {
		t.Fatalf("buffered = %d, want 1", n)
	}
}

func TestWatermarkMonotone(t *testing.T) {
	j := NewJoiner(5)
	j.AdvanceLeftWatermark(50)
	j.AdvanceLeftWatermark(30) // regression ignored
	if wm, _ := j.Watermarks(); wm != 50 {
		t.Fatalf("left watermark = %d, want 50", wm)
	}
}

func TestOutOfOrderArrival(t *testing.T) {
	j := NewJoiner(3)
	// Right side arrives first, out of order relative to left.
	if err := j.AddRight(Event{ID: "b", Time: 50, Content: "r2"}); err != nil {
		t.Fatal(err)
	}
	if err := j.AddRight(Event{ID: "a", Time: 10, Content: "r1"}); err != nil {
		t.Fatal(err)
	}
	if err := j.AddLeft(Event{ID: "a", Time: 12, Content: "l1"}); err != nil {
		t.Fatal(err)
	}
	if err := j.AddLeft(Event{ID: "b", Time: 48, Content: "l2"}); err != nil {
		t.Fatal(err)
	}
	if n := len(j.Pairs()); n != 2 {
		t.Fatalf("pairs = %d, want 2", n)
	}
}

// A duplicate may legally arrive after the match buffer entry was reclaimed
// (opposite watermark passed time+d) as long as the side's own watermark has
// not passed the event time. It must still be ignored, and a conflicting
// re-delivery in that window must still error.
func TestDedupSurvivesMatchBufferReclaim(t *testing.T) {
	j := NewJoiner(5)
	l := Event{ID: "a", Time: 10, Content: "x"}
	r := Event{ID: "a", Time: 12, Content: "y"}
	if err := j.AddLeft(l); err != nil {
		t.Fatal(err)
	}
	if err := j.AddRight(r); err != nil {
		t.Fatal(err)
	}
	if n := len(j.Pairs()); n != 1 {
		t.Fatalf("pairs = %d, want 1", n)
	}
	// Left watermark passes 12+5: right event reclaimed from match buffer.
	j.AdvanceLeftWatermark(18)
	if _, r := j.Buffered(); r != 0 {
		t.Fatalf("right buffered = %d, want 0", r)
	}
	// Right watermark is still 0, so a right duplicate may legally arrive.
	if err := j.AddRight(r); err != nil {
		t.Fatal(err)
	}
	if n := len(j.Pairs()); n != 1 {
		t.Fatalf("pairs after duplicate = %d, want 1", n)
	}
	// Conflicting re-delivery is still detected via the tombstone.
	if err := j.AddRight(Event{ID: "a", Time: 12, Content: "CHANGED"}); err == nil {
		t.Fatal("expected conflict error after match-buffer reclaim, got nil")
	}
}

// Once the side's own watermark passes the event time, no duplicate can
// legally arrive, so the tombstone is reclaimed and the id is reusable.
func TestTombstoneReclaimedByOwnWatermark(t *testing.T) {
	j := NewJoiner(5)
	if err := j.AddLeft(Event{ID: "a", Time: 10, Content: "x"}); err != nil {
		t.Fatal(err)
	}
	j.AdvanceLeftWatermark(10) // not past the event time yet
	j.AdvanceLeftWatermark(11) // now past: tombstone reclaimed
	// Same id with different content is a fresh event now, not a conflict.
	if err := j.AddLeft(Event{ID: "a", Time: 20, Content: "CHANGED"}); err != nil {
		t.Fatalf("unexpected error after tombstone reclaim: %v", err)
	}
}

// bruteForce computes the expected pairs by deduplicating each side by id
// (first occurrence wins) and filtering the full Cartesian product.
func bruteForce(left, right []Event, d int64) []Pair {
	dedup := func(evs []Event) []Event {
		seen := make(map[string]bool)
		var out []Event
		for _, ev := range evs {
			if !seen[ev.ID] {
				seen[ev.ID] = true
				out = append(out, ev)
			}
		}
		return out
	}
	var pairs []Pair
	for _, l := range dedup(left) {
		for _, r := range dedup(right) {
			if l.ID == r.ID && abs(l.Time-r.Time) <= d {
				pairs = append(pairs, Pair{Left: l, Right: r})
			}
		}
	}
	return pairs
}

func sortPairs(pairs []Pair) {
	sort.Slice(pairs, func(i, k int) bool {
		if pairs[i].Left.ID != pairs[k].Left.ID {
			return pairs[i].Left.ID < pairs[k].Left.ID
		}
		return pairs[i].Left.Time < pairs[k].Left.Time
	})
}

// TestBruteForceCrossCheck drives the joiner with out-of-order input and
// non-synchronized watermarks, then compares emitted pairs against a full
// Cartesian-product filter of the whole input.
func TestBruteForceCrossCheck(t *testing.T) {
	iterations := 300
	if v, err := strconv.Atoi(os.Getenv("ALERTJOIN_ITERS")); err == nil && v > 0 {
		iterations = v
	}
	for seed := int64(0); seed < int64(iterations); seed++ {
		rng := rand.New(rand.NewSource(seed))
		d := int64(1 + rng.Intn(10))
		maxTime := int64(50 + rng.Intn(100))
		idPool := 1 + rng.Intn(12)

		// Per (side, id) the event is fixed; duplicates are exact copies.
		build := func(side string) []Event {
			var evs []Event
			for i := 0; i < idPool; i++ {
				if rng.Intn(100) < 70 {
					id := fmt.Sprintf("id-%d", i)
					evs = append(evs, Event{
						ID:      id,
						Time:    rng.Int63n(maxTime),
						Content: fmt.Sprintf("%s-%s", side, id),
					})
				}
			}
			// Inject duplicate deliveries.
			var stream []Event
			for _, ev := range evs {
				for k := 0; k < 1+rng.Intn(3); k++ {
					stream = append(stream, ev)
				}
			}
			rng.Shuffle(len(stream), func(a, b int) { stream[a], stream[b] = stream[b], stream[a] })
			return stream
		}
		leftStream := build("L")
		rightStream := build("R")

		j := NewJoiner(d)

		// Interleave both streams randomly; after each event, maybe advance
		// one side's watermark. A watermark never exceeds the minimum time
		// of that side's undelivered events (watermark semantics), but the
		// two sides advance independently.
		type item struct {
			side int
			ev   Event
		}
		var queue []item
		for _, ev := range leftStream {
			queue = append(queue, item{left, ev})
		}
		for _, ev := range rightStream {
			queue = append(queue, item{right, ev})
		}
		rng.Shuffle(len(queue), func(a, b int) { queue[a], queue[b] = queue[b], queue[a] })

		minPending := func(idx int, side int) int64 {
			m := maxTime
			for ; idx < len(queue); idx++ {
				if queue[idx].side == side && queue[idx].ev.Time < m {
					m = queue[idx].ev.Time
				}
			}
			return m
		}

		for i, it := range queue {
			var err error
			if it.side == left {
				err = j.AddLeft(it.ev)
			} else {
				err = j.AddRight(it.ev)
			}
			if err != nil {
				t.Fatalf("seed %d: unexpected error: %v", seed, err)
			}
			if rng.Intn(100) < 40 {
				lo, _ := j.Watermarks()
				hi := minPending(i+1, left)
				if hi > lo {
					j.AdvanceLeftWatermark(lo + rng.Int63n(hi-lo+1))
				}
			}
			if rng.Intn(100) < 40 {
				_, lo := j.Watermarks()
				hi := minPending(i+1, right)
				if hi > lo {
					j.AdvanceRightWatermark(lo + rng.Int63n(hi-lo+1))
				}
			}
		}

		// Final flush: push both watermarks past any possible match.
		j.AdvanceLeftWatermark(maxTime + d + 1)
		j.AdvanceRightWatermark(maxTime + d + 1)
		if l, r := j.Buffered(); l != 0 || r != 0 {
			t.Fatalf("seed %d: buffered after flush = (%d, %d), want (0, 0)", seed, l, r)
		}

		got := j.Pairs()
		want := bruteForce(leftStream, rightStream, d)
		sortPairs(got)
		sortPairs(want)
		if len(got) != len(want) || (len(got) > 0 && !reflect.DeepEqual(got, want)) {
			t.Fatalf("seed %d (d=%d):\n got %v\nwant %v", seed, d, got, want)
		}
	}
}
