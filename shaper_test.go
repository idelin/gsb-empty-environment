package tbsim

import "testing"

func TestHierarchyWaitIsMaxOverAllAncestors(t *testing.T) {
	// Leaf bucket is full and fast; root is empty and slow. The wait must be
	// dictated by the root: checking only the leaf would miss it.
	root := NewBucket("root", Rat(1, 1), 20, 0)
	leaf := NewBucket("leaf", Rat(100, 1), 20, 0)
	rootNode := &Node{Name: "root", Bucket: root}
	leafNode := &Node{Name: "leaf", Bucket: leaf, Parent: rootNode}
	f := NewFlow("f", leafNode, 0, 1, 100, 0)
	root.Consume(0, 20) // drain root at t=0

	s := NewShaper([]*Flow{f})
	s.AddArrival(Arrival{Time: 0, Pkt: Packet{ID: 1, Flow: "f", Size: 8}})
	s.Run()
	if len(s.Emitted) != 1 {
		t.Fatalf("emitted %d, want 1", len(s.Emitted))
	}
	e := s.Emitted[0]
	if e.Emit != 8 { // root needs 8 ticks for 8 tokens; leaf is instant
		t.Fatalf("emit at %d, want 8 (root constraint must not be missed)", e.Emit)
	}
	if e.Waits["root"] != 8 || e.Waits["leaf"] != 0 {
		t.Fatalf("decomposition wrong: %v", e.Waits)
	}
	if e.TokenWait() != 8 {
		t.Fatalf("token wait = %d, want 8", e.TokenWait())
	}
	// Both buckets were actually charged.
	if got := root.Avail(8); got.Sign() != 0 {
		t.Fatalf("root avail = %v, want 0", got)
	}
	if got := leaf.Avail(8); got.Cmp(Rat(12, 1)) != 0 {
		t.Fatalf("leaf avail = %v, want 12", got)
	}
}

func TestLeafConstraintDominatesWhenRootIsFast(t *testing.T) {
	root := NewBucket("root", Rat(100, 1), 50, 0)
	leaf := NewBucket("leaf", Rat(1, 2), 10, 0) // 1 token per 2 ticks
	rootNode := &Node{Name: "root", Bucket: root}
	leafNode := &Node{Name: "leaf", Bucket: leaf, Parent: rootNode}
	f := NewFlow("f", leafNode, 0, 1, 100, 0)
	leaf.Consume(0, 10)

	s := NewShaper([]*Flow{f})
	s.AddArrival(Arrival{Time: 0, Pkt: Packet{ID: 1, Flow: "f", Size: 5}})
	s.Run()
	e := s.Emitted[0]
	if e.Emit != 10 { // ceil(5 / (1/2)) = 10
		t.Fatalf("emit at %d, want 10 (leaf constraint)", e.Emit)
	}
	if e.Waits["leaf"] != 10 || e.Waits["root"] != 0 {
		t.Fatalf("decomposition wrong: %v", e.Waits)
	}
}

func TestShaperDelaysPolicerDropsSameParams(t *testing.T) {
	build := func() []*Flow {
		root := NewBucket("root", Rat(1, 1), 10, 0)
		node := &Node{Name: "root", Bucket: root}
		return []*Flow{NewFlow("f", node, 0, 1, 100, 0)}
	}
	arrivals := []Arrival{
		{Time: 0, Pkt: Packet{ID: 1, Flow: "f", Size: 6}},
		{Time: 0, Pkt: Packet{ID: 2, Flow: "f", Size: 6}},
		{Time: 1, Pkt: Packet{ID: 3, Flow: "f", Size: 4}},
	}
	// Shaper: nothing dropped, second packet waits for tokens.
	s := NewShaper(build())
	for _, a := range arrivals {
		s.AddArrival(a)
	}
	s.Run()
	if len(s.Emitted) != 3 || len(s.Dropped) != 0 {
		t.Fatalf("shaper: emitted=%d dropped=%d, want 3/0", len(s.Emitted), len(s.Dropped))
	}
	if s.Emitted[1].Delay() == 0 {
		t.Fatalf("shaper must delay when tokens are short")
	}
	// Policer: same bucket params, drops instead of delaying.
	p := NewPolicer(build())
	for _, a := range arrivals {
		p.Arrive(a.Time, a.Pkt)
	}
	if len(p.Passed) != 2 || len(p.Dropped) != 1 {
		t.Fatalf("policer: passed=%d dropped=%d, want 2/1", len(p.Passed), len(p.Dropped))
	}
	for _, pr := range p.Passed {
		_ = pr // policer records carry only the arrival tick: no delay exists
	}
}

