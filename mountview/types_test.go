package mountview

import "testing"

func TestPropagationRoundTrip(t *testing.T) {
	for _, tc := range []struct {
		text string
		want Propagation
	}{
		{"shared", Shared}, {"slave", Slave}, {"private", Private},
	} {
		got, err := ParsePropagation(tc.text)
		if err != nil || got != tc.want || got.String() != tc.text {
			t.Fatalf("round trip %s: got=%s err=%v", tc.text, got, err)
		}
	}
	if _, err := ParsePropagation("bogus"); err == nil {
		t.Fatal("unknown propagation must error")
	}
}

func TestIntervalRelations(t *testing.T) {
	base := Interval{Offset: 10, Len: 10} // [10,20)
	if !base.Contains(Interval{Offset: 10, Len: 10}) {
		t.Fatal("interval should contain itself")
	}
	if base.Contains(Interval{Offset: 15, Len: 10}) {
		t.Fatal("interval must not contain an exceeding range")
	}
	if !base.Overlaps(Interval{Offset: 19, Len: 5}) {
		t.Fatal("adjacent-by-one ranges overlap")
	}
	if base.Overlaps(Interval{Offset: 20, Len: 5}) {
		t.Fatal("half-open ranges touching at the end must not overlap")
	}
}

func TestPermPackingAndRelaxation(t *testing.T) {
	for bits := uint8(0); bits < 8; bits++ {
		if PermFromBits(bits).Bits() != bits {
			t.Fatalf("perm packing not bijective at %d", bits)
		}
	}
	if !rwx().RelaxedBy(ro()) {
		t.Fatal("rwx must be relaxed relative to read-only")
	}
	if ro().RelaxedBy(rwx()) {
		t.Fatal("read-only is not a relaxation of rwx")
	}
}

func TestNopVerifierAcceptsUnsignedRecords(t *testing.T) {
	iv := Interval{Offset: 0, Len: 8}
	// Manifests/checkpoints without signatures: only NopVerifier accepts them.
	m := &Manifest{Path: "u", Group: 1, Object: "obj-u", Interval: iv, Perm: ro(), Propagation: Private}
	c := &Checkpoint{Seq: 1, View: []ViewBinding{binding("u", 1, "obj-u", iv, ro(), Private, 0)}}
	e := NewEngine()
	if err := e.Replay(&Input{
		Manifests:   []*Manifest{m},
		Checkpoints: []*Checkpoint{c},
		Verify:      NopVerifier(),
	}); err != nil {
		t.Fatalf("nop verifier should accept unsigned records: %v", err)
	}
	res, err := e.Resolve("u")
	if err != nil || res.Verdict != Recoverable {
		t.Fatalf("nop replay resolve: %+v err=%v", res, err)
	}
}

func TestReplayRejectsBadInput(t *testing.T) {
	if err := NewEngine().Replay(nil); err == nil {
		t.Fatal("nil input must error")
	}
	e := NewEngine()
	if err := e.Replay(&Input{Entries: []*LogEntry{{Seq: 1, Op: OpMove}}}); err == nil {
		t.Fatal("recovery without a genesis checkpoint must error")
	}
	if _, err := NewEngine().Resolve("../escape"); err == nil {
		t.Fatal("escaping resolve target must error")
	}
}
