package main

import "fmt"

func runScenario(omitB bool) *Sim {
	links := []*Link{
		{Name: "A", Rate: 2, Prop: 5},
		{Name: "B", Rate: 1, Prop: 8, OmitTxStart: omitB},
	}
	drop := func(link string, pkt, attempt int) bool {
		if pkt == 3 {
			return true
		}
		return attempt == 0
	}
	s := NewSim(links, 30, 2, drop)
	s.Send(1, 10, [][]int{{0}, {1}}, 0)
	s.Send(2, 10, [][]int{{0}, {1}}, 0)
	s.Send(3, 10, [][]int{{0}, {1}}, 0)
	s.Run()
	return s
}

func report(s *Sim) {
	for _, id := range s.order {
		p := s.pkts[id]
		created := s.DAG.Event(p.createdID)
		if !p.delivered {
			lo := s.DAG.MaxTickForPacket(id) - created.Tick
			fmt.Printf("packet %d: LOST, no successful delay (elapsed >= %d ticks, upper bound unknown)\n", id, lo)
			continue
		}
		del := s.DAG.Event(p.deliveredID)
		a := s.DAG.Analyze([]int{p.createdID}, p.deliveredID)
		total := del.Tick - created.Tick
		fmt.Printf("packet %d: delivered at tick %d, end-to-end %d ticks (critical path [%d,%d])\n",
			id, del.Tick, total, a.Lower, a.Upper)
		fmt.Printf("  critical path event IDs: %v\n", a.Path)
		fmt.Print("  breakdown:")
		for _, ph := range PhaseOrder {
			b, ok := a.Breakdown[ph]
			if !ok || (b[0] == 0 && b[1] == 0) {
				continue
			}
			if b[0] == b[1] {
				fmt.Printf(" %s=%d", ph, b[1])
			} else {
				fmt.Printf(" %s=[%d,%d]", ph, b[0], b[1])
			}
		}
		fmt.Println()
		ph, v := a.Bottleneck()
		fmt.Printf("  bottleneck: %s (%d ticks)\n", ph, v)
		fmt.Printf("  cumulative work: [%d,%d] ticks (parallel copies counted; overlap not double-added to wall clock)\n",
			a.WorkLower, a.WorkUpper)
		lo, hi := a.BreakdownSum()
		fmt.Printf("  consistency: breakdown sum [%d,%d] vs send->receive %d\n", lo, hi, total)
	}
	for _, c := range s.DAG.Contradictions() {
		fmt.Printf("contradiction: %s | evidence: raw delta %d ticks, edge kept with clamped weight\n",
			c.Reason, c.Edge.RawDelta)
	}
}

func demoContradiction() {
	d := NewDAG()
	tx := d.AddEvent(8, "tx_end", 9, 0, "linkX")
	rv := d.AddEvent(3, "received", 9, 0, "linkX")
	d.AddEdge(tx, rv, PhasePropagate)
	for _, c := range d.Contradictions() {
		fmt.Printf("contradiction: %s\n", c.Reason)
		fmt.Printf("  evidence: edge #%d->#%d raw delta %d ticks (clamped to 0 for computation)\n",
			c.Edge.From, c.Edge.To, c.Edge.RawDelta)
	}
}

func main() {
	fmt.Println("=== scenario: 3 packets x 10B, duplicated onto link A (2B/tick, prop 5) and link B (1B/tick, prop 8),")
	fmt.Println("    attempt 0 dropped on both links (packet 3 always dropped), RTO=30, max 2 retransmits ===")
	report(runScenario(false))
	fmt.Println()
	fmt.Println("=== same scenario with tx_start events missing on link B (bounds instead of zeros) ===")
	report(runScenario(true))
	fmt.Println()
	fmt.Println("=== contradictory timestamps: original edge evidence ===")
	demoContradiction()
}
