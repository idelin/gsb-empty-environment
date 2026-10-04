package main

import (
	"fmt"
	"math/rand"
	"testing"
)

func requests(group string, ids ...string) []Event {
	evs := make([]Event, 0, len(ids))
	for _, id := range ids {
		evs = append(evs, Event{Type: "request", Group: group, ID: id})
	}
	return evs
}

func grantIDs(rr RoundResult) []string {
	ids := make([]string, 0, len(rr.Grants))
	for _, g := range rr.Grants {
		ids = append(ids, g.ID)
	}
	return ids
}

func countByGroup(rr RoundResult) map[string]int {
	m := map[string]int{}
	for _, g := range rr.Grants {
		m[g.Group]++
	}
	return m
}

func runScenario(t *testing.T, sc Scenario) []RoundResult {
	t.Helper()
	sim, err := NewSimulator(sc)
	if err != nil {
		t.Fatalf("NewSimulator: %v", err)
	}
	out := sim.Run(sc)
	if rep := Verify(sc, out); !rep.OK() {
		t.Fatalf("invariants violated: %v", rep.Violations)
	}
	return out
}

// 保底：三组保底之和等于容量时，各组每轮恰好拿到保底额度，无空闲可借。
func TestMinGuaranteeExactFit(t *testing.T) {
	sc := Scenario{
		Capacity: 10,
		Groups:   map[string]int{"A": 4, "B": 4, "C": 2},
		Rounds: []Round{
			{Events: append(append(requests("A", "a1", "a2", "a3", "a4", "a5"),
				requests("B", "b1", "b2", "b3", "b4", "b5")...),
				requests("C", "c1", "c2", "c3")...)},
		},
	}
	out := runScenario(t, sc)
	got := countByGroup(out[0])
	want := map[string]int{"A": 4, "B": 4, "C": 2}
	for g, w := range want {
		if got[g] != w {
			t.Errorf("group %s: got %d grants, want %d", g, got[g], w)
		}
	}
	if len(out[0].Grants) != 10 {
		t.Errorf("total grants = %d, want 10", len(out[0].Grants))
	}
}

// 借出与收回：A 独占时借满全部容量；B、C 恢复活跃后下一轮收回保底，
// A 的借额相应减少；收回只影响后续分配，已发出的许可不撤回。
func TestLendAndTakeBack(t *testing.T) {
	sc := Scenario{
		Capacity: 10,
		Groups:   map[string]int{"A": 4, "B": 4, "C": 2},
		Rounds: []Round{
			{Events: requests("A", "a1", "a2", "a3", "a4", "a5", "a6", "a7", "a8", "a9", "a10")},
			{Events: append(requests("A", "a11", "a12", "a13", "a14", "a15", "a16", "a17", "a18", "a19", "a20"),
				requests("B", "b1", "b2", "b3", "b4", "b5", "b6")...)},
			{Events: requests("C", "c1", "c2", "c3")},
		},
	}
	out := runScenario(t, sc)

	// 第 1 轮：仅 A 活跃，借得 B、C 的空闲份额，拿满 10。
	r1 := countByGroup(out[0])
	if r1["A"] != 10 {
		t.Errorf("round 1: A got %d, want 10 (4 own + 6 borrowed)", r1["A"])
	}
	borrowed := 0
	for _, g := range out[0].Grants {
		if g.Borrowed {
			borrowed++
		}
	}
	if borrowed != 6 {
		t.Errorf("round 1: borrowed grants = %d, want 6", borrowed)
	}

	// 第 2 轮：B 恢复活跃，收回保底 4；A 的借额从 6 降到 1（仅剩 C 的 2 份空闲，A、B 各借 1）。
	r2 := countByGroup(out[1])
	if r2["B"] != 5 || r2["A"] != 5 {
		t.Errorf("round 2: got %v, want A=5 B=5", r2)
	}
	if r2["B"] < 4 {
		t.Errorf("round 2: B got %d, below guaranteed 4", r2["B"])
	}

	// 第 3 轮：C 也活跃。A 余 5、B 余 1、C 需 3：保底 A=4,B=1,C=2，余 3 中 A、C 各借 1，
	// 最后 1 份无需求可借，本轮总量 9 < 容量 10。
	r3 := countByGroup(out[2])
	if r3["A"] != 5 || r3["B"] != 1 || r3["C"] != 3 {
		t.Errorf("round 3: got %v, want A=5 B=1 C=3", r3)
	}
	if r3["C"] < 2 {
		t.Errorf("round 3: C got %d, below guaranteed 2", r3["C"])
	}

	// 不撤回：收回借额只减少后续分配，各轮许可序列只增不改。
	if len(out[0].Grants) != 10 {
		t.Errorf("round 1 grants changed retroactively: %d, want 10", len(out[0].Grants))
	}
}

