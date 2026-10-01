package htb

import (
	"fmt"
	"math/rand"
	"testing"
)

// world bundles a tree with its independent ledger plus the bookkeeping
// needed to generate interleaved events.
type world struct {
	tr       *Tree
	lg       *Ledger
	pktSeq   int
	taParent string
	taFast   bool
}

func newWorld(debt DebtPolicy) *world {
	tr := NewTree(debt)
	lg := NewLedger(debt)
	add := func(id, parent string, capN, capD, rN, rD int64, full bool) {
		if err := tr.AddNode(id, parent, Rat(capN, capD), Rat(rN, rD), full); err != nil {
			panic(err)
		}
		lg.AddNode(id, parent, Rat(capN, capD), Rat(rN, rD), full)
	}
	add("R", "", 24, 1, 1, 1, true)
	add("A", "R", 12, 1, 1, 2, true)
	add("B", "R", 12, 1, 1, 2, true)
	add("ta", "A", 8, 1, 1, 4, true)
	add("tb", "B", 8, 1, 1, 4, true)
	return &world{tr: tr, lg: lg, taParent: "A"}
}

func cloneTree(tr *Tree) *Tree {
	nt := &Tree{now: tr.now, debt: tr.debt, nodes: make(map[string]*Node, len(tr.nodes))}
	for id, n := range tr.nodes {
		nn := &Node{
			ID: n.ID,
			Bucket: &Bucket{
				Capacity: cloneRat(n.Bucket.Capacity),
				Rate:     cloneRat(n.Bucket.Rate),
				Tokens:   cloneRat(n.Bucket.Tokens),
				lastTick: n.Bucket.lastTick,
			},
		}
		for _, p := range n.queue {
			nn.queue = append(nn.queue, &Packet{ID: p.ID, Size: cloneRat(p.Size)})
		}
		nt.nodes[id] = nn
		if n.parent == nil {
			nt.root = nn
		}
	}
	for id, n := range tr.nodes {
		nn := nt.nodes[id]
		if n.parent != nil {
			nn.parent = nt.nodes[n.parent.ID]
		}
		for _, c := range n.children {
			nn.children = append(nn.children, nt.nodes[c.ID])
		}
	}
	return nt
}

func cloneLedger(l *Ledger) *Ledger {
	nl := NewLedger(l.debt)
	nl.now = l.now
	nl.order = append(nl.order, l.order...)
	for id := range l.bal {
		nl.parent[id] = l.parent[id]
		nl.bal[id] = cloneRat(l.bal[id])
		nl.rate[id] = cloneRat(l.rate[id])
		nl.cap_[id] = cloneRat(l.cap_[id])
		nl.settled[id] = l.settled[id]
		nl.initial[id] = cloneRat(l.initial[id])
		nl.refilled[id] = cloneRat(l.refilled[id])
		nl.deducted[id] = cloneRat(l.deducted[id])
	}
	return nl
}

func (w *world) clone() *world {
	return &world{
		tr:       cloneTree(w.tr),
		lg:       cloneLedger(w.lg),
		pktSeq:   w.pktSeq,
		taParent: w.taParent,
		taFast:   w.taFast,
	}
}

// step is one discrete event. run applies it to the world.
type step struct {
	name string
	run  func(w *world) error
}

