package sliding

import (
	"math/rand"
	"reflect"
	"sort"
	"testing"
)

func TestQuantileIndex(t *testing.T) {
	cases := []struct {
		values []float64
		q      Fraction
		want   float64
	}{
		{[]float64{10, 20, 30, 40, 50}, MustFraction(0, 1), 10},
		{[]float64{10, 20, 30, 40, 50}, MustFraction(1, 1), 50},
		{[]float64{10, 20, 30, 40, 50}, MustFraction(1, 2), 30},
		{[]float64{1, 2, 3, 4}, MustFraction(1, 2), 2}, // floor(1/2*3)=1
		{[]float64{1, 2, 3, 4}, MustFraction(2, 3), 3}, // floor(2/3*3)=2
		{[]float64{1, 2, 3, 4}, MustFraction(1, 4), 1}, // floor(1/4*3)=0
		{[]float64{1, 2, 3, 4}, MustFraction(3, 4), 3}, // floor(3/4*3)=2
		{[]float64{7}, MustFraction(9, 10), 7},
	}
	for _, c := range cases {
		got, ok := Quantile(c.values, c.q)
		if !ok || got != c.want {
			t.Errorf("Quantile(%v, %d/%d) = %v, %v; want %v", c.values, c.q.Num, c.q.Den, got, ok, c.want)
		}
	}
	if _, ok := Quantile(nil, MustFraction(1, 2)); ok {
		t.Error("Quantile of empty set must report ok=false")
	}
}

func TestFractionValidation(t *testing.T) {
	if _, err := NewFraction(1, 0); err == nil {
		t.Error("zero denominator accepted")
	}
	if _, err := NewFraction(-1, 2); err == nil {
		t.Error("negative q accepted")
	}
	if _, err := NewFraction(3, 2); err == nil {
		t.Error("q > 1 accepted")
	}
	f := MustFraction(2, 4)
	if f.Num != 1 || f.Den != 2 {
		t.Errorf("2/4 not normalized: %+v", f)
	}
}

func newEngine(t *testing.T, size, slide, num, den int64) *Engine {
	t.Helper()
	e, err := NewEngine(size, slide, MustFraction(num, den))
	if err != nil {
		t.Fatal(err)
	}
	return e
}

func TestWindowBoundaries(t *testing.T) {
	e := newEngine(t, 10, 10, 1, 2)
	e.Upsert(Event{ID: "at-start", Value: 1, Time: 0}) // excluded: (0,10] is left-open
	e.Upsert(Event{ID: "at-end", Value: 2, Time: 10})  // included: right-closed
	e.Upsert(Event{ID: "next", Value: 3, Time: 11})    // belongs to (10,20]
	outs := e.AdvanceWatermark(10)
	want := []Output{{Kind: Emit, WindowStart: 0, WindowEnd: 10, Value: 2}}
	assertOutputs(t, outs, want)

	outs = e.AdvanceWatermark(20)
	want = []Output{{Kind: Emit, WindowStart: 10, WindowEnd: 20, Value: 3}}
	assertOutputs(t, outs, want)
}

func TestEmptyWindow(t *testing.T) {
	e := newEngine(t, 10, 10, 1, 2)
	if outs := e.AdvanceWatermark(35); len(outs) != 0 {
		t.Fatalf("empty windows must emit nothing, got %v", outs)
	}
	// Late event landing in an already-closed empty window: emit, no retraction.
	outs := e.Upsert(Event{ID: "late", Value: 9, Time: 25})
	want := []Output{{Kind: Emit, WindowStart: 20, WindowEnd: 30, Value: 9}}
	assertOutputs(t, outs, want)
}

func TestDuplicateValues(t *testing.T) {
	e := newEngine(t, 10, 10, 1, 2)
	e.Upsert(Event{ID: "a", Value: 5, Time: 1})
	e.Upsert(Event{ID: "b", Value: 5, Time: 2})
	e.Upsert(Event{ID: "c", Value: 5, Time: 3})
	e.Upsert(Event{ID: "d", Value: 1, Time: 4})
	// sorted [1,5,5,5], floor(1/2*3)=1 -> 5
	outs := e.AdvanceWatermark(10)
	assertOutputs(t, outs, []Output{{Kind: Emit, WindowStart: 0, WindowEnd: 10, Value: 5}})

	// Retract one duplicate: [1,5,5], floor(1/2*2)=1 -> 5, unchanged.
	if outs := e.Retract("b"); len(outs) != 0 {
		t.Fatalf("retracting one duplicate must not change the median, got %v", outs)
	}
	// Retract two more: [1], median becomes 1.
	outs = e.Retract("c")
	outs = append(outs, e.Retract("a")...)
	want := []Output{
		{Kind: Retract, WindowStart: 0, WindowEnd: 10, Value: 5},
		{Kind: Emit, WindowStart: 0, WindowEnd: 10, Value: 1},
	}
	assertOutputs(t, outs, want)
}

