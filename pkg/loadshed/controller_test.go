package loadshed

import (
	"errors"
	"testing"
)

// 验收 5：调用环拦截，返回精确最短环。
func TestCycleRejected(t *testing.T) {
	nodes := []Node{
		{ID: "A", Cap: 10},
		{ID: "B", Cap: 10},
		{ID: "C", Cap: 10},
	}
	edges := []Edge{
		{From: "A", To: "B", Fanout: 1, Dep: Hard},
		{From: "B", To: "C", Fanout: 1, Dep: Hard},
		{From: "C", To: "A", Fanout: 1, Dep: Hard},
		// 额外的两节点短环 B<->C，最短环应是 B-C-B（长度 2）而非 A-B-C-A（长度 3）。
		{From: "C", To: "B", Fanout: 1, Dep: Hard},
	}
	_, err := NewGraph(nodes, edges)
	var ce *CycleError
	if !errors.As(err, &ce) {
		t.Fatalf("expected *CycleError, got %v", err)
	}
	if len(ce.Nodes) != 3 || ce.Nodes[0] != ce.Nodes[2] {
		t.Fatalf("expected shortest 2-edge closed cycle, got %v", ce.Nodes)
	}
	if ce.Nodes[0] != "B" || ce.Nodes[1] != "C" {
		t.Fatalf("expected shortest cycle B->C->B, got %v", ce.Nodes)
	}

	// 自环必须被识别为长度 1 的极小环。
	_, err = NewGraph(
		[]Node{{ID: "L", Cap: 1}},
		[]Edge{{From: "L", To: "L", Fanout: 1, Dep: Hard}},
	)
	var selfCE *CycleError
	if !errors.As(err, &selfCE) {
		t.Fatalf("expected self-loop *CycleError, got %v", err)
	}
	if len(selfCE.Nodes) != 2 || selfCE.Nodes[0] != "L" || selfCE.Nodes[1] != "L" {
		t.Fatalf("expected self loop [L L], got %v", selfCE.Nodes)
	}
}

