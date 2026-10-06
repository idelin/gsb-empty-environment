package eventlog

import (
	"math/rand"
	"testing"
)

var observeTags = []string{"alpha", "beta", "gamma"}

// randomLog builds a log mixing commutative and non-commutative operations
// with public observation points.
func randomLog(r *rand.Rand, n int) []Event {
	events := make([]Event, 0, n)
	for i := 0; i < n; i++ {
		switch r.Intn(6) {
		case 0, 1:
			events = append(events, Event{Kind: OpAdd, Arg: r.Intn(21) - 10})
		case 2:
			events = append(events, Event{Kind: OpMul, Arg: r.Intn(7) - 3})
		case 3:
			events = append(events, Event{Kind: OpSet, Arg: r.Intn(100) - 50})
		default:
			events = append(events, Event{Kind: OpObserve, Tag: observeTags[r.Intn(len(observeTags))]})
		}
	}
	return events
}

func TestCompressMergesOnlyCommutativeRuns(t *testing.T) {
	events := []Event{
		{Kind: OpAdd, Arg: 1},
		{Kind: OpAdd, Arg: 2},
		{Kind: OpMul, Arg: 3},
		{Kind: OpMul, Arg: 4},
		{Kind: OpAdd, Arg: 5},
		{Kind: OpSet, Arg: 0},
		{Kind: OpSet, Arg: 7},
		{Kind: OpAdd, Arg: 6},
	}
	cl := Compress(10, events)
	if len(cl.Segments) != 1 {
		t.Fatalf("got %d segments, want 1", len(cl.Segments))
	}
	got := cl.Segments[0].Merged
	want := []Event{
		{Kind: OpAdd, Arg: 3, Count: 2},
		{Kind: OpMul, Arg: 12, Count: 2},
		{Kind: OpAdd, Arg: 5, Count: 1},
		{Kind: OpSet, Arg: 0, Count: 1},
		{Kind: OpSet, Arg: 7, Count: 1},
		{Kind: OpAdd, Arg: 6, Count: 1},
	}
	if len(got) != len(want) {
		t.Fatalf("merged %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("merged[%d] = %+v, want %+v (all: %v)", i, got[i], want[i], got)
		}
	}
	if len(cl.Segments[0].Originals) != len(events) {
		t.Fatalf("originals = %d events, want %d", len(cl.Segments[0].Originals), len(events))
	}
	if err := Validate(cl); err != nil {
		t.Fatalf("Validate: %v", err)
	}
}

func TestCompressEmptyLog(t *testing.T) {
	cl := Compress(42, nil)
	if cl.Final.Value != 42 || len(cl.Observations) != 0 || len(cl.Segments) != 1 {
		t.Fatalf("unexpected compressed log: %+v", cl)
	}
	if err := Validate(cl); err != nil {
		t.Fatalf("Validate: %v", err)
	}
}

func TestCompressPreservesObservationPoints(t *testing.T) {
	events := []Event{
		{Kind: OpAdd, Arg: 5},
		{Kind: OpObserve, Tag: "first"},
		{Kind: OpMul, Arg: 2},
		{Kind: OpObserve, Tag: "second"},
		{Kind: OpSet, Arg: -3},
	}
	cl := Compress(10, events)
	want := []Observation{
		{Seq: 1, Tag: "first", Value: 15},
		{Seq: 3, Tag: "second", Value: 30},
	}
	if len(cl.Observations) != len(want) {
		t.Fatalf("observations = %v, want %v", cl.Observations, want)
	}
	for i := range want {
		if cl.Observations[i] != want[i] {
			t.Fatalf("observation[%d] = %+v, want %+v", i, cl.Observations[i], want[i])
		}
	}
	if len(cl.Segments) != 3 {
		t.Fatalf("got %d segments, want 3", len(cl.Segments))
	}
	if err := Validate(cl); err != nil {
		t.Fatalf("Validate: %v", err)
	}
}

