package flowlb

import (
	"fmt"
	"testing"
)

func newTestSim(t *testing.T, flows int, ticks int64) (*Assigner, *Sim) {
	t.Helper()
	a := NewAssigner()
	for _, p := range []Path{
		{ID: "path-A", Weight: 3, Rate: 10000},
		{ID: "path-B", Weight: 2, Rate: 10000},
		{ID: "path-C", Weight: 1, Rate: 10000},
		{ID: "path-standby", Weight: 0, Rate: 10000},
	} {
		a.AddPath(p)
	}
	s := NewSim(a)
	for i := 0; i < flows; i++ {
		s.AddFlow(fmt.Sprintf("flow-%05d", i), 0, ticks)
	}
	return a, s
}

func TestSimDeliversAllPackets(t *testing.T) {
	_, s := newTestSim(t, 200, 100)
	s.Run(100)
	if s.Dropped != 0 {
		t.Fatalf("dropped = %d, want 0", s.Dropped)
	}
	delivered := 0
	for _, p := range []string{"path-A", "path-B", "path-C", "path-standby"} {
		st := s.Stats(p)
		delivered += st.Delivered
		if st.TotalWait < 0 {
			t.Fatalf("%s has negative total wait", p)
		}
		if backlog := s.Backlog(p); backlog != 0 {
			t.Fatalf("%s backlog = %d, want 0 (links are over-provisioned)", p, backlog)
		}
	}
	if delivered != s.Generated {
		t.Fatalf("delivered = %d, generated = %d", delivered, s.Generated)
	}
}

func TestSimRespectsAssignment(t *testing.T) {
	a, s := newTestSim(t, 500, 50)
	s.Run(50)
	// Per-path delivered counts must match the assignment table exactly:
	// every packet of a flow travels on the flow's single assigned path.
	want := map[string]int{}
	for i := 0; i < 500; i++ {
		flowID := fmt.Sprintf("flow-%05d", i)
		pathID, ok := a.Assign(flowID)
		if !ok {
			t.Fatalf("flow %s unassigned", flowID)
		}
		// Recompute the deterministic schedule to count packets per flow.
		tmp := NewSim(a)
		tmp.AddFlow(flowID, 0, 50)
		want[pathID] += tmp.arrivals.Len()
	}
	for pathID, n := range want {
		if got := s.Stats(pathID).Delivered; got != n {
			t.Fatalf("%s delivered = %d, want %d", pathID, got, n)
		}
	}
}

func TestSimZeroWeightPathIdle(t *testing.T) {
	_, s := newTestSim(t, 500, 50)
	s.Run(50)
	if st := s.Stats("path-standby"); st.Delivered != 0 || st.MaxQueue != 0 {
		t.Fatalf("weight-0 path delivered %d packets (max queue %d), want 0", st.Delivered, st.MaxQueue)
	}
}

func TestSimCongestionBuildsQueue(t *testing.T) {
	a := NewAssigner()
	a.AddPath(Path{ID: "path-slow", Weight: 1, Rate: 1})
	s := NewSim(a)
	// Ten flows on one 1 packet/tick link generate ~4 packets/tick in
	// aggregate, so the queue must grow.
	for i := 0; i < 10; i++ {
		s.AddFlow(fmt.Sprintf("flow-hot-%d", i), 0, 1000)
	}
	s.Run(1000)
	st := s.Stats("path-slow")
	if st.MaxQueue == 0 {
		t.Fatal("expected queue buildup on a 1 packet/tick link")
	}
	if st.AvgWait() <= 0 {
		t.Fatalf("avg wait = %v, want > 0 under congestion", st.AvgWait())
	}
	if st.Delivered+s.Backlog("path-slow") != s.Generated {
		t.Fatalf("delivered %d + backlog %d != generated %d", st.Delivered, s.Backlog("path-slow"), s.Generated)
	}
}
