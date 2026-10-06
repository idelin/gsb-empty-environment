package alertjoin

import (
	"fmt"
	"math"
	"math/rand"
	"reflect"
	"sort"
	"testing"
)

func pairIDs(p Pair) string { return p.Left.ID + "/" + p.Right.ID }

func emittedIDs(pairs []Pair) []string {
	ids := make([]string, 0, len(pairs))
	for _, p := range pairs {
		ids = append(ids, pairIDs(p))
	}
	sort.Strings(ids)
	return ids
}

func TestMatchWithinInterval(t *testing.T) {
	j := New(10)
	if got, _ := j.AddEvent(Left, Event{ID: "L1", Key: "a", Time: 100}); len(got) != 0 {
		t.Fatalf("no match expected yet, got %v", got)
	}
	got, err := j.AddEvent(Right, Event{ID: "R1", Key: "a", Time: 110})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || pairIDs(got[0]) != "L1/R1" {
		t.Fatalf("want exactly one pair L1/R1, got %v", emittedIDs(got))
	}
	// Different key never matches.
	if got, _ := j.AddEvent(Right, Event{ID: "R2", Key: "b", Time: 105}); len(got) != 0 {
		t.Fatalf("key mismatch must not match, got %v", emittedIDs(got))
	}
}

func TestBoundaryInclusive(t *testing.T) {
	j := New(10)
	j.AddEvent(Left, Event{ID: "L1", Key: "a", Time: 100})
	got, _ := j.AddEvent(Right, Event{ID: "R1", Key: "a", Time: 90}) // |dt| == d
	if len(got) != 1 {
		t.Fatalf("|dt| == d must match, got %v", emittedIDs(got))
	}
	if got, _ := j.AddEvent(Right, Event{ID: "R2", Key: "a", Time: 89}); len(got) != 0 {
		t.Fatalf("|dt| == d+1 must not match, got %v", emittedIDs(got))
	}
}

func TestDuplicateIgnoredConflictReported(t *testing.T) {
	j := New(10)
	e := Event{ID: "L1", Key: "a", Time: 100, Payload: "p1"}
	j.AddEvent(Left, e)
	j.AddEvent(Right, Event{ID: "R1", Key: "a", Time: 100})

	// Identical re-delivery: ignored, no new pairs, no error.
	got, err := j.AddEvent(Left, e)
	if err != nil || len(got) != 0 {
		t.Fatalf("duplicate must be ignored, got pairs=%v err=%v", emittedIDs(got), err)
	}
	if n := len(j.Pairs()); n != 1 {
		t.Fatalf("pair must be emitted exactly once, got %d", n)
	}

	// Same ID, different content: conflict error.
	_, err = j.AddEvent(Left, Event{ID: "L1", Key: "a", Time: 100, Payload: "p2"})
	ce, ok := err.(*ConflictError)
	if !ok {
		t.Fatalf("want *ConflictError, got %v", err)
	}
	if ce.Side != Left || ce.ID != "L1" || ce.Existing.Payload != "p1" || ce.Incoming.Payload != "p2" {
		t.Fatalf("unexpected conflict detail: %+v", ce)
	}
}

func TestEvictionOnlyByOppositeWatermark(t *testing.T) {
	j := New(10)
	j.AddEvent(Left, Event{ID: "L1", Key: "a", Time: 100})
	j.AddEvent(Right, Event{ID: "R1", Key: "a", Time: 200})

	// Own watermark must not reclaim own buffer.
	j.UpdateWatermark(Left, 150)
	if j.Buffered(Left) != 1 {
		t.Fatalf("own watermark must not reclaim own events")
	}
	// Opposite watermark at exactly t+d is not enough ("越过" means strictly past).
	j.UpdateWatermark(Right, 110)
	if j.Buffered(Left) != 1 {
		t.Fatalf("watermark == t+d must not reclaim")
	}
	j.UpdateWatermark(Right, 111)
	if j.Buffered(Left) != 0 {
		t.Fatalf("watermark > t+d must reclaim")
	}
	// R1 (t=200) still buffered: left watermark is MinInt64.
	if j.Buffered(Right) != 1 {
		t.Fatalf("R1 must survive until left watermark passes 210")
	}
	// Non-monotonic watermark updates are ignored.
	j.UpdateWatermark(Right, 50)
	if j.Watermark(Right) != 111 {
		t.Fatalf("regressive watermark must be ignored")
	}
}

func TestLateEventDropped(t *testing.T) {
	j := New(10)
	j.UpdateWatermark(Left, 50)
	got, err := j.AddEvent(Left, Event{ID: "L1", Key: "a", Time: 50})
	if err != nil || len(got) != 0 || j.Buffered(Left) != 0 {
		t.Fatalf("late event must be dropped silently")
	}
	if _, err := j.AddEvent(Left, Event{ID: "L2", Key: "a", Time: 51}); err != nil || j.Buffered(Left) != 1 {
		t.Fatalf("event past watermark must be accepted")
	}
}