// TestRandomLogsCompressLosslessly checks, over many random logs, that the
// compressed log validates, preserves every observation, and never grows.
func TestRandomLogsCompressLosslessly(t *testing.T) {
	for seed := int64(0); seed < 300; seed++ {
		r := rand.New(rand.NewSource(seed))
		initial := r.Intn(200) - 100
		events := randomLog(r, r.Intn(80))
		cl := Compress(initial, events)
		if err := Validate(cl); err != nil {
			t.Fatalf("seed %d: Validate: %v", seed, err)
		}

		m := NewMachine(initial)
		for _, ev := range events {
			m.Apply(ev)
		}
		if cl.Final != m.Summary() {
			t.Fatalf("seed %d: final summary %+v, want %+v", seed, cl.Final, m.Summary())
		}
		wantObs := m.Observations()
		if len(cl.Observations) != len(wantObs) {
			t.Fatalf("seed %d: %d observations, want %d", seed, len(cl.Observations), len(wantObs))
		}
		for i := range wantObs {
			if cl.Observations[i] != wantObs[i] {
				t.Fatalf("seed %d: observation[%d] = %+v, want %+v", seed, i, cl.Observations[i], wantObs[i])
			}
		}

		merged, originals := 0, 0
		for _, seg := range cl.Segments {
			merged += len(seg.Merged)
			originals += len(seg.Originals)
		}
		if merged > originals {
			t.Fatalf("seed %d: compression grew the log: %d merged > %d originals", seed, merged, originals)
		}
	}
}

// badCompressByTypeCount simulates a faulty compressor that merges
// operations purely by per-type counts inside each segment, destroying the
// relative order of non-commutative operations.
func badCompressByTypeCount(initial int, events []Event) CompressedLog {
	cl := Compress(initial, events) // correct summaries, observations, originals
	for i := range cl.Segments {
		seg := &cl.Segments[i]
		var adds, muls, sets int
		sum, product, lastSet := 0, 1, 0
		for _, s := range seg.Originals {
			switch s.Kind {
			case OpAdd:
				adds++
				sum += s.Arg
			case OpMul:
				muls++
				product *= s.Arg
			case OpSet:
				sets++
				lastSet = s.Arg
			}
		}
		merged := make([]Event, 0, 3)
		if sets > 0 {
			merged = append(merged, Event{Kind: OpSet, Arg: lastSet, Count: sets})
		}
		if adds > 0 {
			merged = append(merged, Event{Kind: OpAdd, Arg: sum, Count: adds})
		}
		if muls > 0 {
			merged = append(merged, Event{Kind: OpMul, Arg: product, Count: muls})
		}
		seg.Merged = merged
	}
	return cl
}

// mergedEquivalent reports whether two compressed logs' merged streams
// produce the same value at every observation boundary and at the end.
func mergedEquivalent(a, b CompressedLog) bool {
	ma, mb := NewMachine(a.Initial.Value), NewMachine(b.Initial.Value)
	for i := range a.Segments {
		for _, ev := range a.Segments[i].Merged {
			ma.Apply(ev)
		}
		for _, ev := range b.Segments[i].Merged {
			mb.Apply(ev)
		}
		if ma.Summary().Value != mb.Summary().Value {
			return false
		}
	}
	return true
}

// TestValidatorRejectsTypeCountMerges feeds random logs through the faulty
// type-count compressor and checks that Validate rejects exactly those
// outputs whose merged stream changed meaning.
func TestValidatorRejectsTypeCountMerges(t *testing.T) {
	rejected := 0
	for seed := int64(0); seed < 500; seed++ {
		r := rand.New(rand.NewSource(1000 + seed))
		initial := r.Intn(200) - 100
		events := randomLog(r, 5+r.Intn(40))
		good := Compress(initial, events)
		bad := badCompressByTypeCount(initial, events)

		err := Validate(bad)
		if mergedEquivalent(good, bad) {
			if err != nil {
				t.Fatalf("seed %d: equivalent compression rejected: %v", seed, err)
			}
		} else {
			if err == nil {
				t.Fatalf("seed %d: diverging type-count compression accepted", seed)
			}
			rejected++
		}
	}
	if rejected == 0 {
		t.Fatal("test never exercised a diverging bad compression")
	}
	t.Logf("rejected %d diverging type-count compressions", rejected)
}

