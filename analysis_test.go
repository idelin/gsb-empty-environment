package netsec

import "testing"

// dmzGraph models: ingress dmz -> nat -> mgmt, with a loopback monitor
// (nat -> monitor -> nat) so the fixpoint engine must handle cycles.
func dmzGraph(t *testing.T) *Graph {
	t.Helper()
	nodes := []Node{
		{
			ID: 1,
			Filters: []FilterRule{
				{Name: "only-dmz", Field: Src, Match: Interval{Lo: 10, Hi: 19}, Allow: true},
				{Name: "deny-rest", Field: Src, Match: Interval{Lo: 0, Hi: ^uint32(0)}, Allow: false},
			},
		},
		{
			ID: 2,
			Translates: []TranslateRule{
				{
					Name:   "snat-to-pool",
					Target: Src,
					Match:  Interval{Lo: 10, Hi: 19},
					Delta:  90,
				},
			},
		},
		{
			ID: 3,
			Filters: []FilterRule{
				{Name: "allow-mgmt-traffic", Field: Dst, Match: Interval{Lo: 80, Hi: 89}, Allow: true},
				{Name: "deny", Field: Dst, Match: Interval{Lo: 0, Hi: ^uint32(0)}, Allow: false},
			},
		},
		{
			ID: 4,
			Translates: []TranslateRule{
				{Name: "noop-shift", Target: Dst, Match: Interval{Lo: 80, Hi: 89}, Delta: 0},
			},
		},
	}
	edges := []Edge{
		{Src: 1, Dst: 2},
		{Src: 2, Dst: 3},
		{Src: 2, Dst: 4},
		{Src: 4, Dst: 2},
	}
	g, err := NewGraph(nodes, edges)
	if err != nil {
		t.Fatalf("NewGraph: %v", err)
	}
	return g
}

func TestAnalyzeNATReachabilityAndWitness(t *testing.T) {
	g := dmzGraph(t)
	report, err := Analyze(g, AnalysisConfig{
		Entry:   1,
		Exit:    3,
		Ingress: []Constraint{{Field: Src, Range: Interval{Lo: 10, Hi: 19}}, {Field: Dst, Range: Interval{Lo: 80, Hi: 89}}},
		Egress:  []Constraint{{Field: Src, Range: Interval{Lo: 100, Hi: 109}}, {Field: Dst, Range: Interval{Lo: 80, Hi: 89}}},
	})
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	if !report.Reachable() {
		t.Fatal("management service should be reachable after SNAT")
	}
	if len(report.Mismatches) != 0 {
		t.Fatalf("symbolic/enumeration mismatch: %+v", report.Mismatches)
	}
	if len(report.Witnesses) == 0 {
		t.Fatal("expected at least one replayable witness")
	}
	for _, w := range report.Witnesses {
		final, err := w.Replay(g)
		if err != nil {
			t.Fatalf("witness replay failed: %v", err)
		}
		if final.Src < 100 || final.Src > 109 || final.Dst < 80 || final.Dst > 89 {
			t.Fatalf("witness final packet outside egress: %+v", final)
		}
		if !packetSatisfies(w.Packet, report.Config.Ingress) {
			t.Fatalf("witness entry packet violates ingress: %+v", w.Packet)
		}
	}
	// The monitor loop must be observed as fixpoint subsumptions.
	if len(report.LoopEvents) == 0 {
		t.Fatal("expected loop subsumption events from monitor cycle")
	}
}

func TestAnalyzeUnreachableWhenFiltered(t *testing.T) {
	g := dmzGraph(t)
	report, err := Analyze(g, AnalysisConfig{
		Entry:   1,
		Exit:    3,
		Ingress: []Constraint{{Field: Src, Range: Interval{Lo: 100, Hi: 109}}, {Field: Dst, Range: Interval{Lo: 80, Hi: 89}}},
		Egress:  []Constraint{{Field: Src, Range: Interval{Lo: 100, Hi: 109}}, {Field: Dst, Range: Interval{Lo: 80, Hi: 89}}},
	})
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	if report.Reachable() {
		t.Fatal("non-DMZ source must be filtered at entry")
	}
	if len(report.Mismatches) != 0 {
		t.Fatalf("unexpected mismatch: %+v", report.Mismatches)
	}
	if len(report.Witnesses) != 0 {
		t.Fatal("unreachable egress must not produce witnesses")
	}
}

