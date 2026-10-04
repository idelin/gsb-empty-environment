package main

import "testing"

func TestBasicRoute(t *testing.T) {
	in := &Input{
		Capacity: 10, Start: "A", Goal: "C",
		Edges: []Edge{
			{From: "A", To: "B", Time: 3, Energy: 4},
			{From: "B", To: "C", Time: 2, Energy: 5},
			{From: "A", To: "C", Time: 9, Energy: 8},
		},
	}
	res := solve(in)
	if !res.Reachable {
		t.Fatal("应可达")
	}
	if res.Arrival != 5 || res.Cost != 0 {
		t.Fatalf("got arrival=%d cost=%d, want 5/0", res.Arrival, res.Cost)
	}
}

func TestSwapWithWaitForOpen(t *testing.T) {
	// 4 时刻到达 B，换电站 8 点开门，需等待后换满电才能走完。
	in := &Input{
		Capacity: 10, Start: "S", Goal: "G",
		Edges: []Edge{
			{From: "S", To: "B", Time: 4, Energy: 6},
			{From: "B", To: "G", Time: 3, Energy: 10},
		},
		Stations: map[string]Station{
			"B": {Fee: 5, Windows: []Window{{Open: 8, Close: 20}}},
		},
	}
	res := solve(in)
	if !res.Reachable {
		t.Fatal("应可达")
	}
	if res.Arrival != 11 || res.Cost != 5 {
		t.Fatalf("got arrival=%d cost=%d, want 11/5", res.Arrival, res.Cost)
	}
	foundWait := false
	for _, s := range res.Steps {
		if contains(s, "等待至时间 8") {
			foundWait = true
		}
	}
	if !foundWait {
		t.Fatalf("行程中应包含等待开门动作: %v", res.Steps)
	}
}

func TestStationClosedUnreachable(t *testing.T) {
	// 换电站 8-10 点营业，到达时已过营业时间且次日也来不及？次日可等——
	// 这里构造完全无站可用：电量不够且无换电站。
	in := &Input{
		Capacity: 5, Start: "S", Goal: "G",
		Edges: []Edge{{From: "S", To: "G", Time: 1, Energy: 6}},
	}
	if res := solve(in); res.Reachable {
		t.Fatal("应不可达")
	}
}

func TestWaitUntilNextDay(t *testing.T) {
	// 到达时当日窗口已过，等待至次日开门。
	in := &Input{
		Capacity: 10, Start: "S", Goal: "G",
		Edges: []Edge{
			{From: "S", To: "B", Time: 21, Energy: 6},
			{From: "B", To: "G", Time: 1, Energy: 10},
		},
		Stations: map[string]Station{
			"B": {Fee: 2, Windows: []Window{{Open: 8, Close: 20}}},
		},
	}
	res := solve(in)
	if !res.Reachable {
		t.Fatal("应可达")
	}
	// 21 到达 B，次日 8 点(=32)换电，33 到达。
	if res.Arrival != 33 || res.Cost != 2 {
		t.Fatalf("got arrival=%d cost=%d, want 33/2", res.Arrival, res.Cost)
	}
}

func TestZeroEnergyCycleTerminates(t *testing.T) {
	// A<->B 零耗电零耗时环 + A<->D 零耗电有耗时环，不能导致无限搜索。
	in := &Input{
		Capacity: 4, Start: "A", Goal: "G",
		Edges: []Edge{
			{From: "A", To: "B", Time: 0, Energy: 0},
			{From: "B", To: "A", Time: 0, Energy: 0},
			{From: "A", To: "D", Time: 1, Energy: 0},
			{From: "D", To: "A", Time: 1, Energy: 0},
			{From: "B", To: "G", Time: 2, Energy: 3},
		},
	}
	res := solve(in)
	if !res.Reachable {
		t.Fatal("应可达")
	}
	if res.Arrival != 2 || res.Cost != 0 {
		t.Fatalf("got arrival=%d cost=%d, want 2/0", res.Arrival, res.Cost)
	}
}

func TestZeroCostSwapCycleTerminates(t *testing.T) {
	// 免费换电站 + 零耗电环：换电不产生改进，必须终止。
	in := &Input{
		Capacity: 6, Start: "A", Goal: "G",
		Edges: []Edge{
			{From: "A", To: "B", Time: 0, Energy: 0},
			{From: "B", To: "A", Time: 0, Energy: 0},
			{From: "A", To: "G", Time: 1, Energy: 5},
		},
		Stations: map[string]Station{
			"A": {Fee: 0, Windows: []Window{{Open: 0, Close: 24}}},
			"B": {Fee: 0, Windows: []Window{{Open: 0, Close: 24}}},
		},
	}
	res := solve(in)
	if !res.Reachable || res.Arrival != 1 {
		t.Fatalf("got %+v", res)
	}
}