// TestValidatorRejectsNonCommutativeReorder is a concrete regression case:
// add and mul do not commute, so merging by type count moves an add across
// a mul and changes the observed value.
func TestValidatorRejectsNonCommutativeReorder(t *testing.T) {
	events := []Event{
		{Kind: OpSet, Arg: 5},
		{Kind: OpAdd, Arg: 3},
		{Kind: OpObserve, Tag: "checkpoint"},
		{Kind: OpMul, Arg: 2},
		{Kind: OpAdd, Arg: 4},
	}
	bad := badCompressByTypeCount(0, events)
	// Faulty merged stream of segment 1: Add(4) then Mul(2) => (15+4)*2 = 38
	// instead of the correct 15*2+4 = 34.
	if err := Validate(bad); err == nil {
		t.Fatal("accepted compression that moved an add across a mul")
	}
}

func TestValidatorRejectsTampering(t *testing.T) {
	events := []Event{
		{Kind: OpAdd, Arg: 3},
		{Kind: OpMul, Arg: 2},
		{Kind: OpObserve, Tag: "mid"},
		{Kind: OpSet, Arg: 9},
		{Kind: OpAdd, Arg: 1},
	}
	base := Compress(4, events)
	if err := Validate(base); err != nil {
		t.Fatalf("baseline must validate: %v", err)
	}

	cases := map[string]func(*CompressedLog){
		"final value changed": func(cl *CompressedLog) {
			cl.Final = NewSummary(cl.Final.Value+1, cl.Final.Applied, cl.Final.Observed)
		},
		"final hash corrupted": func(cl *CompressedLog) {
			cl.Final.Hash++
		},
		"initial value changed": func(cl *CompressedLog) {
			cl.Initial = NewSummary(cl.Initial.Value+1, 0, 0)
		},
		"observation value changed": func(cl *CompressedLog) {
			cl.Observations[0].Value++
		},
		"observation seq changed": func(cl *CompressedLog) {
			cl.Observations[0].Seq++
		},
		"observation record dropped": func(cl *CompressedLog) {
			cl.Observations = cl.Observations[:0]
			cl.Final = NewSummary(cl.Final.Value, cl.Final.Applied, 0)
		},
		"observation tag changed": func(cl *CompressedLog) {
			cl.Observations[0].Tag = "other"
		},
		"original dropped": func(cl *CompressedLog) {
			cl.Segments[0].Originals = cl.Segments[0].Originals[1:]
		},
		"originals reordered": func(cl *CompressedLog) {
			o := cl.Segments[0].Originals
			o[0], o[1] = o[1], o[0]
		},
		"merged event dropped": func(cl *CompressedLog) {
			cl.Segments[1].Merged = cl.Segments[1].Merged[:0]
		},
		"merged count inflated": func(cl *CompressedLog) {
			cl.Segments[0].Merged[0].Count++
		},
		"segment dropped": func(cl *CompressedLog) {
			cl.Segments = cl.Segments[:1]
		},
		"observe buried mid-segment": func(cl *CompressedLog) {
			cl.Segments[0].Originals = append([]EventSummary{{Kind: OpObserve}}, cl.Segments[0].Originals...)
		},
	}
	for name, mutate := range cases {
		cl := base
		cl.Observations = append([]Observation(nil), base.Observations...)
		cl.Segments = make([]Segment, len(base.Segments))
		for i, seg := range base.Segments {
			cl.Segments[i] = Segment{
				Merged:    append([]Event(nil), seg.Merged...),
				Originals: append([]EventSummary(nil), seg.Originals...),
			}
		}
		mutate(&cl)
		if err := Validate(cl); err == nil {
			t.Errorf("%s: tampered log accepted", name)
		}
	}
}
