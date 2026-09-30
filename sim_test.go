package sim

import (
	"fmt"
	"reflect"
	"testing"
)

func newRouters(n *Network, count, lifetime, spfDelay int) []*Router {
	routers := make([]*Router, count)
	for i := range routers {
		r := NewRouter(i)
		r.LSALifetime = lifetime
		r.SPFDelay = spfDelay
		n.AddRouter(r)
		routers[i] = r
	}
	return routers
}

func assertConverged(t *testing.T, n *Network, alive []int) {
	t.Helper()
	if err := convergenceError(n, alive); err != nil {
		t.Fatal(err)
	}
}

func convergenceError(n *Network, alive []int) error {
	var ref map[int]LSA
	for i, id := range alive {
		snap := n.Routers[id].LSDB()
		if i == 0 {
			ref = snap
			continue
		}
		if !reflect.DeepEqual(ref, snap) {
			return fmt.Errorf("router %d LSDB diverges from router %d", id, alive[0])
		}
	}
	for _, id := range alive {
		for _, dst := range alive {
			if dst == id {
				continue
			}
			if err := pathError(n, id, dst); err != nil {
				return err
			}
		}
	}
	return nil
}

func pathError(n *Network, src, dst int) error {
	cur := n.Routers[src]
	total := 0
	seen := map[int]bool{src: true}
	for cur.ID != dst {
		rt, ok := cur.Routes()[dst]
		if !ok {
			return fmt.Errorf("router %d has no route to %d", cur.ID, dst)
		}
		next := n.Routers[rt.NextHop]
		if next == nil {
			return fmt.Errorf("router %d route to %d via unknown next hop %d", cur.ID, dst, rt.NextHop)
		}
		link := n.LinkBetween(cur.ID, next.ID)
		if link == nil || !link.Up {
			return fmt.Errorf("router %d next hop %d for %d not over an up link", cur.ID, next.ID, dst)
		}
		total += link.Cost
		if seen[next.ID] {
			return fmt.Errorf("routing loop towards %d at router %d", dst, next.ID)
		}
		seen[next.ID] = true
		cur = next
	}
	if got := n.Routers[src].Routes()[dst].Cost; got != total {
		return fmt.Errorf("router %d cost to %d = %d, walked %d", src, dst, got, total)
	}
	return nil
}

func TestOldAndDuplicateLSADiscarded(t *testing.T) {
	s := NewSimulator()
	n := NewNetwork(s)
	r := NewRouter(0)
	r.LSALifetime = 0
	n.AddRouter(r)

	r.receiveLSA(LSA{Origin: 5, Seq: 10, Links: map[int]int{6: 1}}, -1)
	r.receiveLSA(LSA{Origin: 5, Seq: 9, Links: map[int]int{7: 1}}, -1)  // older
	r.receiveLSA(LSA{Origin: 5, Seq: 10, Links: map[int]int{8: 1}}, -1) // duplicate

	if got := r.lsdb[5].lsa.Links; !reflect.DeepEqual(got, map[int]int{6: 1}) {
		t.Fatalf("LSDB entry overwritten by stale LSA: %v", got)
	}
	if r.DroppedOldLSA != 2 {
		t.Fatalf("dropped %d stale LSAs, want 2", r.DroppedOldLSA)
	}
}

func TestStaleLSADoesNotResurrectLink(t *testing.T) {
	s := NewSimulator()
	n := NewNetwork(s)
	r := newRouters(n, 4, 0, 5)
	n.AddLink(r[1], r[0], 1, 5, 1)  // direct path 1->0: 6 ticks
	n.AddLink(r[1], r[3], 1, 40, 1) // relay path 1->3->0: 82 ticks
	n.AddLink(r[3], r[0], 1, 40, 1)
	l12 := n.AddLink(r[1], r[2], 1, 1, 1)

	for _, rt := range r {
		rt.Start()
	}
	// Link 1-2 fails at tick 20. Router 0 learns the new LSA (seq 2,
	// without neighbour 2) via the fast path at tick 26, while the old
	// seq-1 copy (still listing link 1-2) is only delivered via the
	// slow relay at tick 82 and must be dropped.
	s.After(20, func() { n.SetLinkUp(l12, false) })
	s.Run(1000)

	e, ok := r[0].lsdb[1]
	if !ok {
		t.Fatal("router 0 lost router 1's LSA")
	}
	if _, has := e.lsa.Links[2]; has {
		t.Fatal("stale LSA resurrected deleted link 1-2")
	}
	if _, ok := r[0].Routes()[2]; ok {
		t.Fatal("router 0 still has a route to router 2")
	}
	if r[0].DroppedOldLSA == 0 {
		t.Fatal("expected router 0 to drop at least one stale LSA")
	}
}

func TestSPFDebounce(t *testing.T) {
	s := NewSimulator()
	n := NewNetwork(s)
	r := NewRouter(0)
	r.LSALifetime = 0
	r.SPFDelay = 10
	n.AddRouter(r)

	// Three LSDB changes inside one debounce window -> one SPF run.
	for i, tick := range []int{0, 1, 2} {
		i, tick := i, tick
		s.After(tick, func() {
			r.receiveLSA(LSA{Origin: 10 + i, Seq: 1, Links: map[int]int{}}, -1)
		})
	}
	// A change after the window expired -> a second run.
	s.After(20, func() {
		r.receiveLSA(LSA{Origin: 13, Seq: 1, Links: map[int]int{}}, -1)
	})
	s.Run(100)

	if r.SPFRuns != 2 {
		t.Fatalf("SPF ran %d times, want 2 (debounced)", r.SPFRuns)
	}
}

