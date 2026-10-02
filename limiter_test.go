package limiter

import (
	"fmt"
	"math/big"
	"math/rand"
	"testing"
)

// smallTree builds root(cap 100, rate 1) with two leaves a(cap 10, rate 1/3)
// and b(cap 10, rate 1/2).
func smallTree() (*Limiter, *Node, *Node, *Node) {
	root := NewNode("root", Rat(100, 1), Rat(1, 1))
	a := NewNode("a", Rat(10, 1), Rat(1, 3))
	b := NewNode("b", Rat(10, 1), Rat(1, 2))
	l := NewLimiter(root)
	l.Attach(root, a)
	l.Attach(root, b)
	return l, root, a, b
}

func mustLedger(t *testing.T, l *Limiter) {
	t.Helper()
	if err := l.CheckLedger(); err != nil {
		t.Fatalf("ledger violated: %v", err)
	}
}

func TestRationalRefillExact(t *testing.T) {
	l, _, a, _ := smallTree()
	// Drain a to zero: 3 packets of size 3 + ... simpler: send one size-10.
	if err := l.Enqueue(a, Packet{ID: 1, Size: 10}); err != nil {
		t.Fatal(err)
	}
	if _, ok := l.SendNext(a); !ok {
		t.Fatal("expected send")
	}
	if a.Tokens.Sign() != 0 {
		t.Fatalf("a should be empty, got %s", a.Tokens)
	}
	// Rate 1/3 per tick: after 1 tick -> 1/3, after 3 ticks -> 1 exactly.
	l.AdvanceTo(1)
	if a.Tokens.Cmp(Rat(1, 3)) != 0 {
		t.Fatalf("after 1 tick want 1/3, got %s", a.Tokens)
	}
	l.AdvanceTo(3)
	if a.Tokens.Cmp(Rat(1, 1)) != 0 {
		t.Fatalf("after 3 ticks want 1, got %s", a.Tokens)
	}
	// A size-1 packet only becomes sendable now.
	if err := l.Enqueue(a, Packet{ID: 2, Size: 1}); err != nil {
		t.Fatal(err)
	}
	if _, ok := l.SendNext(a); !ok {
		t.Fatal("expected send after 3 ticks of 1/3 refill")
	}
	mustLedger(t, l)
}

func TestDuplicateTickNoDoubleRefill(t *testing.T) {
	l, _, a, _ := smallTree()
	if err := l.Enqueue(a, Packet{ID: 1, Size: 10}); err != nil {
		t.Fatal(err)
	}
	l.SendNext(a)
	l.AdvanceTo(5)
	snap := new(big.Rat).Set(a.Tokens)
	for i := 0; i < 10; i++ {
		l.AdvanceTo(5) // repeated tick must be a no-op
	}
	if a.Tokens.Cmp(snap) != 0 {
		t.Fatalf("duplicate tick refilled again: %s -> %s", snap, a.Tokens)
	}
	mustLedger(t, l)
}

func TestParentInsufficientBlocksAtomically(t *testing.T) {
	root := NewNode("root", Rat(10, 1), Rat(0, 1)) // cap 10, no refill
	child := NewNode("child", Rat(10, 1), Rat(0, 1))
	drainer := NewNode("drainer", Rat(10, 1), Rat(0, 1))
	l := NewLimiter(root)
	l.Attach(root, child)
	l.Attach(root, drainer)
	// Drain root down to 5 tokens via a sibling tenant.
	if err := l.Enqueue(drainer, Packet{ID: 0, Size: 5}); err != nil {
		t.Fatal(err)
	}
	if _, ok := l.SendNext(drainer); !ok {
		t.Fatal("drain send failed")
	}
	if err := l.Enqueue(child, Packet{ID: 1, Size: 6}); err != nil {
		t.Fatal(err)
	}
	rootSnap := new(big.Rat).Set(root.Tokens)
	childSnap := new(big.Rat).Set(child.Tokens)
	if _, ok := l.SendNext(child); ok {
		t.Fatal("child has 10 but parent only 5: must not send size 6")
	}
	if root.Tokens.Cmp(rootSnap) != 0 || child.Tokens.Cmp(childSnap) != 0 {
		t.Fatal("failed send must deduct nothing (atomic)")
	}
	if l.QueueLen(child) != 1 {
		t.Fatal("blocked packet must stay queued")
	}
	mustLedger(t, l)
}

