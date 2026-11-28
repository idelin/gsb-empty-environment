package loadshed

import (
	"math"
	"testing"
)

func floatEQ(t *testing.T, name string, got, want float64) {
	t.Helper()
	scale := math.Max(1.0, math.Max(math.Abs(got), math.Abs(want)))
	if math.Abs(got-want) > 1e-7*scale {
		t.Fatalf("%s = %.10f, want %.10f", name, got, want)
	}
}

func nodePtr(g *Graph, id string) int {
	v, ok := g.NodeIndex(id)
	if !ok {
		panic("missing node " + id)
	}
	return v
}

// 验收 1：菱形多路径扇出总负载精确守恒。
// 入口 E -> 中台 A/B（fanout 各 2、3）-> 共享存储 D，两条路径在 D 上必须精确累加而非按树重复。
func TestDiamondMultipathConservation(t *testing.T) {
	nodes := []Node{
		{ID: "E", Cap: 1000, Arrival: [NumPriorities]float64{100, 0, 0, 0}},
		{ID: "A", Cap: 1000},
		{ID: "B", Cap: 1000},
		{ID: "D", Cap: 1000},
	}
	edges := []Edge{
		{From: "E", To: "A", Fanout: 2, Dep: Hard},
		{From: "E", To: "B", Fanout: 3, Dep: Hard},
		{From: "A", To: "D", Fanout: 2, Dep: Hard},
		{From: "B", To: "D", Fanout: 1, Dep: Hard},
	}
	g, err := NewGraph(nodes, edges)
	if err != nil {
		t.Fatalf("NewGraph: %v", err)
	}
	r, err := g.BuildRouting(nil)
	if err != nil {
		t.Fatalf("BuildRouting: %v", err)
	}
	e, a, b, d := nodePtr(g, "E"), nodePtr(g, "A"), nodePtr(g, "B"), nodePtr(g, "D")
	// E 的 1 单位流量：E=1，A=2，B=3，D=2*2+3*1=7（共享路径精确合并）。
	floatEQ(t, "coef E->E", r.Coef[CriticalPlus][e][e], 1)
	floatEQ(t, "coef E->A", r.Coef[CriticalPlus][e][a], 2)
	floatEQ(t, "coef E->B", r.Coef[CriticalPlus][e][b], 3)
	floatEQ(t, "coef E->D", r.Coef[CriticalPlus][e][d], 7)

	adm := g.NewAdmission()
	perNode, total := g.Propagate(r, adm)
	floatEQ(t, "load E", total[e], 100)
	floatEQ(t, "load A", total[a], 200)
	floatEQ(t, "load B", total[b], 300)
	floatEQ(t, "load D merged", total[d], 700)
	floatEQ(t, "per-priority load D", perNode[d][CriticalPlus], 700)

	// 容量充足时全量放行、无脱落、无切边。
	sol, err := Solve(g)
	if err != nil {
		t.Fatalf("Solve: %v", err)
	}
	floatEQ(t, "retained CP", sol.Retained[CriticalPlus], 100)
	floatEQ(t, "rejected CP", sol.Rejected[CriticalPlus], 0)
	if len(sol.CutEdges) != 0 {
		t.Fatalf("expected no cut edges, got %v", sol.CutEdges)
	}
	for v, nd := range g.Nodes {
		if sol.Load[v] > nd.Cap+1e-6 {
			t.Fatalf("node %s overloaded: %.3f > %.3f", nd.ID, sol.Load[v], nd.Cap)
		}
	}
}

// 验收 2：切断弱依赖保全关键流量。软边切断只清零边流量、不增加上游失败，且优先切低代价边。
func TestSoftDegradePreservesCritical(t *testing.T) {
	nodes := []Node{
		{ID: "G", Cap: 1000, Arrival: [NumPriorities]float64{100, 0, 0, 0}},
		{ID: "H", Cap: 1000},
		{ID: "S", Cap: 120},
	}
	edges := []Edge{
		{From: "G", To: "H", Fanout: 1, Dep: Hard},
		// 两条平行 SOFT 边都把 H 的请求扇出到 S，应优先切低代价边。
		{From: "H", To: "S", Fanout: 1, Dep: Soft, DegradeCost: 1},
		{From: "H", To: "S", Fanout: 1, Dep: Soft, DegradeCost: 10},
	}
	g, err := NewGraph(nodes, edges)
	if err != nil {
		t.Fatalf("NewGraph: %v", err)
	}
	sol, err := Solve(g)
	if err != nil {
		t.Fatalf("Solve: %v", err)
	}
	// S 全量负载 = 100*(1+1)=200 > 120；切低代价边后 S=100 <= 120。
	if len(sol.CutEdges) != 1 {
		t.Fatalf("expected exactly 1 cut edge, got %v", sol.CutEdges)
	}
	if g.Edges[sol.CutEdges[0]].DegradeCost != 1 {
		t.Fatalf("expected lowest-cost edge cut, got cost %.1f", g.Edges[sol.CutEdges[0]].DegradeCost)
	}
	floatEQ(t, "critical retained", sol.Retained[CriticalPlus], 100)
	floatEQ(t, "critical rejected", sol.Rejected[CriticalPlus], 0)
	floatEQ(t, "degrade cost", sol.DegradeCost, 1)
	s := nodePtr(g, "S")
	h := nodePtr(g, "H")
	floatEQ(t, "S load after cut", sol.Load[s], 100)
	// 上游 H 自身处理负载不变（软边切断不引起上游请求失败）。
	floatEQ(t, "H load preserved", sol.Load[h], 100)

	// 对照：全部为 HARD 边时无法降级，只能脱落关键请求。
	hardNodes := []Node{
		{ID: "G", Cap: 1000, Arrival: [NumPriorities]float64{100, 0, 0, 0}},
		{ID: "H", Cap: 1000},
		{ID: "S", Cap: 120},
	}
	hardEdges := []Edge{
		{From: "G", To: "H", Fanout: 1, Dep: Hard},
		{From: "H", To: "S", Fanout: 2, Dep: Hard},
	}
	hg, err := NewGraph(hardNodes, hardEdges)
	if err != nil {
		t.Fatalf("NewGraph hard: %v", err)
	}
	hsol, err := Solve(hg)
	if err != nil {
		t.Fatalf("Solve hard: %v", err)
	}
	if len(hsol.CutEdges) != 0 {
		t.Fatalf("hard edges must not be cut: %v", hsol.CutEdges)
	}
	floatEQ(t, "hard-case retained", hsol.Retained[CriticalPlus], 60)
	floatEQ(t, "hard-case rejected", hsol.Rejected[CriticalPlus], 40)
}

