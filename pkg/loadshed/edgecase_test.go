package loadshed

import "testing"

// 边界：下游零容量且仅经 SOFT 边可达时，应全切软边、零脱落，且不超容量。
func TestZeroCapacitySoftOnly(t *testing.T) {
	nodes := []Node{
		{ID: "E", Cap: 100, Arrival: [NumPriorities]float64{50, 0, 0, 0}},
		{ID: "Z", Cap: 0},
	}
	edges := []Edge{{From: "E", To: "Z", Fanout: 4, Dep: Soft, DegradeCost: 1}}
	g, err := NewGraph(nodes, edges)
	if err != nil {
		t.Fatalf("NewGraph: %v", err)
	}
	sol, err := Solve(g)
	if err != nil {
		t.Fatalf("Solve: %v", err)
	}
	if len(sol.CutEdges) != 1 {
		t.Fatalf("expected soft edge cut for zero cap, got %v", sol.CutEdges)
	}
	floatEQ(t, "no rejection", sol.Rejected[CriticalPlus], 0)
	floatEQ(t, "retained", sol.Retained[CriticalPlus], 50)
	z := nodePtr(g, "Z")
	floatEQ(t, "zero load on dead node", sol.Load[z], 0)
}

// 边界：零容量但经 HARD 边可达时无法降级，只能把相关入口脱落到 0。
func TestZeroCapacityHardSheds(t *testing.T) {
	nodes := []Node{
		{ID: "E", Cap: 100, Arrival: [NumPriorities]float64{50, 0, 0, 0}},
		{ID: "Z", Cap: 0},
	}
	edges := []Edge{{From: "E", To: "Z", Fanout: 4, Dep: Hard}}
	g, err := NewGraph(nodes, edges)
	if err != nil {
		t.Fatalf("NewGraph: %v", err)
	}
	sol, err := Solve(g)
	if err != nil {
		t.Fatalf("Solve: %v", err)
	}
	if len(sol.CutEdges) != 0 {
		t.Fatalf("hard edge cannot be cut, got %v", sol.CutEdges)
	}
	e := nodePtr(g, "E")
	floatEQ(t, "fully shed", sol.Admission[e][CriticalPlus], 0)
	floatEQ(t, "rejected all", sol.Rejected[CriticalPlus], 50)
	if sol.FullyAdmitted {
		t.Fatalf("zero-cap hard dependency must reject traffic (FullyAdmitted=false)")
	}
}

// 边界：不同扇出系数下的 Max-Min 公平——瓶颈约束按实际路由系数加权，速率仍逐流均等。
func TestMaxMinWithFanoutWeights(t *testing.T) {
	nodes := []Node{
		{ID: "I1", Cap: 1000, Arrival: [NumPriorities]float64{100, 0, 0, 0}},
		{ID: "I2", Cap: 1000, Arrival: [NumPriorities]float64{100, 0, 0, 0}},
		{ID: "B", Cap: 30},
	}
	edges := []Edge{
		// I1 每单位流量在 B 上系数 1，I2 系数 2，瓶颈 30 => x+2x=30 => 各得速率 10。
		{From: "I1", To: "B", Fanout: 1, Dep: Hard},
		{From: "I2", To: "B", Fanout: 2, Dep: Hard},
	}
	g, err := NewGraph(nodes, edges)
	if err != nil {
		t.Fatalf("NewGraph: %v", err)
	}
	sol, err := Solve(g)
	if err != nil {
		t.Fatalf("Solve: %v", err)
	}
	i1, i2 := nodePtr(g, "I1"), nodePtr(g, "I2")
	floatEQ(t, "I1 rate", sol.Load[i1], 10)
	floatEQ(t, "I2 rate", sol.Load[i2], 10)
	floatEQ(t, "I1 ratio", sol.Admission[i1][CriticalPlus], 0.1)
	floatEQ(t, "I2 ratio", sol.Admission[i2][CriticalPlus], 0.1)
	b := nodePtr(g, "B")
	floatEQ(t, "weighted bottleneck", sol.Load[b], 30)
}

// 边界：无入口流量的孤立节点不影响求解，比例保持全放行默认值。
func TestNoArrivalNodes(t *testing.T) {
	nodes := []Node{
		{ID: "A", Cap: 10},
		{ID: "B", Cap: 10},
	}
	g, err := NewGraph(nodes, nil)
	if err != nil {
		t.Fatalf("NewGraph: %v", err)
	}
	sol, err := Solve(g)
	if err != nil {
		t.Fatalf("Solve: %v", err)
	}
	for v := range g.Nodes {
		for p := Priority(0); p < NumPriorities; p++ {
			floatEQ(t, "default full admit", sol.Admission[v][p], 1)
		}
	}
	if !sol.FullyAdmitted {
		t.Fatalf("idle graph must fully admit all traffic")
	}
}