func TestOversizeExplicitlyRejected(t *testing.T) {
	l, _, a, _ := smallTree()
	err := l.Enqueue(a, Packet{ID: 1, Size: 11}) // a capacity is 10
	if err == nil || !isOversize(err) {
		t.Fatalf("want ErrOversize, got %v", err)
	}
	if l.QueueLen(a) != 0 {
		t.Fatal("rejected packet must not be queued")
	}
	mustLedger(t, l)
}

func isOversize(err error) bool {
	for err != nil {
		if err == ErrOversize {
			return true
		}
		type unwrapper interface{ Unwrap() error }
		u, ok := err.(unwrapper)
		if !ok {
			return false
		}
		err = u.Unwrap()
	}
	return false
}

func TestCancelConsumesNothing(t *testing.T) {
	l, root, a, _ := smallTree()
	if err := l.Enqueue(a, Packet{ID: 1, Size: 4}); err != nil {
		t.Fatal(err)
	}
	rootSnap := new(big.Rat).Set(root.Tokens)
	aSnap := new(big.Rat).Set(a.Tokens)
	if !l.Cancel(a, 1) {
		t.Fatal("cancel should find packet")
	}
	if root.Tokens.Cmp(rootSnap) != 0 || a.Tokens.Cmp(aSnap) != 0 {
		t.Fatal("cancel must not touch any tokens")
	}
	if l.Cancel(a, 1) {
		t.Fatal("double cancel must fail")
	}
	mustLedger(t, l)
}

func TestRateChangeNoDoubleRefill(t *testing.T) {
	l, _, a, _ := smallTree()
	if err := l.Enqueue(a, Packet{ID: 1, Size: 10}); err != nil {
		t.Fatal(err)
	}
	l.SendNext(a)             // a now empty, rate 1/3
	l.AdvanceTo(3)            // a has exactly 1
	l.SetRate(a, Rat(2, 1))   // settle old rate at tick 3, then switch
	l.AdvanceTo(4)            // +2 under new rate only
	if a.Tokens.Cmp(Rat(3, 1)) != 0 {
		t.Fatalf("want 3 (1 old + 2 new), got %s", a.Tokens)
	}
	mustLedger(t, l)
}

func TestReattachMigratesQueueAndTokens(t *testing.T) {
	root := NewNode("root", Rat(100, 1), Rat(0, 1))
	p1 := NewNode("p1", Rat(50, 1), Rat(0, 1))
	p2 := NewNode("p2", Rat(50, 1), Rat(0, 1))
	tenant := NewNode("tenant", Rat(10, 1), Rat(0, 1))
	l := NewLimiter(root)
	l.Attach(root, p1)
	l.Attach(root, p2)
	l.Attach(p1, tenant)

	if err := l.Enqueue(tenant, Packet{ID: 1, Size: 3}); err != nil {
		t.Fatal(err)
	}
	if err := l.Enqueue(tenant, Packet{ID: 2, Size: 4}); err != nil {
		t.Fatal(err)
	}
	tokSnap := new(big.Rat).Set(tenant.Tokens)

	// Atomic migration: queue and reserved tokens ride along.
	l.Reattach(tenant, p2)
	if l.QueueLen(tenant) != 2 {
		t.Fatalf("queue lost in migration: len=%d", l.QueueLen(tenant))
	}
	if tenant.Tokens.Cmp(tokSnap) != 0 {
		t.Fatalf("reserved tokens changed in migration: %s -> %s", tokSnap, tenant.Tokens)
	}
	if tenant.Parent != p2 || len(p1.Children) != 0 {
		t.Fatal("tree links not updated")
	}

	// Sends now deduct the NEW path (root -> p2 -> tenant), never p1.
	p1Snap := new(big.Rat).Set(p1.Tokens)
	ev, ok := l.SendNext(tenant)
	if !ok {
		t.Fatal("send after reattach failed")
	}
	if p1.Tokens.Cmp(p1Snap) != 0 {
		t.Fatal("old parent p1 must not be deducted after reattach")
	}
	wantPath := []string{"root", "p2", "tenant"}
	if len(ev.Deductions) != len(wantPath) {
		t.Fatalf("evidence path length %d, want %d", len(ev.Deductions), len(wantPath))
	}
	for i, d := range ev.Deductions {
		if d.Node != wantPath[i] {
			t.Fatalf("evidence[%d] = %s, want %s", i, d.Node, wantPath[i])
		}
		if d.Amount.Cmp(Rat(3, 1)) != 0 {
			t.Fatalf("evidence[%d] amount %s, want 3", i, d.Amount)
		}
	}
	mustLedger(t, l)
}

