package register

import (
	"fmt"
	"testing"
)

// lockstep forces constant network delays so event order is fully
// deterministic.
func lockstep(s *Sim) { s.MinDelay, s.MaxDelay = 1, 1 }

func TestQuorumAndFaultTolerance(t *testing.T) {
	cases := []struct{ n, quorum, f int }{
		{3, 2, 1},
		{5, 3, 2},
		{7, 4, 3},
	}
	for _, c := range cases {
		s := NewSim(c.n, 1)
		if s.Quorum() != c.quorum || s.FaultTolerance() != c.f {
			t.Errorf("n=%d: quorum=%d f=%d, want quorum=%d f=%d",
				c.n, s.Quorum(), s.FaultTolerance(), c.quorum, c.f)
		}
	}
}

func TestWriteThenRead(t *testing.T) {
	s := NewSim(5, 1)
	c := s.NewClient()
	if w := c.Write("A"); !w.OK {
		t.Fatalf("write not completed")
	}
	r := c.Read()
	if !r.OK || !r.Found || r.Result != "A" {
		t.Fatalf("read = %+v, want completed read of %q", r, "A")
	}
}

// A minority of stale replicas must not make a read regress to an older
// value, and the read must repair the stale replicas.
func TestStaleMinorityCannotRegressRead(t *testing.T) {
	s := NewSim(5, 1)
	lockstep(s)
	c := s.NewClient()

	if w := c.Write("old"); !w.OK {
		t.Fatalf("write old: not completed")
	}
	// Isolate two replicas so they miss the next write: they become the
	// stale minority.
	s.Nodes()[3].Crash()
	s.Nodes()[4].Crash()
	if w := c.Write("new"); !w.OK {
		t.Fatalf("write new: not completed with f=2 failures")
	}
	// Bring the stale minority back, then crash two of the three
	// up-to-date replicas. The only reachable majority is
	// {1 fresh, 2 stale}.
	s.Nodes()[3].Restart()
	s.Nodes()[4].Restart()
	s.Nodes()[1].Crash()
	s.Nodes()[2].Crash()

	r := c.Read()
	if !r.OK {
		t.Fatalf("read not completed with f=2 failures")
	}
	if r.Result != "new" {
		t.Fatalf("read regressed: got %q, want %q", r.Result, "new")
	}
	// Read repair: the stale minority now holds the newest value.
	for _, i := range []int{3, 4} {
		if _, v, _ := s.Nodes()[i].Stored(); v != "new" {
			t.Fatalf("node %d not repaired: holds %q", i, v)
		}
	}
	// After repair, even a majority drawn mostly from formerly stale
	// replicas returns the newest value.
	s.Nodes()[0].Crash()
	s.Nodes()[1].Restart()
	s.Nodes()[2].Restart()
	if r := c.Read(); !r.OK || r.Result != "new" {
		t.Fatalf("second read = %+v, want completed read of %q", r, "new")
	}
}

// Two concurrent writes with the same version number are decided by the
// unique writer IDs in their tags.
func TestConcurrentWritesResolvedByUniqueTags(t *testing.T) {
	s := NewSim(5, 1)
	lockstep(s)
	c1 := s.NewClient()
	c2 := s.NewClient()

	// Start both writes before running the clock: with lockstep delays
	// both phase-1 rounds finish before either phase-2 starts, so both
	// pick the same version number and the writer ID decides.
	w1 := c1.WriteAsync("from-c1")
	w2 := c2.WriteAsync("from-c2")
	s.Run()

	if !w1.OK || !w2.OK {
		t.Fatalf("concurrent writes should both complete: w1.OK=%v w2.OK=%v", w1.OK, w2.OK)
	}
	if w1.Tag.Ver != w2.Tag.Ver {
		t.Fatalf("expected same version number, got %d and %d", w1.Tag.Ver, w2.Tag.Ver)
	}
	// c2 has the higher client ID, so its tag wins.
	want := "from-c2"
	r := c1.Read()
	if !r.OK || r.Result != want {
		t.Fatalf("read = %+v, want winner %q", r, want)
	}
	// Every replica converged to the winning tag.
	for _, n := range s.Nodes() {
		if tag, v, _ := n.Stored(); v != want || tag != w2.Tag {
			t.Fatalf("node %d holds (%v,%q), want (%v,%q)", n.ID(), tag, v, w2.Tag, want)
		}
	}
}

