package slideq

import (
	"math/rand"
	"reflect"
	"sort"
	"testing"
)

func TestFractionNormalization(t *testing.T) {
	cases := []struct{ num, den, wantNum, wantDen int64 }{
		{2, 4, 1, 2},
		{1, -2, -1, 2},
		{-3, -9, 1, 3},
		{0, 5, 0, 1},
	}
	for _, c := range cases {
		got := NewFraction(c.num, c.den)
		if got != (Fraction{Num: c.wantNum, Den: c.wantDen}) {
			t.Errorf("NewFraction(%d,%d) = %v", c.num, c.den, got)
		}
	}
}

func TestQuantileFloorIndex(t *testing.T) {
	// n = 4 -> index floor(q*3): q=1/2 -> 1, q=2/3 -> 2, q=1 -> 3.
	values := []float64{4, 1, 3, 2}
	cases := []struct {
		q    Fraction
		want float64
	}{
		{NewFraction(0, 1), 1},
		{NewFraction(1, 3), 2},
		{NewFraction(1, 2), 2},
		{NewFraction(2, 3), 3},
		{NewFraction(1, 1), 4},
	}
	for _, c := range cases {
		if got := Quantile(values, c.q); got != c.want {
			t.Errorf("Quantile(%v) = %v, want %v", c.q, got, c.want)
		}
	}
}

func TestQuantileDuplicates(t *testing.T) {
	values := []float64{2, 2, 2, 5, 5}
	// n = 5, q = 1/2 -> index 2 -> 2.
	if got := Quantile(values, NewFraction(1, 2)); got != 2 {
		t.Errorf("got %v, want 2", got)
	}
	// q = 3/4 -> index 3 -> 5.
	if got := Quantile(values, NewFraction(3, 4)); got != 5 {
		t.Errorf("got %v, want 5", got)
	}
}

func TestWindowBoundaries(t *testing.T) {
	a := NewAggregator(NewFraction(1, 2), 10, 5)
	// Event exactly on a window end belongs to that window (right-closed).
	out := a.Insert(Event{ID: "a", Timestamp: 10, Value: 1})
	if len(out) != 0 {
		t.Fatalf("unexpected emissions before watermark: %v", out)
	}
	// Event exactly on a window start is excluded from it (left-open).
	a.Insert(Event{ID: "b", Timestamp: 5, Value: 100})

	out = a.AdvanceWatermark(10)
	// Window (-5,5] is right-closed, so it holds the t=5 event alone;
	// window (0,10] holds both events; window (5,15] is not complete.
	want := []Emission{
		{Window: Window{Start: -5, End: 5}, Value: 100},
		{Window: Window{Start: 0, End: 10}, Value: 1},
	}
	if !reflect.DeepEqual(out, want) {
		t.Fatalf("got %v, want %v", out, want)
	}

	// (5,15] must not contain the event at t=5: only t=10 qualifies.
	out = a.AdvanceWatermark(15)
	want = []Emission{{Window: Window{Start: 5, End: 15}, Value: 1}}
	if !reflect.DeepEqual(out, want) {
		t.Fatalf("got %v, want %v", out, want)
	}
}

func TestEmptyWindowEmitsNothing(t *testing.T) {
	a := NewAggregator(NewFraction(1, 2), 10, 5)
	a.Insert(Event{ID: "a", Timestamp: 7, Value: 3})
	out := a.AdvanceWatermark(20)
	// Only windows containing t=7 emit: (0,10] and (5,15]. The grid
	// window (10,20] is empty and must stay silent.
	want := []Emission{
		{Window: Window{Start: 0, End: 10}, Value: 3},
		{Window: Window{Start: 5, End: 15}, Value: 3},
	}
	if !reflect.DeepEqual(out, want) {
		t.Fatalf("got %v, want %v", out, want)
	}
}

func TestWatermarkNeverRegresses(t *testing.T) {
	a := NewAggregator(NewFraction(1, 2), 10, 5)
	a.Insert(Event{ID: "a", Timestamp: 3, Value: 9})
	a.AdvanceWatermark(12)
	if out := a.AdvanceWatermark(12); out != nil {
		t.Fatalf("equal watermark emitted %v", out)
	}
	if out := a.AdvanceWatermark(4); out != nil {
		t.Fatalf("regressed watermark emitted %v", out)
	}
	if wm, ok := a.Watermark(); !ok || wm != 12 {
		t.Fatalf("watermark = %v,%v want 12,true", wm, ok)
	}
}

func TestLateReviseRetractsAndReemits(t *testing.T) {
	a := NewAggregator(NewFraction(1, 2), 10, 5)
	a.Insert(Event{ID: "a", Timestamp: 3, Value: 5})
	a.Insert(Event{ID: "b", Timestamp: 4, Value: 10})
	out := a.AdvanceWatermark(10)
	want := []Emission{
		{Window: Window{Start: -5, End: 5}, Value: 5},
		{Window: Window{Start: 0, End: 10}, Value: 5},
	}
	if !reflect.DeepEqual(out, want) {
		t.Fatalf("got %v, want %v", out, want)
	}

	// Late correction inside an already emitted window.
	out = a.Revise(Event{ID: "b", Timestamp: 4, Value: 1})
	want = []Emission{
		{Window: Window{Start: -5, End: 5}, Value: 5, Retract: true},
		{Window: Window{Start: -5, End: 5}, Value: 1},
		{Window: Window{Start: 0, End: 10}, Value: 5, Retract: true},
		{Window: Window{Start: 0, End: 10}, Value: 1},
	}
	if !reflect.DeepEqual(out, want) {
		t.Fatalf("got %v, want %v", out, want)
	}
}