func TestEvidenceCoversEveryAncestor(t *testing.T) {
	l, _, a, _ := smallTree()
	if err := l.Enqueue(a, Packet{ID: 7, Size: 2}); err != nil {
		t.Fatal(err)
	}
	ev, ok := l.SendNext(a)
	if !ok {
		t.Fatal("send failed")
	}
	if ev.PacketID != 7 || ev.Tick != 0 {
		t.Fatalf("bad evidence header: %+v", ev)
	}
	if len(ev.Deductions) != 2 || ev.Deductions[0].Node != "root" || ev.Deductions[1].Node != "a" {
		t.Fatalf("evidence must cover root->a: %+v", ev.Deductions)
	}
	for _, d := range ev.Deductions {
		if d.Amount.Cmp(Rat(2, 1)) != 0 {
			t.Fatalf("each ancestor must be deducted exactly the size: %+v", d)
		}
	}
	mustLedger(t, l)
}

// TestInterleavingsExhaustive enumerates every event sequence up to depth 6
// over an 8-event alphabet mixing send/cancel/refill/reattach, and runs the
// independent ledger audit after every single event.
func TestInterleavingsExhaustive(t *testing.T) {
	const depth = 6
	const width = 8
	seq := make([]int, 0, depth)
	checked := 0
	var walk func()
	walk = func() {
		if len(seq) == depth {
			runSequence(t, seq)
			checked++
			return
		}
		for e := 0; e < width; e++ {
			walk2(&seq, e, walk)
		}
	}
	walk()
	t.Logf("exhaustive sequences verified: %d", checked)
}

func walk2(seq *[]int, e int, next func()) {
	*seq = append(*seq, e)
	next()
	*seq = (*seq)[:len(*seq)-1]
}

// runSequence executes one event sequence on a fresh tree. Events:
// 0 enqueue A, 1 send A, 2 cancel A, 3 enqueue B, 4 send B,
// 5 tick+1, 6 tick+3, 7 reattach A between the two parents.
func runSequence(t *testing.T, seq []int) {
	root := NewNode("root", Rat(30, 1), Rat(1, 1))
	p1 := NewNode("p1", Rat(20, 1), Rat(1, 2))
	p2 := NewNode("p2", Rat(20, 1), Rat(1, 2))
	a := NewNode("a", Rat(8, 1), Rat(1, 3))
	b := NewNode("b", Rat(8, 1), Rat(1, 4))
	l := NewLimiter(root)
	l.Attach(root, p1)
	l.Attach(root, p2)
	l.Attach(p1, a)
	l.Attach(p2, b)

	nextID := 1
	tick := int64(0)
	aOnP1 := true
	for step, e := range seq {
		switch e {
		case 0:
			l.Enqueue(a, Packet{ID: nextID, Size: int64(1 + nextID%5)})
			nextID++
		case 1:
			l.SendNext(a)
		case 2:
			l.Cancel(a, nextID-1)
		case 3:
			l.Enqueue(b, Packet{ID: nextID, Size: int64(1 + nextID%5)})
			nextID++
		case 4:
			l.SendNext(b)
		case 5:
			tick++
			l.AdvanceTo(tick)
			l.AdvanceTo(tick) // duplicate tick on purpose
		case 6:
			tick += 3
			l.AdvanceTo(tick)
		case 7:
			if aOnP1 {
				l.Reattach(a, p2)
			} else {
				l.Reattach(a, p1)
			}
			aOnP1 = !aOnP1
		}
		if err := l.CheckLedger(); err != nil {
			t.Fatalf("seq %v step %d event %d: %v", seq, step, e, err)
		}
	}
}

