package tbsim

import (
	"math/rand"
	"testing"
)

// scenario builds a 3-level tree: shared root -> class nodes -> flow leaves.
func scenario() ([]*Flow, map[string]*Bucket) {
	root := NewBucket("root", Rat(5, 2), 40, 0)
	classA := NewBucket("classA", Rat(2, 1), 24, 0)
	classB := NewBucket("classB", Rat(3, 2), 24, 0)
	leafA1 := NewBucket("leafA1", Rat(1, 1), 16, 0)
	leafB1 := NewBucket("leafB1", Rat(3, 4), 12, 0)
	leafB2 := NewBucket("leafB2", Rat(1, 1), 16, 0)

	rootN := &Node{Name: "root", Bucket: root}
	classAN := &Node{Name: "classA", Bucket: classA, Parent: rootN}
	classBN := &Node{Name: "classB", Bucket: classB, Parent: rootN}
	leafA1N := &Node{Name: "leafA1", Bucket: leafA1, Parent: classAN}
	leafB1N := &Node{Name: "leafB1", Bucket: leafB1, Parent: classBN}
	leafB2N := &Node{Name: "leafB2", Bucket: leafB2, Parent: classBN}

	flows := []*Flow{
		NewFlow("a1", leafA1N, 1, 2, 64, 0), // high priority
		NewFlow("b1", leafB1N, 0, 1, 64, 1),
		NewFlow("b2", leafB2N, 0, 3, 64, 2),
	}
	buckets := map[string]*Bucket{
		"root": root, "classA": classA, "classB": classB,
		"leafA1": leafA1, "leafB1": leafB1, "leafB2": leafB2,
	}
	return flows, buckets
}

func genTraffic(seed int64, n int) []Arrival {
	rng := rand.New(rand.NewSource(seed))
	names := []string{"a1", "b1", "b2"}
	var out []Arrival
	t := int64(0)
	for i := 0; i < n; i++ {
		t += int64(rng.Intn(6))
		out = append(out, Arrival{
			Time: t,
			Pkt:  Packet{ID: i, Flow: names[rng.Intn(3)], Size: int64(4 + rng.Intn(13))},
		})
	}
	return out
}

func controls() []ControlOp {
	return []ControlOp{
		{Time: 100, Kind: "setweight", Flow: "b1", Weight: 5},            // weight hot-swap
		{Time: 150, Kind: "setrate", Bucket: "root", RateN: 3, RateD: 1}, // rate hot-swap
		{Time: 200, Kind: "setweight", Flow: "b2", Weight: 1},
		{Time: 250, Kind: "cancel", Flow: "b1", PktID: 120}, // cancel queued pkt
		{Time: 300, Kind: "setrate", Bucket: "leafB2", RateN: 1, RateD: 2},
	}
}

func emitSeq(recs []EmitRecord) [][2]int64 {
	out := make([][2]int64, len(recs))
	for i, e := range recs {
		out[i] = [2]int64{e.Emit, int64(e.Pkt.ID)}
	}
	return out
}

func dropSeq(recs []DropRecord) [][2]int64 {
	out := make([][2]int64, len(recs))
	for i, d := range recs {
		out[i] = [2]int64{d.Time, int64(d.Pkt.ID)}
	}
	return out
}