func TestEarliestArrivalThenLowestCost(t *testing.T) {
	// 快但贵：S->Y(2,5) 换电(费9) Y->G(2,10)，到达 t=4 费 9
	// 慢但免费：S->G(20,5)，到达 t=20 费 0
	// 最早到达优先 -> 选 t=4 费 9。
	in := &Input{
		Capacity: 10, Start: "S", Goal: "G",
		Edges: []Edge{
			{From: "S", To: "G", Time: 20, Energy: 5},
			{From: "S", To: "Y", Time: 2, Energy: 5},
			{From: "Y", To: "G", Time: 2, Energy: 10},
		},
		Stations: map[string]Station{
			"Y": {Fee: 9, Windows: []Window{{Open: 0, Close: 24}}},
		},
	}
	res := solve(in)
	if res.Arrival != 4 || res.Cost != 9 {
		t.Fatalf("最早到达优先失败: got arrival=%d cost=%d, want 4/9", res.Arrival, res.Cost)
	}
}

func TestSameArrivalTimePicksCheaper(t *testing.T) {
	// 两条路同为 t=10 到达：直达免费 vs 换电费 3，应选免费。
	in := &Input{
		Capacity: 10, Start: "S", Goal: "G",
		Edges: []Edge{
			{From: "S", To: "G", Time: 10, Energy: 5},
			{From: "S", To: "X", Time: 4, Energy: 5},
			{From: "X", To: "G", Time: 6, Energy: 10},
		},
		Stations: map[string]Station{
			"X": {Fee: 3, Windows: []Window{{Open: 0, Close: 24}}},
		},
	}
	res := solve(in)
	if res.Arrival != 10 || res.Cost != 0 {
		t.Fatalf("同刻应选最低费用: got arrival=%d cost=%d, want 10/0", res.Arrival, res.Cost)
	}
}

func TestFrontierKeepsParetoLabels(t *testing.T) {
	// 支配必须同时看时间与费用：(5,10) 与 (6,2) 互不支配，都应保留。
	p := newPlanner(&Input{Capacity: 10})
	p.record("N", 5, 5, 10)
	p.record("N", 5, 6, 2)
	if p.dominated("N", 5, 6, 1) {
		t.Fatal("(6,1) 不应被 (5,10) 或 (6,2) 支配")
	}
	if !p.dominated("N", 5, 7, 3) {
		t.Fatal("(7,3) 应被 (6,2) 支配")
	}
	// 高电量标签可支配低电量标签。
	p.record("N", 8, 4, 1)
	if !p.dominated("N", 5, 5, 5) {
		t.Fatal("电量 8 的 (4,1) 应支配电量 5 的 (5,5)")
	}
	if p.dominated("N", 9, 5, 5) {
		t.Fatal("电量 9 不应被电量 8 的标签支配")
	}
}

func TestCheapLateLabelSurvivesAndWins(t *testing.T) {
	// 到 M 有两条标签：早而贵(换电) 与 晚而免费。
	// 若支配只看时间，晚标签会被误删，导致终点费用非最优。
	in := &Input{
		Capacity: 10, Start: "S", Goal: "G",
		Edges: []Edge{
			// 支路1：S->M 快但耗电，到 M 必须付费换电才能走 M->G
			{From: "S", To: "M", Time: 1, Energy: 9},
			// 支路2：S->M 慢但省电，到 M 余电充足
			{From: "S", To: "M", Time: 3, Energy: 1},
			{From: "M", To: "G", Time: 1, Energy: 9},
		},
		Stations: map[string]Station{
			"M": {Fee: 7, Windows: []Window{{Open: 0, Close: 24}}},
		},
	}
	res := solve(in)
	// 支路1：t=1 到 M 余 1，换电费 7，t=2 到 G；支路2：t=3 到 M 余 9，t=4 到 G 免费。
	// 最早到达是支路1：t=2 费 7。
	if res.Arrival != 2 || res.Cost != 7 {
		t.Fatalf("got arrival=%d cost=%d, want 2/7", res.Arrival, res.Cost)
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
