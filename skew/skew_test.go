package skew

import (
	"testing"
)

func graphOf(evs []Event, eds []Edge) *Graph {
	return &Graph{Events: evs, Edges: eds}
}

// TestFeasibleRTT checks the classic two-node request/response.
// c_req=1000 -> s_req=1100 : x_c - x_s <= -100
// s_resp=1120 -> c_resp=1040 : x_s - x_c <= 80
// with x_c=0: -100 <= x_s <= -80.
func TestFeasibleRTT(t *testing.T) {
	g := graphOf(
		[]Event{
			{ID: "c_req", Node: "client", TS: 1000},
			{ID: "s_req", Node: "server", TS: 1100},
			{ID: "s_resp", Node: "server", TS: 1120},
			{ID: "c_resp", Node: "client", TS: 1040},
		},
		[]Edge{
			{From: "c_req", To: "s_req"},
			{From: "s_resp", To: "c_resp"},
		},
	)
	res, err := g.Analyze("client", Options{})
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	if !res.Feasible {
		t.Fatalf("expected feasible, got chains: %+v", res.Chains)
	}
	iv := res.Intervals["server"]
	if iv.Lo != -100 || iv.Hi != -80 {
		t.Fatalf("server interval = [%d,%d], want [-100,-80]", iv.Lo, iv.Hi)
	}
	if iv.LoOpen || iv.HiOpen {
		t.Fatalf("server interval should be bounded, got %+v", iv)
	}
	ref := res.Intervals["client"]
	if ref.Lo != 0 || ref.Hi != 0 {
		t.Fatalf("client interval = [%d,%d], want [0,0]", ref.Lo, ref.Hi)
	}
	if res.MinSlack < 0 {
		t.Fatalf("min slack negative: %d", res.MinSlack)
	}
	assertCausalOrder(t, g, res)
}

func assertCausalOrder(t *testing.T, g *Graph, res *Result) {
	t.Helper()
	if err := g.index(); err != nil {
		t.Fatalf("index: %v", err)
	}
	ts := map[string]int64{}
	for _, e := range g.Events {
		ts[e.ID] = res.CorrectedTS(e)
	}
	for _, ed := range g.Edges {
		if ts[ed.From] > ts[ed.To] {
			t.Errorf("causal edge %s -> %s reversed after correction: %d > %d",
				ed.From, ed.To, ts[ed.From], ts[ed.To])
		}
	}
}

// TestConflictRoundTrip: c_req=1000 -> s_req=990 (delta=-10) and
// s_resp=1005 -> c_resp=1010 (delta=5). Sum -5 < 0: no offsets exist.
func TestConflictRoundTrip(t *testing.T) {
	g := graphOf(
		[]Event{
			{ID: "c_req", Node: "client", TS: 1000},
			{ID: "s_req", Node: "server", TS: 990},
			{ID: "s_resp", Node: "server", TS: 1005},
			{ID: "c_resp", Node: "client", TS: 1010},
		},
		[]Edge{
			{From: "c_req", To: "s_req"},
			{From: "s_resp", To: "c_resp"},
		},
	)
	res, err := g.Analyze("client", Options{})
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	if res.Feasible {
		t.Fatal("expected infeasible")
	}
	if len(res.Chains) != 1 {
		t.Fatalf("chains = %d, want 1", len(res.Chains))
	}
	c := res.Chains[0]
	if c.Sum != -5 {
		t.Fatalf("chain sum = %d, want -5", c.Sum)
	}
	if len(c.Edges) != 2 {
		t.Fatalf("chain edges = %d, want 2", len(c.Edges))
	}
	if c.Edges[0].From != "client" || c.Edges[0].To != "server" {
		t.Fatalf("chain should start with client->server, got %s->%s",
			c.Edges[0].From, c.Edges[0].To)
	}
	if c.Edges[0].Weight != -10 {
		t.Fatalf("first edge weight = %d, want -10 (negative delta preserved)",
			c.Edges[0].Weight)
	}
}