// steps lists every event applicable in the current state. Events that
// would be no-ops (send/cancel on an empty queue) are pruned to keep the
// enumeration focused on meaningful interleavings.
func (w *world) steps() []step {
	var out []step
	adv := func(delta int64) step {
		return step{fmt.Sprintf("advance(+%d)", delta), func(w *world) error {
			w.tr.Advance(w.tr.Now() + delta)
			w.lg.Advance(w.tr.Now())
			return nil
		}}
	}
	out = append(out, adv(1), adv(3))
	out = append(out, step{"advance(dup)", func(w *world) error {
		w.tr.Advance(w.tr.Now()) // duplicate tick: must not refill again
		w.lg.Advance(w.tr.Now())
		return nil
	}})
	enq := func(tenant string, sizeN, sizeD int64) step {
		return step{fmt.Sprintf("enqueue(%s,%d/%d)", tenant, sizeN, sizeD), func(w *world) error {
			w.pktSeq++
			pkt := Packet{ID: fmt.Sprintf("p%d", w.pktSeq), Size: Rat(sizeN, sizeD)}
			return w.tr.Enqueue(tenant, pkt)
		}}
	}
	out = append(out, enq("ta", 2, 1)) // normal packet
	out = append(out, enq("ta", 9, 1)) // oversize vs ta's capacity 8
	out = append(out, enq("ta", 5, 2)) // fractional cost
	out = append(out, enq("tb", 3, 1))
	send := func(tenant string) step {
		return step{"send(" + tenant + ")", func(w *world) error {
			res := w.tr.SendNext(tenant)
			return w.lg.Send(tenant, res)
		}}
	}
	if w.tr.QueueLen("ta") > 0 {
		out = append(out, send("ta"))
	}
	if w.tr.QueueLen("tb") > 0 {
		out = append(out, send("tb"))
	}
	if id, ok := w.tr.HeadID("ta"); ok {
		out = append(out, step{"cancel(ta)", func(w *world) error {
			if !w.tr.Cancel("ta", id) {
				return fmt.Errorf("cancel(%s) failed", id)
			}
			w.lg.Cancel()
			return nil
		}})
	}
	other := "A"
	if w.taParent == "A" {
		other = "B"
	}
	out = append(out, step{"reattach(ta->" + other + ")", func(w *world) error {
		if err := w.tr.Reattach("ta", other); err != nil {
			return err
		}
		w.lg.Reattach("ta", other)
		w.taParent = other
		return nil
	}})
	out = append(out, step{"rate(ta)", func(w *world) error {
		r := Rat(1, 4)
		if !w.taFast {
			r = Rat(2, 1)
		}
		if err := w.tr.SetRate("ta", r); err != nil {
			return err
		}
		w.lg.SetRate("ta", r)
		w.taFast = !w.taFast
		return nil
	}})
	return out
}

// enumerateInterleavings explores ALL event sequences up to the given
// depth, verifying the tree against the independent ledger after every
// single event of every sequence.
func enumerateInterleavings(t *testing.T, debt DebtPolicy, depth int) int {
	t.Helper()
	root := newWorld(debt)
	sequences := 0
	var dfs func(w *world, d int, trace []string) error
	dfs = func(w *world, d int, trace []string) error {
		if err := w.lg.Verify(w.tr); err != nil {
			return fmt.Errorf("after [%v]: %v", trace, err)
		}
		if d == 0 {
			sequences++
			return nil
		}
		for _, s := range w.steps() {
			nw := w.clone()
			if err := s.run(nw); err != nil {
				return fmt.Errorf("event %s after [%v]: %v", s.name, trace, err)
			}
			if err := dfs(nw, d-1, append(trace, s.name)); err != nil {
				return err
			}
		}
		return nil
	}
	if err := dfs(root, depth, nil); err != nil {
		t.Fatal(err)
	}
	return sequences
}

func TestInterleavingsRejectOversize(t *testing.T) {
	n := enumerateInterleavings(t, RejectOversize, 4)
	t.Logf("verified %d complete interleavings (depth 4, RejectOversize)", n)
	if n < 1000 {
		t.Fatalf("suspiciously few interleavings explored: %d", n)
	}
}

func TestInterleavingsAllowDebt(t *testing.T) {
	n := enumerateInterleavings(t, AllowDebt, 4)
	t.Logf("verified %d complete interleavings (depth 4, AllowDebt)", n)
	if n < 1000 {
		t.Fatalf("suspiciously few interleavings explored: %d", n)
	}
}

// TestRandomInterleavings drives long random event sequences (fixed
// seeds, deterministic) and verifies the ledger after every event.
func TestRandomInterleavings(t *testing.T) {
	for _, debt := range []DebtPolicy{RejectOversize, AllowDebt} {
		for seed := int64(0); seed < 40; seed++ {
			w := newWorld(debt)
			rng := rand.New(rand.NewSource(seed))
			for i := 0; i < 300; i++ {
				steps := w.steps()
				s := steps[rng.Intn(len(steps))]
				if err := s.run(w); err != nil {
					t.Fatalf("debt=%v seed=%d step=%d event=%s: %v", debt, seed, i, s.name, err)
				}
				if err := w.lg.Verify(w.tr); err != nil {
					t.Fatalf("debt=%v seed=%d step=%d event=%s: %v", debt, seed, i, s.name, err)
				}
			}
		}
	}
}