func TestOverflowDroppedAndEnumerated(t *testing.T) {
	g, err := NewGraph(
		[]Node{
			{
				ID: 1,
				Translates: []TranslateRule{
					{Name: "edge-shift", Target: Dst, Match: Interval{Lo: 0, Hi: ^uint32(0)}, Delta: 1 << 40},
				},
			},
		},
		nil,
	)
	if err != nil {
		t.Fatalf("NewGraph: %v", err)
	}
	engine := newFixpointEngine(g, 0)
	if err := engine.run(1, Rect{
		S: Interval{Lo: 0, Hi: 10},
		D: Interval{Lo: 0, Hi: 10},
	}); err != nil {
		t.Fatalf("run: %v", err)
	}
	if got := engine.counter(1).OverflowRects; got == 0 {
		t.Fatalf("expected overflow rectangles, got counters %+v", engine.counter(1))
	}
	if len(engine.outputAt(1)) != 0 {
		t.Fatalf("all packets overflow, expected no output state, got %+v", engine.outputAt(1))
	}
	if len(engine.reachableAt(1)) == 0 {
		t.Fatal("input state at the node must still be recorded for cross-checking")
	}

	domain, err := BuildNarrowedDomain(g, nil, nil, DomainConfig{})
	if err != nil {
		t.Fatalf("BuildNarrowedDomain: %v", err)
	}
	enum, err := Enumerate(g, 1, nil, domain)
	if err != nil {
		t.Fatalf("Enumerate: %v", err)
	}
	if enum.OverflowPackets == 0 {
		t.Fatal("enumerator must record concrete overflow packets")
	}
	if len(g.Edges[1]) != 0 {
		t.Fatal("test graph should have no outgoing edges")
	}
	// The entry packet is observable at the node input, but stepping it must
	// end in overflow rather than producing any propagated successor.
	if got := len(enum.Reachable[1]); got == 0 {
		t.Fatal("node-input state should still be enumerated")
	}
}

func TestNarrowedDomainIsDeltaClosed(t *testing.T) {
	g := dmzGraph(t)
	domain, err := BuildNarrowedDomain(g,
		[]Constraint{{Field: Src, Range: Interval{Lo: 10, Hi: 19}}},
		[]Constraint{{Field: Src, Range: Interval{Lo: 100, Hi: 109}}},
		DomainConfig{},
	)
	if err != nil {
		t.Fatalf("BuildNarrowedDomain: %v", err)
	}
	for _, n := range g.Nodes {
		for _, rule := range n.Translates {
			for _, v := range domain.Values {
				if !rule.Match.Contains(v) {
					continue
				}
				next, ok := addChecked(v, rule.Delta)
				if ok && !domain.Contains(next) {
					t.Fatalf("domain not closed: %d + %d = %d missing", v, rule.Delta, next)
				}
			}
		}
	}
	if domain.Size() > DefaultDomainMax {
		t.Fatalf("domain exceeded default budget: %d", domain.Size())
	}
}

func TestDomainBudgetError(t *testing.T) {
	g, err := NewGraph([]Node{
		{
			ID: 1,
			Translates: []TranslateRule{
				{Name: "march", Target: Src, Match: Interval{Lo: 0, Hi: ^uint32(0)}, Delta: 1},
			},
		},
	}, nil)
	if err != nil {
		t.Fatalf("NewGraph: %v", err)
	}
	_, err = BuildNarrowedDomain(g, nil, nil, DomainConfig{MaxValues: 8})
	if err == nil {
		t.Fatal("expected ErrDomainTooLarge when closure exceeds budget")
	}
}

func TestCrossCheckDetectsTamperedSymbolicClaim(t *testing.T) {
	g := dmzGraph(t)
	domain, err := BuildNarrowedDomain(g,
		[]Constraint{{Field: Src, Range: Interval{Lo: 10, Hi: 19}}},
		nil, DomainConfig{})
	if err != nil {
		t.Fatalf("domain: %v", err)
	}
	enum, err := Enumerate(g, 1, []Constraint{{Field: Src, Range: Interval{Lo: 10, Hi: 19}}}, domain)
	if err != nil {
		t.Fatalf("enumerate: %v", err)
	}
	fake := map[int][]Rect{
		3: {{S: Interval{Lo: 0, Hi: 1}, D: Interval{Lo: 0, Hi: 1}}},
	}
	mismatches := crossCheck(g, AnalysisConfig{Entry: 1, Exit: 3}, domain, fake, enum)
	if len(mismatches) == 0 {
		t.Fatal("cross-check must flag a fabricated symbolic region")
	}
}