func TestLateRetractEmitsRetractionOnly(t *testing.T) {
	a := NewAggregator(NewFraction(1, 2), 10, 5)
	a.Insert(Event{ID: "a", Timestamp: 3, Value: 7})
	a.AdvanceWatermark(10)

	// Removing the only event empties the windows: retract, no new value.
	out := a.Retract("a")
	want := []Emission{
		{Window: Window{Start: -5, End: 5}, Value: 7, Retract: true},
		{Window: Window{Start: 0, End: 10}, Value: 7, Retract: true},
	}
	if !reflect.DeepEqual(out, want) {
		t.Fatalf("got %v, want %v", out, want)
	}
	if out := a.Retract("missing"); out != nil {
		t.Fatalf("retract of unknown id emitted %v", out)
	}
}

// TestModelRebuild runs random op sequences and, after every op,
// rebuilds the sorted sets from scratch to cross-check the emitted
// state. Values come from a tiny set to force duplicates, timestamps
// land on and around grid points to exercise window boundaries, and
// retractions drain windows to exercise empty-window handling.
func TestModelRebuild(t *testing.T) {
	qs := []Fraction{
		NewFraction(0, 1), NewFraction(1, 3), NewFraction(1, 2),
		NewFraction(3, 7), NewFraction(2, 3), NewFraction(1, 1),
	}
	for seed := int64(0); seed < 8; seed++ {
		rng := rand.New(rand.NewSource(seed))
		size, slide := int64(10), int64(5)
		q := qs[rng.Intn(len(qs))]
		agg := NewAggregator(q, size, slide)
		ref := make(map[string]Event)
		emitted := make(map[Window]float64)
		var watermark int64
		watermarkInit := false

		applyEmissions := func(out []Emission) {
			t.Helper()
			for _, em := range out {
				if em.Retract {
					prev, ok := emitted[em.Window]
					if !ok {
						t.Fatalf("seed %d: retract of never emitted window %v", seed, em.Window)
					}
					if prev != em.Value {
						t.Fatalf("seed %d: retract value %v, emitted was %v", seed, em.Value, prev)
					}
					delete(emitted, em.Window)
				} else {
					emitted[em.Window] = em.Value
				}
			}
		}

		nextID := 0
		var ids []string
		for op := 0; op < 3000; op++ {
			// Timestamps cluster on grid multiples with small offsets to
			// hit left-open/right-closed boundaries and go negative.
			ts := 5*int64(rng.Intn(25)-5) + int64(rng.Intn(3)-1)
			value := float64(rng.Intn(4)) // duplicates galore
			switch rng.Intn(5) {
			case 0, 1: // insert
				id := string(rune('a' + nextID))
				nextID++
				ids = append(ids, id)
				e := Event{ID: id, Timestamp: ts, Value: value}
				applyEmissions(agg.Insert(e))
				ref[id] = e
			case 2: // revise
				if len(ids) == 0 {
					continue
				}
				id := ids[rng.Intn(len(ids))]
				if _, ok := ref[id]; !ok {
					continue
				}
				e := Event{ID: id, Timestamp: ts, Value: value}
				applyEmissions(agg.Revise(e))
				ref[id] = e
			case 3: // retract
				if len(ids) == 0 {
					continue
				}
				id := ids[rng.Intn(len(ids))]
				if _, ok := ref[id]; !ok {
					continue
				}
				applyEmissions(agg.Retract(id))
				delete(ref, id)
			case 4: // advance watermark, sometimes backwards
				newWM := ts
				if !watermarkInit || newWM > watermark {
					watermark, watermarkInit = newWM, true
				}
				applyEmissions(agg.AdvanceWatermark(newWM))
			}

			// Rebuild reference: every grid window ending at or behind
			// the watermark, computed from a freshly sorted value set.
			want := make(map[Window]float64)
			if watermarkInit {
				minTs := watermark
				have := false
				for _, e := range ref {
					if !have || e.Timestamp < minTs {
						minTs, have = e.Timestamp, true
					}
				}
				if have {
					for end := floorDiv(minTs, slide) * slide; end <= watermark; end += slide {
						w := Window{Start: end - size, End: end}
						var values []float64
						for _, e := range ref {
							if w.Contains(e.Timestamp) {
								values = append(values, e.Value)
							}
						}
						if len(values) == 0 {
							continue // empty window: no output expected
						}
						sort.Float64s(values)
						idx := q.MulFloor(int64(len(values) - 1))
						want[w] = values[idx]
					}
				}
			}
			if !reflect.DeepEqual(emitted, want) {
				t.Fatalf("seed %d op %d: emitted %v, rebuilt reference %v", seed, op, emitted, want)
			}
		}
	}
}