func TestEqualCostNextHopStable(t *testing.T) {
	s := NewSimulator()
	n := NewNetwork(s)
	r := newRouters(n, 6, 0, 5)
	n.AddLink(r[0], r[1], 1, 1, 1)
	n.AddLink(r[0], r[2], 1, 1, 1)
	n.AddLink(r[1], r[3], 1, 1, 1)
	n.AddLink(r[2], r[3], 1, 1, 1)
	n.AddLink(r[3], r[4], 1, 1, 1)
	l45 := n.AddLink(r[4], r[5], 1, 1, 1)

	for _, rt := range r {
		rt.Start()
	}
	// Router 0 has two equal-cost paths to 3 (via 1 and via 2); the
	// next hop must always be the lower-ID neighbour, across unrelated
	// topology changes elsewhere.
	check := func() {
		rt, ok := r[0].Routes()[3]
		if !ok {
			t.Fatalf("tick %d: router 0 has no route to router 3", s.Now())
		}
		if rt.NextHop != 1 || rt.Cost != 2 {
			t.Fatalf("tick %d: router 0 -> 3 = %+v, want next hop 1 cost 2", s.Now(), rt)
		}
		back := r[3].Routes()[0]
		if back.NextHop != 1 || back.Cost != 2 {
			t.Fatalf("tick %d: router 3 -> 0 = %+v, want next hop 1 cost 2", s.Now(), back)
		}
	}
	s.After(50, check)
	s.After(100, func() { n.SetLinkUp(l45, false) })
	s.After(150, check)
	s.After(200, func() { n.SetLinkUp(l45, true) })
	s.After(250, check)
	s.Run(1000)
}

func TestConvergenceAfterFailureAndRecovery(t *testing.T) {
	s := NewSimulator()
	n := NewNetwork(s)
	r := newRouters(n, 6, 0, 5)
	ring := [][2]int{{0, 1}, {1, 2}, {2, 3}, {3, 4}, {4, 5}, {5, 0}}
	links := map[[2]int]*Link{}
	for _, e := range ring {
		links[e] = n.AddLink(r[e[0]], r[e[1]], 1, 2, 1)
	}
	n.AddLink(r[0], r[3], 1, 2, 1) // chord

	all := []int{0, 1, 2, 3, 4, 5}
	for _, rt := range r {
		rt.Start()
	}
	l12 := links[[2]int{1, 2}]
	s.After(40, func() { assertConverged(t, n, all) })
	s.After(50, func() { n.SetLinkUp(l12, false) })
	s.After(100, func() {
		assertConverged(t, n, all)
		if got := r[1].Routes()[2].Cost; got != 3 {
			t.Fatalf("after failure: router 1 -> 2 cost = %d, want 3 (via 0-3 chord)", got)
		}
	})
	s.After(150, func() { n.SetLinkUp(l12, true) })
	s.After(200, func() {
		assertConverged(t, n, all)
		if got := r[1].Routes()[2].Cost; got != 1 {
			t.Fatalf("after recovery: router 1 -> 2 cost = %d, want 1", got)
		}
	})
	s.Run(1000)
}

func TestExpiryRemovesDeadRouter(t *testing.T) {
	s := NewSimulator()
	n := NewNetwork(s)
	r := newRouters(n, 3, 60, 5)
	n.AddLink(r[0], r[1], 1, 2, 1)
	n.AddLink(r[1], r[2], 1, 2, 1)

	for _, rt := range r {
		rt.Start()
	}
	s.After(50, func() {
		if _, ok := r[0].Routes()[2]; !ok {
			t.Fatal("router 0 should reach router 2 before the failure")
		}
	})
	s.After(100, func() { n.SetRouterDown(r[2]) })

	// Router 2 keeps no refresh schedule once down, so its LSA ages
	// out everywhere; routers 0 and 1 must eventually converge with
	// no trace of router 2. Because live routers keep refreshing
	// their LSAs, any single observation tick may catch a packet in
	// flight, so poll until the system settles.
	settled := func() error {
		for _, id := range []int{0, 1} {
			if _, ok := n.Routers[id].lsdb[2]; ok {
				return fmt.Errorf("router %d still has an LSA from dead router 2", id)
			}
			if _, ok := n.Routers[id].Routes()[2]; ok {
				return fmt.Errorf("router %d still routes to dead router 2", id)
			}
		}
		return convergenceError(n, []int{0, 1})
	}
	converged := false
	tries := 0
	var check func()
	check = func() {
		if err := settled(); err != nil {
			tries++
			if tries > 50 {
				t.Fatalf("not converged after router 2 died: %v", err)
			}
			s.After(10, check)
			return
		}
		converged = true
	}
	s.After(150, check)
	s.Run(2000)
	if !converged {
		t.Fatal("routers 0 and 1 never converged after router 2 died")
	}
}