func TestShaperNeverExceedsBuffer(t *testing.T) {
	root := NewBucket("root", Rat(1, 1), 10, 0)
	node := &Node{Name: "root", Bucket: root}
	f := NewFlow("f", node, 0, 1, 10, 0) // 10 bytes of cache
	s := NewShaper([]*Flow{f})
	s.AddArrival(Arrival{Time: 0, Pkt: Packet{ID: 1, Flow: "f", Size: 10}}) // fills cache
	s.AddArrival(Arrival{Time: 0, Pkt: Packet{ID: 2, Flow: "f", Size: 1}})  // exceeds cache
	s.Run()
	if len(s.Dropped) != 1 || s.Dropped[0].Reason != "buffer" {
		t.Fatalf("expected buffer drop, got %+v", s.Dropped)
	}
	if len(s.Emitted) != 1 {
		t.Fatalf("emitted %d, want 1", len(s.Emitted))
	}
}

func TestOversizeNeverPasses(t *testing.T) {
	root := NewBucket("root", Rat(1, 1), 5, 0)
	node := &Node{Name: "root", Bucket: root}
	f := NewFlow("f", node, 0, 1, 100, 0)
	s := NewShaper([]*Flow{f})
	s.AddArrival(Arrival{Time: 0, Pkt: Packet{ID: 1, Flow: "f", Size: 6}}) // > Cap
	s.Run()
	if len(s.Emitted) != 0 || len(s.Dropped) != 1 || s.Dropped[0].Reason != "oversize" {
		t.Fatalf("oversize handling wrong: %+v %+v", s.Emitted, s.Dropped)
	}
}

func TestPriorityBeatsWeight(t *testing.T) {
	root := NewBucket("root", Rat(100, 1), 100, 0)
	node := &Node{Name: "root", Bucket: root}
	hi := NewFlow("hi", node, 1, 1, 100, 0)
	lo := NewFlow("lo", node, 0, 9, 100, 0) // much larger weight, lower priority
	s := NewShaper([]*Flow{hi, lo})
	s.AddArrival(Arrival{Time: 0, Pkt: Packet{ID: 1, Flow: "lo", Size: 1}})
	s.AddArrival(Arrival{Time: 0, Pkt: Packet{ID: 2, Flow: "hi", Size: 1}})
	s.Run()
	if s.Emitted[0].Pkt.Flow != "hi" {
		t.Fatalf("priority violated: first emit is %s", s.Emitted[0].Pkt.Flow)
	}
}

func TestDecompositionConsistentWithEmit(t *testing.T) {
	// No contention: emit must equal decision + max(per-bucket waits).
	root := NewBucket("root", Rat(3, 2), 12, 0)
	leaf := NewBucket("leaf", Rat(1, 1), 6, 0)
	rootNode := &Node{Name: "root", Bucket: root}
	leafNode := &Node{Name: "leaf", Bucket: leaf, Parent: rootNode}
	f := NewFlow("f", leafNode, 0, 1, 100, 0)
	s := NewShaper([]*Flow{f})
	for i := 0; i < 6; i++ {
		s.AddArrival(Arrival{Time: int64(i), Pkt: Packet{ID: i, Flow: "f", Size: 4}})
	}
	s.Run()
	for _, e := range s.Emitted {
		if e.Emit != e.Decision+e.TokenWait() && e.Emit != e.ReadyAt {
			t.Fatalf("pkt %d: emit=%d decision=%d waits=%v readyAt=%d",
				e.Pkt.ID, e.Emit, e.Decision, e.Waits, e.ReadyAt)
		}
		if e.Emit < e.Arrival {
			t.Fatalf("pkt %d emitted before arrival", e.Pkt.ID)
		}
	}
}