func TestEventHopMatchesPerTickReference(t *testing.T) {
	arrivals := genTraffic(42, 250)
	ctl := controls()
	horizon := int64(3000)

	// Event-hopping shaper.
	flows1, buckets1 := scenario()
	s := NewShaper(flows1)
	for _, a := range arrivals {
		s.AddArrival(a)
	}
	for _, op := range ctl {
		s.AddControl(op)
	}
	s.Run()

	// Per-tick reference.
	flows2, buckets2 := scenario()
	refEmit, refDrop := RunRefShaper(flows2, arrivals, ctl, horizon)

	got, want := emitSeq(s.Emitted), emitSeq(refEmit)
	if len(got) != len(want) {
		t.Fatalf("emit count: event=%d ref=%d", len(got), len(want))
	}
	for i := range got {
		if got[i] != want[i] {
			t.Fatalf("emit %d: event=%v ref=%v", i, got[i], want[i])
		}
	}
	gd, wd := dropSeq(s.Dropped), dropSeq(refDrop)
	if len(gd) != len(wd) {
		t.Fatalf("drop count: event=%d ref=%d", len(gd), len(wd))
	}
	for i := range gd {
		if gd[i] != wd[i] {
			t.Fatalf("drop %d: event=%v ref=%v", i, gd[i], wd[i])
		}
	}

	// Token levels must match exactly across both models at the horizon.
	for name, b1 := range buckets1 {
		a1, a2 := b1.Avail(horizon), buckets2[name].Avail(horizon)
		if a1.Cmp(a2) != 0 {
			t.Fatalf("bucket %s: event avail=%v ref avail=%v", name, a1, a2)
		}
	}
	// Conservation evidence holds in both models after hot-swaps and cancels.
	for name, b := range buckets1 {
		if a := b.Audit(horizon); !a.Conserved {
			t.Fatalf("event model bucket %s not conserved: %+v", name, a)
		}
	}
	for name, b := range buckets2 {
		if a := b.Audit(horizon); !a.Conserved {
			t.Fatalf("ref model bucket %s not conserved: %+v", name, a)
		}
	}
}

func TestPolicerEventMatchesPerTick(t *testing.T) {
	arrivals := genTraffic(7, 300)
	horizon := int64(2000)

	flows1, _ := scenario()
	p := NewPolicer(flows1)
	for _, a := range arrivals {
		p.Arrive(a.Time, a.Pkt)
	}

	flows2, _ := scenario()
	refPass, refDrop := RunRefPolicer(flows2, arrivals, horizon)

	if len(p.Passed) != len(refPass) || len(p.Dropped) != len(refDrop) {
		t.Fatalf("policer mismatch: event %d/%d ref %d/%d",
			len(p.Passed), len(p.Dropped), len(refPass), len(refDrop))
	}
	for i := range p.Passed {
		if p.Passed[i].Pkt.ID != refPass[i].Pkt.ID {
			t.Fatalf("pass %d: event=%d ref=%d", i, p.Passed[i].Pkt.ID, refPass[i].Pkt.ID)
		}
	}
	for i := range p.Dropped {
		if p.Dropped[i].Pkt.ID != refDrop[i].Pkt.ID {
			t.Fatalf("drop %d: event=%d ref=%d", i, p.Dropped[i].Pkt.ID, refDrop[i].Pkt.ID)
		}
	}
}

func TestCancelAndWeightSwapDoNotTouchTokens(t *testing.T) {
	// Slow bucket: packets queue up, so the cancel at t=1 hits a queued pkt.
	root := NewBucket("root", Rat(1, 1), 8, 0)
	node := &Node{Name: "root", Bucket: root}
	f := NewFlow("f", node, 0, 1, 100, 0)
	s := NewShaper([]*Flow{f})
	for i := 0; i < 5; i++ {
		s.AddArrival(Arrival{Time: 0, Pkt: Packet{ID: i, Flow: "f", Size: 4}})
	}
	s.AddControl(ControlOp{Time: 1, Kind: "cancel", Flow: "f", PktID: 3})
	s.AddControl(ControlOp{Time: 1, Kind: "setweight", Flow: "f", Weight: 9})
	s.Run()
	for _, e := range s.Emitted {
		if e.Pkt.ID == 3 {
			t.Fatalf("cancelled packet emitted at %d", e.Emit)
		}
	}
	if len(s.Emitted) != 4 {
		t.Fatalf("emitted %d, want 4", len(s.Emitted))
	}
	// Cancel and weight swap consume nothing: only the 4 emitted packets
	// were ever charged, and conservation still holds exactly.
	if a := root.Audit(1000); !a.Conserved {
		t.Fatalf("not conserved after cancel: %+v", a)
	}
	if got := root.Audit(1000).Consumed; got.Cmp(Rat(16, 1)) != 0 {
		t.Fatalf("consumed = %v, want 16 (cancel must not charge tokens)", got)
	}
}