// Without a majority the operation must report "not completed" instead
// of a false success.
func TestNoMajorityReturnsNotCompleted(t *testing.T) {
	s := NewSim(5, 1)
	c := s.NewClient()
	if w := c.Write("x"); !w.OK {
		t.Fatalf("initial write not completed")
	}
	// Crash f+1 = 3 of 5 replicas: no majority remains.
	s.Nodes()[0].Crash()
	s.Nodes()[1].Crash()
	s.Nodes()[2].Crash()

	if w := c.Write("y"); !w.Done || w.OK {
		t.Fatalf("write without majority: Done=%v OK=%v, want Done=true OK=false", w.Done, w.OK)
	}
	if r := c.Read(); !r.Done || r.OK {
		t.Fatalf("read without majority: Done=%v OK=%v, want Done=true OK=false", r.Done, r.OK)
	}
	// The failed write must not have silently succeeded: after recovery
	// the register still holds the old value.
	for _, n := range s.Nodes() {
		n.Restart()
	}
	if r := c.Read(); !r.OK || r.Result != "x" {
		t.Fatalf("read after recovery = %+v, want completed read of %q", r, "x")
	}
}

// Replicas keep their state on a simulated disk, so a full cluster
// outage followed by restarts loses nothing.
func TestRestartPreservesState(t *testing.T) {
	s := NewSim(5, 1)
	c := s.NewClient()
	if w := c.Write("durable"); !w.OK {
		t.Fatalf("write not completed")
	}
	for _, n := range s.Nodes() {
		n.Crash()
	}
	for _, n := range s.Nodes() {
		n.Restart()
	}
	if r := c.Read(); !r.OK || r.Result != "durable" {
		t.Fatalf("read after full restart = %+v, want completed read of %q", r, "durable")
	}
}

// Liveness: with at most f crashed replicas every operation completes.
func TestLivenessWithMaxFailures(t *testing.T) {
	s := NewSim(5, 7)
	c := s.NewClient()
	s.Nodes()[0].Crash()
	s.Nodes()[1].Crash() // f = 2 failures, quorum of 3 still reachable
	for i := 0; i < 20; i++ {
		v := fmt.Sprintf("v%d", i)
		if w := c.Write(v); !w.OK {
			t.Fatalf("round %d: write not completed with f failures", i)
		}
		if r := c.Read(); !r.OK || r.Result != v {
			t.Fatalf("round %d: read = %+v, want %q", i, r, v)
		}
	}
}

// Safety under randomized message reordering, crashes and restarts:
// whenever at most f replicas are down, operations complete, and a read
// that follows a completed write (with no concurrent writer) returns
// exactly that write's value. Concurrent writes always resolve to one of
// the written values and never to a regression.
func TestRandomizedSafetyUnderFaultsAndReordering(t *testing.T) {
	for seed := int64(0); seed < 50; seed++ {
		s := NewSim(5, seed)
		s.MinDelay, s.MaxDelay = 1, 8
		c := s.NewClient()
		c2 := s.NewClient()
		last := ""
		for round := 0; round < 15; round++ {
			// Randomly crash/restart, never exceeding f = 2 down.
			down := int(s.rnd.Int63n(3))
			for i, n := range s.Nodes() {
				if i < down {
					n.Crash()
				} else {
					n.Restart()
				}
			}
			if s.rnd.Int63n(4) == 0 {
				// Concurrent pair of writes.
				v1 := fmt.Sprintf("s%d-r%d-a", seed, round)
				v2 := fmt.Sprintf("s%d-r%d-b", seed, round)
				w1 := c.WriteAsync(v1)
				w2 := c2.WriteAsync(v2)
				s.Run()
				if !w1.OK || !w2.OK {
					t.Fatalf("seed %d round %d: concurrent writes not completed", seed, round)
				}
				r := c.Read()
				if !r.OK || (r.Result != v1 && r.Result != v2) {
					t.Fatalf("seed %d round %d: read = %+v, want one of %q,%q",
						seed, round, r, v1, v2)
				}
				last = r.Result
			} else {
				v := fmt.Sprintf("s%d-r%d", seed, round)
				if w := c.Write(v); !w.OK {
					t.Fatalf("seed %d round %d: write not completed", seed, round)
				}
				r := c.Read()
				if !r.OK || r.Result != v {
					t.Fatalf("seed %d round %d: read = %+v, want %q", seed, round, r, v)
				}
				last = v
			}
		}
		// Quiescent final read from a fully recovered cluster must not
		// regress below the last observed value.
		for _, n := range s.Nodes() {
			n.Restart()
		}
		if r := c.Read(); !r.OK || r.Result != last {
			t.Fatalf("seed %d: final read = %+v, want %q", seed, r, last)
		}
	}
}