// 验收 6：恢复滞回防震荡时间线（脱落档位 + SOFT 边两类对象）。
func TestHysteresisTimeline(t *testing.T) {
	// 单入口 E 经一条 SOFT 边到存储 S。初始容量充足，随后容量骤降触发降级/脱落，再恢复。
	nodes := []Node{
		{ID: "E", Cap: 1000, Arrival: [NumPriorities]float64{100, 0, 0, 0}},
		{ID: "S", Cap: 1000},
	}
	edges := []Edge{
		{From: "E", To: "S", Fanout: 1, Dep: Soft, DegradeCost: 5},
	}
	g, err := NewGraph(nodes, edges)
	if err != nil {
		t.Fatalf("NewGraph: %v", err)
	}
	cfg := HysteresisConfig{
		CooldownTicks:        2,
		SustainTicks:         2,
		RecoverHeadroomRatio: 0.1,
		RatioStep:            0.25,
		EdgeStep:             0.5,
	}
	c := NewController(g, cfg)
	sIdx := nodePtr(g, "S")
	edgeIdx := 0

	// tick1：容量骤降到 60，理想目标是切断 SOFT 边（S 负载归零，E 仍 100 全保）。
	g.Nodes[sIdx].Cap = 60
	snap1, err := c.Tick()
	if err != nil {
		t.Fatalf("tick1: %v", err)
	}
	if len(snap1.CutEdges) != 1 || snap1.CutEdges[0] != edgeIdx {
		t.Fatalf("tick1 expected soft edge cut, got cut=%v", snap1.CutEdges)
	}
	floatEQ(t, "tick1 S load shed to 0", snap1.Load[sIdx], 0)
	floatEQ(t, "tick1 E still fully served", snap1.Retained[CriticalPlus], 100)

	// tick2：即便容量已恢复，冷却窗（2 tick）未过，边不得恢复。
	g.Nodes[sIdx].Cap = 1000
	snap2, err := c.Tick()
	if err != nil {
		t.Fatalf("tick2: %v", err)
	}
	if len(snap2.CutEdges) != 1 {
		t.Fatalf("tick2 edge must remain cut during cooldown, got %v", snap2.EdgeScale)
	}
	if !snap2.Cooling {
		t.Fatalf("tick2 expected Cooling=true")
	}

	// tick3：冷却窗（2 tick）刚结束，但余量仅连续达标 1 个 tick（SustainTicks=2），仍不得恢复。
	snap3, err := c.Tick()
	if err != nil {
		t.Fatalf("tick3: %v", err)
	}
	if len(snap3.CutEdges) != 1 {
		t.Fatalf("tick3 edge must remain cut until sustained, got %v", snap3.EdgeScale)
	}

	// tick4：冷却窗与持续达标（tick2、tick3 共 2 个 tick）同时满足，按 EdgeStep=0.5 平滑恢复到 0.5。
	snap4, err := c.Tick()
	if err != nil {
		t.Fatalf("tick4: %v", err)
	}
	if len(snap4.CutEdges) != 0 || len(snap4.RecoveringEdges) != 1 {
		t.Fatalf("tick4 expected partial recovery, got cut=%v recovering=%v", snap4.CutEdges, snap4.RecoveringEdges)
	}
	floatEQ(t, "tick4 edge scale 0.5", snap4.EdgeScale[0].Scale, 0.5)
	// S 负载 = E 放行 100 * 0.5 = 50，未超容量且满足 10% 水位（余量 950 >= 100）。
	floatEQ(t, "tick4 S partial load", snap4.Load[sIdx], 50)

	// tick5：每一步恢复都会重新进入冷却窗，因此本 tick 维持 0.5，不允许连跳。
	snap5, err := c.Tick()
	if err != nil {
		t.Fatalf("tick5: %v", err)
	}
	if len(snap5.RecoveringEdges) != 1 {
		t.Fatalf("tick5 expected to hold at 0.5 during re-cooldown, got %v", snap5.EdgeScale)
	}
	floatEQ(t, "tick5 holds edge scale", snap5.EdgeScale[0].Scale, 0.5)

	// tick6：冷却与持续再次满足后，继续步长恢复到 1.0，完全恢复且全程无任何时刻过载。
	snap6, err := c.Tick()
	if err != nil {
		t.Fatalf("tick6: %v", err)
	}
	// tick4 的恢复动作重置了冷却窗，因此 tick6 仍在再冷却中，维持 0.5，杜绝连跳回涌。
	if len(snap6.RecoveringEdges) != 1 || !snap6.Cooling {
		t.Fatalf("tick6 expected re-cooldown hold at 0.5, got %v cooling=%v", snap6.EdgeScale, snap6.Cooling)
	}
	floatEQ(t, "tick6 holds partial load", snap6.Load[sIdx], 50)

	// tick7：再冷却结束且持续达标，恢复到 1.0 完全恢复。
	snap7, err := c.Tick()
	if err != nil {
		t.Fatalf("tick7: %v", err)
	}
	if len(snap7.RecoveringEdges) != 0 || len(snap7.CutEdges) != 0 {
		t.Fatalf("tick7 expected full recovery, got scales=%v", snap7.EdgeScale)
	}
	floatEQ(t, "tick7 S full load", snap7.Load[sIdx], 100)
	for _, snap := range []*Snapshot{snap1, snap2, snap3, snap4, snap5, snap6, snap7} {
		for v, nd := range g.Nodes {
			if snap.Load[v] > nd.Cap+1e-6 {
				t.Fatalf("tick %d node %s overloaded %.3f > %.3f (oscillation)", snap.Tick, nd.ID, snap.Load[v], nd.Cap)
			}
		}
	}

	// 独立子场景：纯比例脱落（无 SOFT 边）也必须走冷却+持续+步长恢复，杜绝瞬间回涌。
	hardNodes := []Node{
		{ID: "E", Cap: 1000, Arrival: [NumPriorities]float64{100, 0, 0, 0}},
		{ID: "S", Cap: 40},
	}
	hardEdges := []Edge{{From: "E", To: "S", Fanout: 1, Dep: Hard}}
	hg, err := NewGraph(hardNodes, hardEdges)
	if err != nil {
		t.Fatalf("NewGraph ratio: %v", err)
	}
	hc := NewController(hg, cfg)
	hsIdx := nodePtr(hg, "S")
	heIdx := nodePtr(hg, "E")
	hs1, err := hc.Tick()
	if err != nil {
		t.Fatalf("ratio tick1: %v", err)
	}
	floatEQ(t, "ratio tick1 admitted", hs1.Admission[heIdx][CriticalPlus], 0.4)
	floatEQ(t, "ratio tick1 load", hs1.Load[hsIdx], 40)

	hg.Nodes[hsIdx].Cap = 1000
	hs2, err := hc.Tick() // 冷却中
	if err != nil {
		t.Fatalf("ratio tick2: %v", err)
	}
	floatEQ(t, "ratio tick2 frozen", hs2.Admission[heIdx][CriticalPlus], 0.4)
	hs3, err := hc.Tick() // 冷却过，持续计数中
	if err != nil {
		t.Fatalf("ratio tick3: %v", err)
	}
	floatEQ(t, "ratio tick3 still frozen", hs3.Admission[heIdx][CriticalPlus], 0.4)
	hs4, err := hc.Tick() // tick4：冷却与持续同时满足，首次步长回升 +0.25
	if err != nil {
		t.Fatalf("ratio tick4: %v", err)
	}
	floatEQ(t, "ratio tick4 step up", hs4.Admission[heIdx][CriticalPlus], 0.65)
	for _, snap := range []*Snapshot{hs1, hs2, hs3, hs4} {
		for v, nd := range hg.Nodes {
			if snap.Load[v] > nd.Cap+1e-6 {
				t.Fatalf("ratio tick %d overload at %s %.3f>%.3f", snap.Tick, nd.ID, snap.Load[v], nd.Cap)
			}
		}
	}
}

