package solver

import (
	"math/big"
	"testing"
)

func rat(s string) *Rat {
	r, ok := new(big.Rat).SetString(s)
	if !ok {
		panic("bad rat " + s)
	}
	return r
}

func build(worlds []World, actions []string, losses map[string]map[string]string, caps []Capacity) *Problem {
	p := &Problem{Worlds: worlds, Actions: actions, Loss: make(map[string]map[string]*Rat), Capacities: caps}
	for wid, row := range losses {
		p.Loss[wid] = make(map[string]*Rat)
		for a, s := range row {
			p.Loss[wid][a] = rat(s)
		}
	}
	return p
}

// 不可预知规则：w1、w2 共享信号 s1，求解器必须给二者同一动作。
// 手算：全部策略最坏损失均为 10，最优期望损失 5/2 由 {s1:a, s2:a} 取得。
func TestNonAnticipation(t *testing.T) {
	p := build(
		[]World{
			{ID: "w1", Prob: rat("1/2"), Signal: "s1"},
			{ID: "w2", Prob: rat("1/4"), Signal: "s1"},
			{ID: "w3", Prob: rat("1/4"), Signal: "s2"},
		},
		[]string{"a", "b"},
		map[string]map[string]string{
			"w1": {"a": "0", "b": "10"},
			"w2": {"a": "10", "b": "0"},
			"w3": {"a": "0", "b": "10"},
		}, nil)
	res, err := p.Enumerate()
	if err != nil {
		t.Fatal(err)
	}
	if res.Enumerated != 4 {
		t.Fatalf("enumerated %d strategies, want 4", res.Enumerated)
	}
	if res.Best.Strategy["s1"] != "a" || res.Best.Strategy["s2"] != "a" {
		t.Fatalf("best = %v, want {s1:a, s2:a}", res.Best.Strategy)
	}
	if res.Best.Worst.Cmp(rat("10")) != 0 {
		t.Fatalf("worst = %s, want 10", res.Best.Worst)
	}
	if res.Best.Average.Cmp(rat("5/2")) != 0 {
		t.Fatalf("average = %s, want 5/2", res.Best.Average)
	}
}

// 字典序目标：动作 b 期望损失更低但最坏损失更高，必须选 a。
func TestLexicographicObjective(t *testing.T) {
	p := build(
		[]World{
			{ID: "w1", Prob: rat("1/2"), Signal: "s"},
			{ID: "w2", Prob: rat("1/2"), Signal: "s"},
		},
		[]string{"a", "b"},
		map[string]map[string]string{
			"w1": {"a": "3", "b": "4"},
			"w2": {"a": "3", "b": "0"},
		}, nil)
	res, err := p.Enumerate()
	if err != nil {
		t.Fatal(err)
	}
	if res.Best.Strategy["s"] != "a" {
		t.Fatalf("best = %v, want action a (worst 3 < 4 despite higher average)", res.Best.Strategy)
	}
	if res.Best.Worst.Cmp(rat("3")) != 0 || res.Best.Average.Cmp(rat("3")) != 0 {
		t.Fatalf("got worst=%s average=%s, want 3/3", res.Best.Worst, res.Best.Average)
	}
}

// 共享容量：动作 a 的期望用量不得超过 1/2。
// 含 s1:a 的策略用量 >= 3/4 被排除；最优退化为 {s1:b, s2:a}。
func TestSharedCapacity(t *testing.T) {
	cap := Capacity{
		Name:  "capA",
		Limit: rat("1/2"),
		Weight: map[string]map[string]*Rat{
			"w1": {"a": rat("1")},
			"w2": {"a": rat("1")},
			"w3": {"a": rat("1")},
		},
	}
	p := build(
		[]World{
			{ID: "w1", Prob: rat("1/2"), Signal: "s1"},
			{ID: "w2", Prob: rat("1/4"), Signal: "s1"},
			{ID: "w3", Prob: rat("1/4"), Signal: "s2"},
		},
		[]string{"a", "b"},
		map[string]map[string]string{
			"w1": {"a": "0", "b": "10"},
			"w2": {"a": "10", "b": "0"},
			"w3": {"a": "0", "b": "10"},
		}, []Capacity{cap})
	res, err := p.Enumerate()
	if err != nil {
		t.Fatal(err)
	}
	if res.Feasible != 2 {
		t.Fatalf("feasible = %d, want 2", res.Feasible)
	}
	if res.Best.Strategy["s1"] != "b" || res.Best.Strategy["s2"] != "a" {
		t.Fatalf("best = %v, want {s1:b, s2:a}", res.Best.Strategy)
	}
	if res.Best.Average.Cmp(rat("5")) != 0 {
		t.Fatalf("average = %s, want 5", res.Best.Average)
	}
	// 显式核对一个违规策略。
	ev, err := p.Evaluate(Strategy{"s1": "a", "s2": "a"})
	if err != nil {
		t.Fatal(err)
	}
	if ev.Feasible {
		t.Fatal("expected capacity violation for {s1:a, s2:a}")
	}
	want := `capacity "capA": used 1 > limit 1/2`
	if ev.Violation != want {
		t.Fatalf("violation = %q, want %q", ev.Violation, want)
	}
}

// 概率为 1/3 等分数时仍精确求和为 1，且期望损失精确。
func TestRationalExactness(t *testing.T) {
	p := build(
		[]World{
			{ID: "w1", Prob: rat("1/3"), Signal: "s"},
			{ID: "w2", Prob: rat("1/3"), Signal: "s"},
			{ID: "w3", Prob: rat("1/3"), Signal: "s"},
		},
		[]string{"a"},
		map[string]map[string]string{
			"w1": {"a": "1/6"},
			"w2": {"a": "1/3"},
			"w3": {"a": "1/2"},
		}, nil)
	res, err := p.Enumerate()
	if err != nil {
		t.Fatal(err)
	}
	// (1/6 + 1/3 + 1/2) / 3 = 1/3
	if res.Best.Average.Cmp(rat("1/3")) != 0 {
		t.Fatalf("average = %s, want 1/3", res.Best.Average)
	}
	if res.Best.Worst.Cmp(rat("1/2")) != 0 {
		t.Fatalf("worst = %s, want 1/2", res.Best.Worst)
	}
}

// 无可行策略时应报错。
func TestInfeasible(t *testing.T) {
	cap := Capacity{
		Name:  "capA",
		Limit: rat("0"),
		Weight: map[string]map[string]*Rat{
			"w1": {"a": rat("1"), "b": rat("1")},
		},
	}
	p := build(
		[]World{{ID: "w1", Prob: rat("1"), Signal: "s"}},
		[]string{"a", "b"},
		map[string]map[string]string{"w1": {"a": "1", "b": "1"}},
		[]Capacity{cap})
	if _, err := p.Enumerate(); err == nil {
		t.Fatal("expected infeasibility error")
	}
}