// 验收 3：四档优先级字典序严格保护。容量仅 30、硬路径系数 1，低档被完全牺牲而高档全保。
func TestLexicographicPriority(t *testing.T) {
	nodes := []Node{
		{ID: "X", Cap: 30, Arrival: [NumPriorities]float64{20, 20, 20, 20}},
	}
	g, err := NewGraph(nodes, nil)
	if err != nil {
		t.Fatalf("NewGraph: %v", err)
	}
	sol, err := Solve(g)
	if err != nil {
		t.Fatalf("Solve: %v", err)
	}
	floatEQ(t, "CP retained", sol.Retained[CriticalPlus], 20)
	floatEQ(t, "C retained", sol.Retained[Critical], 10)
	floatEQ(t, "SP retained", sol.Retained[SheddablePlus], 0)
	floatEQ(t, "S retained", sol.Retained[Sheddable], 0)
	floatEQ(t, "SP rejected", sol.Rejected[SheddablePlus], 20)
	floatEQ(t, "S rejected", sol.Rejected[Sheddable], 20)
	x := nodePtr(g, "X")
	if sol.Load[x] > g.Nodes[x].Cap+1e-6 {
		t.Fatalf("X overloaded %.4f", sol.Load[x])
	}

	// 子场景：存在 SOFT 边时应优先降级，而不是丢弃 CRITICAL_PLUS/CRITICAL。
	n2 := []Node{
		// 入口 X 自身容量充足，过载只发生在下游 Z；切断 X->Z 软边可在不牺牲任何入口流量的前提下降级。
		{ID: "X", Cap: 1000, Arrival: [NumPriorities]float64{40, 40, 0, 0}},
		{ID: "Z", Cap: 50},
	}
	e2 := []Edge{{From: "X", To: "Z", Fanout: 1, Dep: Soft, DegradeCost: 2}}
	g2, err := NewGraph(n2, e2)
	if err != nil {
		t.Fatalf("NewGraph soft-lex: %v", err)
	}
	s2, err := Solve(g2)
	if err != nil {
		t.Fatalf("Solve soft-lex: %v", err)
	}
	if len(s2.CutEdges) != 1 {
		t.Fatalf("expected soft cut to protect priority, got %v", s2.CutEdges)
	}
	floatEQ(t, "CP fully kept via degrade", s2.Retained[CriticalPlus], 40)
	floatEQ(t, "C fully kept via degrade", s2.Retained[Critical], 40)
	floatEQ(t, "no rejection after degrade", s2.Rejected[CriticalPlus]+s2.Rejected[Critical], 0)
}

// 验收 4：同优先级多入口竞争同一瓶颈时的 Max-Min 公平分配（按放行速率水填）。
func TestMaxMinFairness(t *testing.T) {
	nodes := []Node{
		{ID: "I1", Cap: 1000, Arrival: [NumPriorities]float64{30, 0, 0, 0}},
		{ID: "I2", Cap: 1000, Arrival: [NumPriorities]float64{60, 0, 0, 0}},
		{ID: "B", Cap: 40},
	}
	edges := []Edge{
		{From: "I1", To: "B", Fanout: 1, Dep: Hard},
		{From: "I2", To: "B", Fanout: 1, Dep: Hard},
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
	// 瓶颈 40 被两入口均分：各 20 速率；I1 比例 2/3，I2 比例 1/3。
	floatEQ(t, "I1 admitted rate", sol.Load[i1], 20)
	floatEQ(t, "I2 admitted rate", sol.Load[i2], 20)
	floatEQ(t, "I1 ratio", sol.Admission[i1][CriticalPlus], 2.0/3.0)
	floatEQ(t, "I2 ratio", sol.Admission[i2][CriticalPlus], 1.0/3.0)
	b := nodePtr(g, "B")
	floatEQ(t, "bottleneck saturated", sol.Load[b], 40)

	// 小到达者先封顶，剩余容量给大到达者：I1=30 封顶，I2=50。
	g.Nodes[b].Cap = 80
	sol2, err := Solve(g)
	if err != nil {
		t.Fatalf("Solve cap80: %v", err)
	}
	floatEQ(t, "I1 capped", sol2.Load[i1], 30)
	floatEQ(t, "I2 gets remainder", sol2.Load[i2], 50)
	floatEQ(t, "I1 ratio full", sol2.Admission[i1][CriticalPlus], 1)
	floatEQ(t, "I2 ratio", sol2.Admission[i2][CriticalPlus], 50.0/60.0)
}