func TestLateCorrection(t *testing.T) {
	e := newEngine(t, 10, 10, 1, 2)
	e.Upsert(Event{ID: "a", Value: 1, Time: 5})
	e.Upsert(Event{ID: "b", Value: 2, Time: 6})
	e.Upsert(Event{ID: "c", Value: 3, Time: 7})
	outs := e.AdvanceWatermark(10)
	assertOutputs(t, outs, []Output{{Kind: Emit, WindowStart: 0, WindowEnd: 10, Value: 2}})

	// Late revision: [1,2,3] -> [2,3,100], median 2 -> 3.
	outs = e.Upsert(Event{ID: "a", Value: 100, Time: 5})
	want := []Output{
		{Kind: Retract, WindowStart: 0, WindowEnd: 10, Value: 2},
		{Kind: Emit, WindowStart: 0, WindowEnd: 10, Value: 3},
	}
	assertOutputs(t, outs, want)

	// Late retraction: [2,3,100] -> [2,100], floor(1/2*1)=0 -> 2.
	outs = e.Retract("c")
	want = []Output{
		{Kind: Retract, WindowStart: 0, WindowEnd: 10, Value: 3},
		{Kind: Emit, WindowStart: 0, WindowEnd: 10, Value: 2},
	}
	assertOutputs(t, outs, want)

	// Retract everything: window becomes empty, only a retraction remains.
	e.Retract("a")
	outs = e.Retract("b")
	want = []Output{{Kind: Retract, WindowStart: 0, WindowEnd: 10, Value: 2}}
	assertOutputs(t, outs, want)
}

func TestRevisionMovesAcrossWindows(t *testing.T) {
	e := newEngine(t, 10, 10, 1, 2)
	e.Upsert(Event{ID: "x", Value: 1, Time: 5})
	e.Upsert(Event{ID: "y", Value: 2, Time: 15})
	outs := e.AdvanceWatermark(20)
	assertOutputs(t, outs, []Output{
		{Kind: Emit, WindowStart: 0, WindowEnd: 10, Value: 1},
		{Kind: Emit, WindowStart: 10, WindowEnd: 20, Value: 2},
	})
	// Move x from t=5 to t=15: first window empties, second gains a member.
	outs = e.Upsert(Event{ID: "x", Value: 1, Time: 15})
	want := []Output{
		{Kind: Retract, WindowStart: 0, WindowEnd: 10, Value: 1},
		{Kind: Retract, WindowStart: 10, WindowEnd: 20, Value: 2},
		{Kind: Emit, WindowStart: 10, WindowEnd: 20, Value: 1},
	}
	assertOutputs(t, outs, want)
}

func TestWatermarkNeverRetreats(t *testing.T) {
	e := newEngine(t, 10, 10, 1, 2)
	e.Upsert(Event{ID: "a", Value: 1, Time: 5})
	e.AdvanceWatermark(100)
	if outs := e.AdvanceWatermark(50); len(outs) != 0 {
		t.Fatalf("retreating watermark produced outputs: %v", outs)
	}
	if e.Watermark() != 100 {
		t.Fatalf("watermark moved backwards: %d", e.Watermark())
	}
	if outs := e.AdvanceWatermark(100); len(outs) != 0 {
		t.Fatalf("equal watermark produced outputs: %v", outs)
	}
}

func TestRetractUnknownID(t *testing.T) {
	e := newEngine(t, 10, 10, 1, 2)
	if outs := e.Retract("nope"); len(outs) != 0 {
		t.Fatalf("retracting unknown ID produced outputs: %v", outs)
	}
}

func assertOutputs(t *testing.T, got, want []Output) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("outputs mismatch:\n got: %v\nwant: %v", got, want)
	}
}

// reference is an oracle that, after every operation, rebuilds the sorted
// set of every closed window from scratch and diffs it against what it
// previously made visible.
type reference struct {
	events    map[string]Event
	size      int64
	slide     int64
	q         Fraction
	watermark int64
	closed    map[int64]bool
	visible   map[int64]float64
}