// TestInterleavingsRandomized hammers longer random interleavings with
// random sizes, rates, cancels, rate changes and reattachments.
func TestInterleavingsRandomized(t *testing.T) {
	for seed := int64(0); seed < 200; seed++ {
		rng := rand.New(rand.NewSource(seed))
		root := NewNode("root", Rat(40, 1), Rat(1, 1))
		p1 := NewNode("p1", Rat(25, 1), Rat(1, 2))
		p2 := NewNode("p2", Rat(25, 1), Rat(1, 2))
		a := NewNode("a", Rat(9, 1), Rat(1, 3))
		b := NewNode("b", Rat(9, 1), Rat(1, 5))
		l := NewLimiter(root)
		l.Attach(root, p1)
		l.Attach(root, p2)
		l.Attach(p1, a)
		l.Attach(p1, b)
		leaves := []*Node{a, b}
		parents := []*Node{p1, p2}
		tick := int64(0)
		nextID := 1
		for step := 0; step < 120; step++ {
			leaf := leaves[rng.Intn(2)]
			switch rng.Intn(7) {
			case 0, 1:
				l.Enqueue(leaf, Packet{ID: nextID, Size: int64(1 + rng.Intn(12))})
				nextID++
			case 2:
				l.SendNext(leaf)
			case 3:
				if nextID > 1 {
					l.Cancel(leaf, rng.Intn(nextID))
				}
			case 4:
				tick += int64(rng.Intn(4))
				l.AdvanceTo(tick)
				l.AdvanceTo(tick) // duplicate tick on purpose
			case 5:
				l.SetRate(leaf, Rat(int64(rng.Intn(4)), int64(1+rng.Intn(3))))
			case 6:
				l.Reattach(leaf, parents[rng.Intn(2)])
			}
			if err := l.CheckLedger(); err != nil {
				t.Fatalf("seed %d step %d: %v", seed, step, err)
			}
		}
		// Cross-check the evidence log against the ledger: total deducted
		// per bucket must equal the sum of evidence amounts.
		sum := map[string]*big.Rat{}
		for _, ev := range l.Sent {
			for _, d := range ev.Deductions {
				if sum[d.Node] == nil {
					sum[d.Node] = new(big.Rat)
				}
				sum[d.Node].Add(sum[d.Node], d.Amount)
			}
		}
		for name, node := range map[string]*Node{"root": root, "p1": p1, "p2": p2, "a": a, "b": b} {
			got := sum[name]
			if got == nil {
				got = new(big.Rat)
			}
			if got.Cmp(l.deducted[node]) != 0 {
				t.Fatalf("seed %d: evidence total for %s = %s, ledger deducted = %s",
					seed, name, got, l.deducted[node])
			}
		}
	}
}

func ExampleLimiter() {
	l, _, a, _ := smallTree()
	_ = l.Enqueue(a, Packet{ID: 1, Size: 4})
	if ev, ok := l.SendNext(a); ok {
		for _, d := range ev.Deductions {
			fmt.Printf("pkt %d @tick %d: %s -%s -> %s\n",
				ev.PacketID, ev.Tick, d.Node, Fmt(d.Amount), Fmt(d.Remaining))
		}
	}
	// Output:
	// pkt 1 @tick 0: root -4 -> 96
	// pkt 1 @tick 0: a -4 -> 6
}