// 健全性：控制器在容量再次骤降时即时重新切断，不受恢复冷却拖累（保护动作优先于滞回）。
func TestControllerImmediateReprotect(t *testing.T) {
	nodes := []Node{
		{ID: "E", Cap: 1000, Arrival: [NumPriorities]float64{100, 0, 0, 0}},
		{ID: "S", Cap: 1000},
	}
	edges := []Edge{{From: "E", To: "S", Fanout: 1, Dep: Soft, DegradeCost: 5}}
	g, err := NewGraph(nodes, edges)
	if err != nil {
		t.Fatalf("NewGraph: %v", err)
	}
	c := NewController(g, DefaultHysteresisConfig())
	sIdx := nodePtr(g, "S")

	g.Nodes[sIdx].Cap = 50
	first, err := c.Tick()
	if err != nil {
		t.Fatalf("first tick: %v", err)
	}
	if len(first.CutEdges) != 1 {
		t.Fatalf("expected immediate cut, got %v", first.CutEdges)
	}
	// 同一 tick 周期内容量进一步骤降，保护动作仍须即时生效（这里再 tick 一次确认持续切断）。
	g.Nodes[sIdx].Cap = 10
	second, err := c.Tick()
	if err != nil {
		t.Fatalf("second tick: %v", err)
	}
	if len(second.CutEdges) != 1 {
		t.Fatalf("expected edge to stay cut under deeper drop, got %v", second.EdgeScale)
	}
	if second.Load[sIdx] > g.Nodes[sIdx].Cap+1e-6 {
		t.Fatalf("re-protect failed, S load %.3f > cap %.3f", second.Load[sIdx], g.Nodes[sIdx].Cap)
	}
}

// 健全性：多入口共享下游的守恒在控制器快照中同样成立。
func TestControllerSnapshotConservation(t *testing.T) {
	nodes := []Node{
		{ID: "A", Cap: 1000, Arrival: [NumPriorities]float64{10, 0, 0, 0}},
		{ID: "B", Cap: 1000, Arrival: [NumPriorities]float64{20, 0, 0, 0}},
		{ID: "D", Cap: 1000},
	}
	edges := []Edge{
		{From: "A", To: "D", Fanout: 2, Dep: Hard},
		{From: "B", To: "D", Fanout: 3, Dep: Hard},
	}
	g, err := NewGraph(nodes, edges)
	if err != nil {
		t.Fatalf("NewGraph: %v", err)
	}
	c := NewController(g, DefaultHysteresisConfig())
	snap, err := c.Tick()
	if err != nil {
		t.Fatalf("tick: %v", err)
	}
	d := nodePtr(g, "D")
	// D = 10*2 + 20*3 = 80，多入口共享路径精确合并。
	floatEQ(t, "shared downstream load", snap.Load[d], 80)
}