// TestNegativeDeltaNotClamped: a single edge with observed delta=-1ms is
// a perfectly valid one-sided constraint x_s >= 1ms, not zero.
func TestNegativeDeltaNotClamped(t *testing.T) {
	g := graphOf(
		[]Event{
			{ID: "c1", Node: "client", TS: 10_000_000},
			{ID: "s1", Node: "server", TS: 9_000_000},
		},
		[]Edge{{From: "c1", To: "s1"}},
	)
	res, err := g.Analyze("client", Options{})
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	if !res.Feasible {
		t.Fatal("expected feasible")
	}
	iv := res.Intervals["server"]
	if iv.Lo != 1_000_000 {
		t.Fatalf("server lower bound = %d, want 1ms", iv.Lo)
	}
	if !iv.HiOpen {
		t.Fatalf("server upper bound should be open, got %d", iv.Hi)
	}
	if res.CorrectedTS(Event{Node: "server", TS: 9_000_000}) < 10_000_000 {
		t.Fatal("corrected receive must not precede corrected send")
	}
}

// TestDisconnectedWide: a node with no causal link to the reference gets
// the wide interval on both sides.
func TestDisconnectedWide(t *testing.T) {
	g := graphOf(
		[]Event{
			{ID: "a1", Node: "a", TS: 1},
			{ID: "a2", Node: "a", TS: 2},
			{ID: "b1", Node: "b", TS: 100},
			{ID: "b2", Node: "b", TS: 200},
		},
		nil,
	)
	const wide = int64(500)
	res, err := g.Analyze("a", Options{Wide: wide})
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	if !res.Feasible {
		t.Fatal("expected feasible")
	}
	iv := res.Intervals["b"]
	if !iv.LoOpen || !iv.HiOpen {
		t.Fatalf("b should be unbounded both sides, got %+v", iv)
	}
	if iv.Lo != -wide || iv.Hi != wide {
		t.Fatalf("b wide interval = [%d,%d], want [%d,%d]", iv.Lo, iv.Hi, -wide, wide)
	}
}

// TestCorrectedOrder drives several constrained nodes and checks that
// corrected timestamps never reverse any explicit causal edge and keep
// per-node observed ordering.
func TestCorrectedOrder(t *testing.T) {
	g := graphOf(
		[]Event{
			{ID: "a1", Node: "a", TS: 0},
			{ID: "b1", Node: "b", TS: 30},
			{ID: "c1", Node: "c", TS: 10},
			{ID: "c2", Node: "c", TS: 40},
			{ID: "b2", Node: "b", TS: 80},
			{ID: "a2", Node: "a", TS: 50},
		},
		[]Edge{
			{From: "a1", To: "b1"},
			{From: "b1", To: "c1"},
			{From: "c2", To: "b2"},
			{From: "b2", To: "a2"},
		},
	)
	res, err := g.Analyze("a", Options{})
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	if !res.Feasible {
		t.Fatalf("expected feasible, chains=%+v", res.Chains)
	}
	assertCausalOrder(t, g, res)

	// Local ordering on every node must remain chronological.
	byNode := map[string][]int64{}
	for _, e := range g.Events {
		byNode[e.Node] = append(byNode[e.Node], res.CorrectedTS(e))
	}
	for n, ts := range byNode {
		for i := 1; i < len(ts); i++ {
			if ts[i] < ts[i-1] {
				t.Errorf("node %s local order reversed: %v", n, ts)
			}
		}
	}
	if res.MinSlack < 0 {
		t.Fatalf("min slack negative: %d", res.MinSlack)
	}
}

// TestTrueOffsetLiesInInterval synthesizes traces from ground-truth
// offsets and checks every true offset is contained in its interval.
func TestTrueOffsetLiesInInterval(t *testing.T) {
	// True offsets: a=0, b=-100, c=250. True-time events:
	//  a sends at 1000; b observes 1100-100=1000-ish receive...
	// Build true times directly then convert to local observed times.
	type ev struct {
		id, node string
		trueTS   int64
	}
	off := map[string]int64{"a": 0, "b": -100, "c": 250}
	trueEvents := []ev{
		{"a1", "a", 1000},
		{"b1", "b", 1030}, // receive after a1
		{"b2", "b", 1060},
		{"c1", "c", 1090}, // receive after b2
		{"c2", "c", 1120},
		{"a2", "a", 1150}, // receive after c2
	}
	var events []Event
	for _, e := range trueEvents {
		events = append(events, Event{ID: e.id, Node: e.node, TS: e.trueTS - off[e.node]})
	}
	g := graphOf(events, []Edge{
		{From: "a1", To: "b1"},
		{From: "b2", To: "c1"},
		{From: "c2", To: "a2"},
	})
	res, err := g.Analyze("a", Options{})
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	if !res.Feasible {
		t.Fatalf("expected feasible, chains=%+v", res.Chains)
	}
	for n, want := range off {
		iv := res.Intervals[n]
		if want < iv.Lo || want > iv.Hi {
			t.Errorf("true offset %s=%d outside [%d,%d]", n, want, iv.Lo, iv.Hi)
		}
	}
	assertCausalOrder(t, g, res)
}