// TestRandomizedAgainstCartesian feeds out-of-order events, duplicates,
// conflicts and independently advancing (unsynchronized) watermarks, then
// compares the emitted pairs against a brute-force Cartesian-product
// filter over the deduplicated inputs.
func TestRandomizedAgainstCartesian(t *testing.T) {
	const d = int64(10)
	for seed := int64(0); seed < 300; seed++ {
		r := rand.New(rand.NewSource(seed))
		j := New(d)

		type op struct {
			isWM     bool
			side     Side
			ev       Event
			wm       int64
			conflict bool // expected to fail with *ConflictError
		}

		// Build per-side event ops: fresh events plus duplicate/conflicting
		// re-deliveries. Times are random in [0, 100]; delivery order is
		// shuffled, so input is out of order.
		var pending [2][]op
		oracle := [2]map[string]Event{Left: {}, Right: {}}
		names := [2]string{"L", "R"}
		for _, s := range []Side{Left, Right} {
			type keyed struct {
				o   op
				key float64
			}
			var gen []keyed
			n := 5 + r.Intn(30)
			for i := 0; i < n; i++ {
				e := Event{
					ID:      fmt.Sprintf("%s%d", names[s], i),
					Key:     fmt.Sprintf("k%d", r.Intn(3)),
					Time:    int64(r.Intn(101)),
					Payload: fmt.Sprintf("p%d", r.Intn(5)),
				}
				k := r.Float64()
				gen = append(gen, keyed{op{side: s, ev: e}, k})
				// With some probability, schedule a re-delivery; its sort
				// key keeps it strictly after the original delivery.
				if r.Float64() < 0.35 {
					dup := e
					conflict := r.Float64() < 0.4
					if conflict {
						dup.Payload = "tampered"
					}
					gen = append(gen, keyed{op{side: s, ev: dup, conflict: conflict},
						k + (1-k)*(0.01+0.98*r.Float64())})
				}
			}
			// Out-of-order delivery within the side.
			sort.Slice(gen, func(a, b int) bool { return gen[a].key < gen[b].key })
			for _, g := range gen {
				pending[s] = append(pending[s], g.o)
			}
		}

		// Interleave event delivery with watermark advances. A watermark w
		// for side s is only valid if every not-yet-delivered event op of
		// side s has Time > w; this keeps the watermark contract while the
		// two sides advance independently.
		var wm [2]int64
		wm[Left], wm[Right] = math.MinInt64, math.MinInt64
		minPending := func(s Side) int64 {
			m := int64(math.MaxInt64)
			for _, o := range pending[s] {
				if o.ev.Time < m {
					m = o.ev.Time
				}
			}
			return m
		}

		var ops []op
		for len(pending[Left])+len(pending[Right]) > 0 {
			if r.Float64() < 0.3 {
				s := Side(r.Intn(2))
				if len(pending[s]) == 0 {
					continue
				}
				lo, hi := wm[s], minPending(s)-1
				if hi > lo {
					if lo == math.MinInt64 {
						lo = -1 << 40
					}
					w := lo + 1 + r.Int63n(hi-lo)
					wm[s] = w
					ops = append(ops, op{isWM: true, side: s, wm: w})
				}
				continue
			}
			s := Side(r.Intn(2))
			if len(pending[s]) == 0 {
				s = s.other()
			}
			ops = append(ops, pending[s][0])
			pending[s] = pending[s][1:]
		}

		// Replay.
		for i, o := range ops {
			if o.isWM {
				j.UpdateWatermark(o.side, o.wm)
				continue
			}
			_, err := j.AddEvent(o.side, o.ev)
			if o.conflict {
				if _, ok := err.(*ConflictError); !ok {
					t.Fatalf("seed %d op %d: want ConflictError for %+v, got %v", seed, i, o.ev, err)
				}
				continue // conflicting copy rejected; oracle keeps first version
			}
			if err != nil {
				t.Fatalf("seed %d op %d: unexpected error %v", seed, i, err)
			}
			if prev, seen := oracle[o.side][o.ev.ID]; !seen {
				oracle[o.side][o.ev.ID] = o.ev
			} else if !prev.sameContent(o.ev) {
				t.Fatalf("seed %d op %d: unflagged conflict", seed, i)
			}
		}

		// Oracle: full Cartesian product, filtered by key and |dt| <= d.
		want := map[string]bool{}
		for _, l := range oracle[Left] {
			for _, rr := range oracle[Right] {
				if l.Key == rr.Key && absDiff(l.Time, rr.Time) <= d {
					want[l.ID+"/"+rr.ID] = true
				}
			}
		}
		gotPairs := j.Pairs()
		got := map[string]bool{}
		for _, p := range gotPairs {
			id := pairIDs(p)
			if got[id] {
				t.Fatalf("seed %d: pair %s emitted twice", seed, id)
			}
			got[id] = true
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("seed %d: mismatch\n got: %v\nwant: %v", seed, sortedKeys(got), sortedKeys(want))
		}

		// After both watermarks pass every possible event time + d, all
		// state must be reclaimed.
		j.UpdateWatermark(Left, math.MaxInt64)
		j.UpdateWatermark(Right, math.MaxInt64)
		if j.Buffered(Left) != 0 || j.Buffered(Right) != 0 {
			t.Fatalf("seed %d: state not fully reclaimed: L=%d R=%d",
				seed, j.Buffered(Left), j.Buffered(Right))
		}
	}
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
