package main

import "testing"

func scenario(omitB bool) *Sim {
	return runScenario(omitB)
}

func analyzePkt(s *Sim, id int) Analysis {
	p := s.pkts[id]
	return s.DAG.Analyze([]int{p.createdID}, p.deliveredID)
}

func TestExpectedDeliveryTicks(t *testing.T) {
	s := scenario(false)
	want := map[int]int{1: 50, 2: 60}
	for id, tick := range want {
		p := s.pkts[id]
		if !p.delivered {
			t.Fatalf("packet %d not delivered", id)
		}
		if got := s.DAG.Event(p.deliveredID).Tick; got != tick {
			t.Fatalf("packet %d delivered at %d, want %d", id, got, tick)
		}
	}
}

func TestBreakdownConsistentWithSendToReceive(t *testing.T) {
	s := scenario(false)
	for _, id := range []int{1, 2} {
		p := s.pkts[id]
		a := analyzePkt(s, id)
		total := s.DAG.Event(p.deliveredID).Tick - s.DAG.Event(p.createdID).Tick
		if a.Lower != total || a.Upper != total {
			t.Fatalf("packet %d: critical path [%d,%d] != send->receive %d",
				id, a.Lower, a.Upper, total)
		}
		lo, hi := a.BreakdownSum()
		if lo != total || hi != total {
			t.Fatalf("packet %d: breakdown sum [%d,%d] != total %d", id, lo, hi, total)
		}
		if len(a.Path) == 0 || a.Path[len(a.Path)-1] != p.deliveredID {
			t.Fatalf("packet %d: critical path does not end at delivered event", id)
		}
	}
}

func TestPhaseBreakdownValues(t *testing.T) {
	s := scenario(false)
	a1 := analyzePkt(s, 1)
	if got := a1.Breakdown[PhaseRetransmit]; got != [2]int{30, 30} {
		t.Fatalf("packet 1 retransmit wait = %v, want [30,30]", got)
	}
	if got := a1.Breakdown[PhaseSerialize]; got != [2]int{15, 15} {
		t.Fatalf("packet 1 serialize = %v, want [15,15]", got)
	}
	if got := a1.Breakdown[PhasePropagate]; got != [2]int{5, 5} {
		t.Fatalf("packet 1 propagate = %v, want [5,5]", got)
	}
	a2 := analyzePkt(s, 2)
	if got := a2.Breakdown[PhaseQueue]; got != [2]int{10, 10} {
		t.Fatalf("packet 2 queue wait = %v, want [10,10]", got)
	}
	ph, v := a1.Bottleneck()
	if ph != PhaseRetransmit || v != 30 {
		t.Fatalf("packet 1 bottleneck = %s (%d), want retransmit (30)", ph, v)
	}
}

func TestWorkExceedsWallClockUnderParallelism(t *testing.T) {
	s := scenario(false)
	a := analyzePkt(s, 1)
	if a.WorkUpper != 73 {
		t.Fatalf("packet 1 cumulative work = %d, want 73", a.WorkUpper)
	}
	if a.WorkUpper <= a.Upper {
		t.Fatalf("work %d should exceed wall clock %d (parallel copies)", a.WorkUpper, a.Upper)
	}
	paths, err := s.DAG.EnumeratePaths([]int{s.pkts[1].createdID}, s.pkts[1].deliveredID, 100000)
	if err != nil {
		t.Fatal(err)
	}
	maxW := 0
	for _, p := range paths {
		if w := s.DAG.PathWeight(p, true); w > maxW {
			maxW = w
		}
	}
	if maxW != a.Upper {
		t.Fatalf("exhaustive max path %d != DP critical path %d", maxW, a.Upper)
	}
}

func TestLostPacketHasNoSuccessDelay(t *testing.T) {
	s := scenario(false)
	p := s.pkts[3]
	if p.delivered {
		t.Fatal("packet 3 must be lost")
	}
	if !p.lost {
		t.Fatal("packet 3 must be marked lost after max attempts")
	}
	a := s.DAG.Analyze([]int{p.createdID}, p.deliveredID)
	if a.SinkFound {
		t.Fatal("lost packet must not yield a successful end-to-end delay")
	}
	lo := s.DAG.MaxTickForPacket(3) - s.DAG.Event(p.createdID).Tick
	if lo != 140 {
		t.Fatalf("lost packet elapsed lower bound = %d, want 140", lo)
	}
}

func TestMissingTxStartYieldsBounds(t *testing.T) {
	s := scenario(true)
	a2 := analyzePkt(s, 2)
	if a2.Lower != 50 || a2.Upper != 60 {
		t.Fatalf("packet 2 critical path = [%d,%d], want [50,60]", a2.Lower, a2.Upper)
	}
	got := a2.Breakdown[PhaseQueueSerialize]
	if got != [2]int{10, 20} {
		t.Fatalf("packet 2 queue+serialize = %v, want [10,20]", got)
	}
	lo, hi := a2.BreakdownSum()
	if lo != a2.Lower || hi != a2.Upper {
		t.Fatalf("breakdown sum [%d,%d] != critical path [%d,%d]", lo, hi, a2.Lower, a2.Upper)
	}
	total := s.DAG.Event(s.pkts[2].deliveredID).Tick - s.DAG.Event(s.pkts[2].createdID).Tick
	if total < a2.Lower || total > a2.Upper {
		t.Fatalf("true total %d outside bounds [%d,%d]", total, a2.Lower, a2.Upper)
	}
}

func TestMultihopPath(t *testing.T) {
	links := []*Link{
		{Name: "A", Rate: 2, Prop: 5},
		{Name: "C", Rate: 1, Prop: 3},
	}
	s := NewSim(links, 1000, 0, nil)
	s.Send(1, 10, [][]int{{0, 1}}, 0)
	s.Run()
	p := s.pkts[1]
	if !p.delivered {
		t.Fatal("packet not delivered")
	}
	if got := s.DAG.Event(p.deliveredID).Tick; got != 23 {
		t.Fatalf("delivered at %d, want 23 (5 ser + 5 prop + 10 ser + 3 prop)", got)
	}
	a := analyzePkt(s, 1)
	if got := a.Breakdown[PhasePropagate]; got != [2]int{8, 8} {
		t.Fatalf("propagate = %v, want [8,8]", got)
	}
	if got := a.Breakdown[PhaseSerialize]; got != [2]int{15, 15} {
		t.Fatalf("serialize = %v, want [15,15]", got)
	}
}

func TestNoContradictionsInSimulation(t *testing.T) {
	s := scenario(false)
	if len(s.DAG.Contradictions()) != 0 {
		t.Fatalf("simulation produced %d contradictions", len(s.DAG.Contradictions()))
	}
}