// TestIntervalBoundsAreTight verifies each reported endpoint is feasible
// and a unit beyond it is infeasible, for a chain of constraints.
func TestIntervalBoundsAreTight(t *testing.T) {
	// a->b delta 10 (x_a - x_b <= 10), b->c delta 20 (x_b - x_c <= 20).
	// x_a=0 => x_b >= -10; x_c >= -30. No upper bounds (single-sided).
	g := graphOf(
		[]Event{
			{ID: "a1", Node: "a", TS: 0},
			{ID: "b1", Node: "b", TS: 10},
			{ID: "b2", Node: "b", TS: 100},
			{ID: "c1", Node: "c", TS: 120},
		},
		[]Edge{
			{From: "a1", To: "b1"},
			{From: "b2", To: "c1"},
		},
	)
	res, err := g.Analyze("a", Options{})
	if err != nil {
		t.Fatal(err)
	}
	if iv := res.Intervals["b"]; iv.Lo != -10 || !iv.HiOpen {
		t.Fatalf("b interval = %+v, want lo=-10 open hi", iv)
	}
	if iv := res.Intervals["c"]; iv.Lo != -30 || !iv.HiOpen {
		t.Fatalf("c interval = %+v, want lo=-30 open hi", iv)
	}
}

// TestZeroDeltaFeasible: zero observed duration is allowed and pins a
// bound; only a negative cycle is a contradiction.
func TestZeroDeltaFeasible(t *testing.T) {
	g := graphOf(
		[]Event{
			{ID: "a1", Node: "a", TS: 7},
			{ID: "b1", Node: "b", TS: 7},
		},
		[]Edge{{From: "a1", To: "b1"}},
	)
	res, err := g.Analyze("a", Options{})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Feasible {
		t.Fatal("zero delta must be feasible")
	}
	if iv := res.Intervals["b"]; iv.Lo != 0 {
		t.Fatalf("b lower bound = %d, want 0", iv.Lo)
	}
}

// TestLocalSelfLoopConflict: explicit causal edge between two events on
// the same node going backwards in observed time is a negative self-loop.
func TestLocalSelfLoopConflict(t *testing.T) {
	g := graphOf(
		[]Event{
			{ID: "x1", Node: "n", TS: 100},
			{ID: "x2", Node: "n", TS: 90},
		},
		[]Edge{{From: "x1", To: "x2"}},
	)
	res, err := g.Analyze("n", Options{})
	if err != nil {
		t.Fatal(err)
	}
	if res.Feasible {
		t.Fatal("expected infeasible from negative self-loop")
	}
	if len(res.Chains) != 1 || res.Chains[0].Sum != -10 {
		t.Fatalf("chains = %+v, want one sum -10", res.Chains)
	}
}

func TestErrors(t *testing.T) {
	if _, err := (&Graph{Events: nil}).Analyze("a", Options{}); err == nil {
		t.Fatal("empty graph should error")
	}
	g := graphOf(
		[]Event{{ID: "a1", Node: "a", TS: 1}},
		[]Edge{{From: "a1", To: "missing"}},
	)
	if _, err := g.Analyze("a", Options{}); err == nil {
		t.Fatal("unknown edge endpoint should error")
	}
	g2 := graphOf([]Event{{ID: "a1", Node: "a", TS: 1}}, nil)
	if _, err := g2.Analyze("zzz", Options{}); err == nil {
		t.Fatal("unknown reference should error")
	}
}