func newReference(size, slide int64, q Fraction) *reference {
	return &reference{
		events:    make(map[string]Event),
		size:      size,
		slide:     slide,
		q:         q,
		watermark: -1 << 62,
		closed:    make(map[int64]bool),
		visible:   make(map[int64]float64),
	}
}

func (r *reference) upsert(e Event) []Output {
	r.events[e.ID] = e
	return r.sync()
}

func (r *reference) retract(id string) []Output {
	delete(r.events, id)
	return r.sync()
}

func (r *reference) advance(t int64) []Output {
	if t > r.watermark {
		r.watermark = t
	}
	return r.sync()
}

// sync closes newly passed windows, rebuilds every closed window's sorted
// set from the full event set, and emits the diff.
func (r *reference) sync() []Output {
	for end := r.size; end <= r.watermark; end += r.slide {
		r.closed[end] = true
	}
	ends := make([]int64, 0, len(r.closed))
	for end := range r.closed {
		ends = append(ends, end)
	}
	sort.Slice(ends, func(i, j int) bool { return ends[i] < ends[j] })

	var outs []Output
	for _, end := range ends {
		vals := make([]float64, 0)
		for _, e := range r.events {
			if e.Time > end-r.size && e.Time <= end {
				vals = append(vals, e.Value)
			}
		}
		sort.Float64s(vals)
		v, ok := Quantile(vals, r.q)
		old, had := r.visible[end]
		start := end - r.size
		switch {
		case had && ok && old == v:
		case had && ok:
			r.visible[end] = v
			outs = append(outs,
				Output{Kind: Retract, WindowStart: start, WindowEnd: end, Value: old},
				Output{Kind: Emit, WindowStart: start, WindowEnd: end, Value: v})
		case had && !ok:
			delete(r.visible, end)
			outs = append(outs, Output{Kind: Retract, WindowStart: start, WindowEnd: end, Value: old})
		case !had && ok:
			r.visible[end] = v
			outs = append(outs, Output{Kind: Emit, WindowStart: start, WindowEnd: end, Value: v})
		}
	}
	return outs
}

// normalized sorts outputs so that ordering differences between the
// incremental engine and the from-scratch oracle do not matter.
func normalized(outs []Output) []Output {
	cp := make([]Output, len(outs))
	copy(cp, outs)
	sort.Slice(cp, func(i, j int) bool {
		if cp[i].WindowEnd != cp[j].WindowEnd {
			return cp[i].WindowEnd < cp[j].WindowEnd
		}
		if cp[i].Kind != cp[j].Kind {
			return cp[i].Kind < cp[j].Kind
		}
		return cp[i].Value < cp[j].Value
	})
	return cp
}

func TestAgainstRebuildOracle(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	const (
		size  = 10
		slide = 5
		ops   = 5000
	)
	q := MustFraction(1, 2)
	eng, err := NewEngine(size, slide, q)
	if err != nil {
		t.Fatal(err)
	}
	ref := newReference(size, slide, q)
	wm := int64(0)

	for i := 0; i < ops; i++ {
		var got, want []Output
		switch rng.Intn(4) {
		case 0, 1: // upsert: small ID/value pools force revisions and duplicates
			ev := Event{
				ID:    string(rune('a' + rng.Intn(12))),
				Value: float64(rng.Intn(6)),
				Time:  int64(rng.Intn(80)),
			}
			got = eng.Upsert(ev)
			want = ref.upsert(ev)
		case 2: // retract
			id := string(rune('a' + rng.Intn(12)))
			got = eng.Retract(id)
			want = ref.retract(id)
		case 3: // advance watermark, sometimes not forward
			wm += int64(rng.Intn(15))
			if rng.Intn(10) == 0 {
				wm -= int64(rng.Intn(20))
			}
			got = eng.AdvanceWatermark(wm)
			want = ref.advance(wm)
		}
		if !reflect.DeepEqual(normalized(got), normalized(want)) {
			t.Fatalf("op %d: outputs mismatch:\n got: %v\nwant: %v", i, normalized(got), normalized(want))
		}
	}
	if !reflect.DeepEqual(eng.visible, ref.visible) {
		t.Fatalf("final visible state mismatch:\n got: %v\nwant: %v", eng.visible, ref.visible)
	}
}