// 取消：分配前取消的请求不获许可；已发出的许可不可取消。
func TestCancel(t *testing.T) {
	sc := Scenario{
		Capacity: 2,
		Groups:   map[string]int{"A": 1},
		Rounds: []Round{
			{Events: []Event{
				{Type: "request", Group: "A", ID: "a1"},
				{Type: "request", Group: "A", ID: "a2"},
				{Type: "cancel", ID: "a1"},
			}},
			{Events: []Event{{Type: "cancel", ID: "a2"}}}, // a2 已获批，取消无效
		},
	}
	out := runScenario(t, sc)
	ids := grantIDs(out[0])
	if len(ids) != 1 || ids[0] != "a2" {
		t.Errorf("round 1 grants = %v, want [a2]", ids)
	}
	if len(out[1].Grants) != 0 {
		t.Errorf("round 2 grants = %v, want none", grantIDs(out[1]))
	}
}

// 组内先来先服务：容量不足时跨轮按到达顺序批准。
func TestGroupFIFO(t *testing.T) {
	sc := Scenario{
		Capacity: 2,
		Groups:   map[string]int{"A": 1, "B": 1},
		Rounds: []Round{
			{Events: requests("A", "a1", "a2", "a3")},
			{Events: requests("A", "a4")},
			{},
		},
	}
	out := runScenario(t, sc)
	var got []string
	for _, rr := range out {
		for _, g := range rr.Grants {
			if g.Group == "A" {
				got = append(got, g.ID)
			}
		}
	}
	want := []string{"a1", "a2", "a3", "a4"}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("A grant order = %v, want %v", got, want)
	}
}

// 配置校验：保底总和超容量、未知组、重复 id 均应报错。
func TestValidate(t *testing.T) {
	cases := []Scenario{
		{Capacity: 3, Groups: map[string]int{"A": 2, "B": 2}},
		{Capacity: 3, Groups: map[string]int{"A": 1}, Rounds: []Round{
			{Events: []Event{{Type: "request", Group: "X", ID: "x1"}}},
		}},
		{Capacity: 3, Groups: map[string]int{"A": 1}, Rounds: []Round{
			{Events: []Event{
				{Type: "request", Group: "A", ID: "a1"},
				{Type: "request", Group: "A", ID: "a1"},
			}},
		}},
	}
	for i, sc := range cases {
		if _, err := NewSimulator(sc); err == nil {
			t.Errorf("case %d: expected validation error, got nil", i)
		}
	}
}

// 随机场景：任意请求/取消序列下，三条不变量必须始终成立。
func TestRandomScenarios(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	for trial := 0; trial < 200; trial++ {
		groups := map[string]int{"A": rng.Intn(4), "B": rng.Intn(4), "C": rng.Intn(3)}
		sum := 0
		for _, m := range groups {
			sum += m
		}
		sc := Scenario{
			Capacity: sum + rng.Intn(6),
			Groups:   groups,
		}
		names := []string{"A", "B", "C"}
		var issued []string
		for r := 0; r < 20; r++ {
			var evs []Event
			for _, id := range issued {
				if rng.Intn(4) == 0 {
					evs = append(evs, Event{Type: "cancel", ID: id})
				}
			}
			for k := 0; k < rng.Intn(8); k++ {
				id := fmt.Sprintf("t%d-r%d-%d", trial, r, k)
				evs = append(evs, Event{Type: "request", Group: names[rng.Intn(3)], ID: id})
				issued = append(issued, id)
			}
			sc.Rounds = append(sc.Rounds, Round{Events: evs})
		}
		sim, err := NewSimulator(sc)
		if err != nil {
			t.Fatalf("trial %d: %v", trial, err)
		}
		out := sim.Run(sc)
		if rep := Verify(sc, out); !rep.OK() {
			t.Fatalf("trial %d: invariants violated: %v", trial, rep.Violations)
		}
	}
}
