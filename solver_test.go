package main

import (
	"math"
	"testing"
)

func cut(from, to, artifact string, cost float64) Edge {
	return Edge{From: from, To: to, Cuttable: true, Artifact: artifact, Cost: cost}
}

func hard(from, to string) Edge {
	return Edge{From: from, To: to}
}

func verifyPlan(t *testing.T, spec GraphSpec, p *Plan) {
	t.Helper()
	// 切边必须全部来自已授权的可切边，且产物与成本一致。
	allowed := make(map[[2]string]Edge)
	for _, e := range spec.Edges {
		if e.Cuttable {
			allowed[[2]string{e.From, e.To}] = e
		}
	}
	var sum float64
	for _, c := range p.Cuts {
		e, ok := allowed[[2]string{c.From, c.To}]
		if !ok {
			t.Fatalf("方案切断了未授权的边 %s -> %s", c.From, c.To)
		}
		if e.Artifact != c.Artifact {
			t.Fatalf("边 %s -> %s 使用了错误的产物 %q（应为 %q）", c.From, c.To, c.Artifact, e.Artifact)
		}
		sum += c.Cost
	}
	if math.Abs(sum-p.TotalCost) > 1e-9 {
		t.Fatalf("总成本 %v 与切边成本之和 %v 不符", p.TotalCost, sum)
	}
	// 拓扑计划必须覆盖全部节点且满足所有保留边。
	pos := make(map[string]int)
	stageOf := make(map[string]int)
	for i, stage := range p.Stages {
		for j, n := range stage {
			if _, dup := pos[n]; dup {
				t.Fatalf("节点 %s 在计划中出现多次", n)
			}
			pos[n] = j
			stageOf[n] = i
		}
	}
	all := make(map[string]bool)
	for _, n := range spec.Nodes {
		all[n] = true
	}
	for _, e := range spec.Edges {
		all[e.From] = true
		all[e.To] = true
	}
	for n := range all {
		if _, ok := pos[n]; !ok {
			t.Fatalf("节点 %s 未出现在拓扑计划中", n)
		}
	}
	cutSet := make(map[[2]string]bool)
	for _, c := range p.Cuts {
		cutSet[[2]string{c.From, c.To}] = true
	}
	for _, e := range spec.Edges {
		if cutSet[[2]string{e.From, e.To}] {
			continue
		}
		if stageOf[e.From] >= stageOf[e.To] {
			t.Fatalf("保留边 %s -> %s 违反拓扑序", e.From, e.To)
		}
	}
}

func TestSimpleCycle(t *testing.T) {
	spec := GraphSpec{Edges: []Edge{
		hard("a", "b"),
		cut("b", "c", "boot-c", 5),
		cut("c", "a", "boot-a", 2),
	}}
	p, err := Solve(spec)
	if err != nil {
		t.Fatal(err)
	}
	if p.TotalCost != 2 || len(p.Cuts) != 1 || p.Cuts[0].Artifact != "boot-a" {
		t.Fatalf("应切断最便宜的边 c->a，得到 %+v", p)
	}
	verifyPlan(t, spec, p)
}

func TestInfeasibleUncuttableCycle(t *testing.T) {
	spec := GraphSpec{Edges: []Edge{
		hard("a", "b"),
		hard("b", "a"),
		cut("b", "c", "boot-c", 1),
	}}
	if _, err := Solve(spec); err == nil {
		t.Fatal("不可切边成环应当无解")
	}
}

func TestMustCutMultiple(t *testing.T) {
	// 两个环共享一条贵边：切共享边一次即可同时破两环。
	spec := GraphSpec{Edges: []Edge{
		cut("a", "b", "boot-b", 3),
		cut("b", "a", "boot-a", 3),
		cut("a", "c", "boot-c", 3),
		cut("c", "a", "shared", 4),
		cut("c", "b", "boot-b2", 3),
	}}
	p, err := Solve(spec)
	if err != nil {
		t.Fatal(err)
	}
	verifyPlan(t, spec, p)
	// 最优：切 a->b 与 a->c（或等价组合），成本 6；穷举校验最优性。
	if got, want := p.TotalCost, bruteForce(spec); got != want {
		t.Fatalf("成本 %v，穷举最优 %v", got, want)
	}
}

func TestSelfLoop(t *testing.T) {
	spec := GraphSpec{Edges: []Edge{
		cut("a", "a", "boot-a", 7),
		hard("a", "b"),
	}}
	p, err := Solve(spec)
	if err != nil {
		t.Fatal(err)
	}
	if p.TotalCost != 7 || len(p.Cuts) != 1 {
		t.Fatalf("自环必须被切断: %+v", p)
	}
	verifyPlan(t, spec, p)
}

func TestTooManyCuttable(t *testing.T) {
	var spec GraphSpec
	for i := 0; i < 26; i++ {
		spec.Edges = append(spec.Edges, cut(string(rune('a'+i)), "z", "x", 1))
	}
	if _, err := Solve(spec); err == nil {
		t.Fatal("超过 25 条可切边应报错")
	}
}

func TestAcyclicNoCuts(t *testing.T) {
	spec := GraphSpec{Edges: []Edge{
		hard("a", "b"),
		cut("a", "c", "boot-c", 1),
	}}
	p, err := Solve(spec)
	if err != nil {
		t.Fatal(err)
	}
	if p.TotalCost != 0 || len(p.Cuts) != 0 {
		t.Fatalf("无环图不应有切边: %+v", p)
	}
	verifyPlan(t, spec, p)
}

func TestRandomAgainstBruteForce(t *testing.T) {
	// 确定性伪随机生成若干小图，与穷举法对比最优成本。
	seed := int64(42)
	rand := func(n int) int {
		seed = (seed*6364136223846793005 + 1442695040888963407) >> 33
		if seed < 0 {
			seed = -seed
		}
		return int(seed % int64(n))
	}
	for trial := 0; trial < 60; trial++ {
		n := 3 + rand(4)
		names := make([]string, n)
		for i := range names {
			names[i] = string(rune('a' + i))
		}
		var spec GraphSpec
		ncut := 0
		for i := 0; i < n; i++ {
			for j := 0; j < n; j++ {
				if i == j || rand(3) > 0 {
					continue
				}
				if rand(2) == 0 && ncut < 12 {
					spec.Edges = append(spec.Edges, cut(names[i], names[j], "boot-"+names[i]+names[j], float64(1+rand(9))))
					ncut++
				} else {
					spec.Edges = append(spec.Edges, hard(names[i], names[j]))
				}
			}
		}
		p, err := Solve(spec)
		want := bruteForce(spec)
		if math.IsInf(want, 1) {
			if err == nil {
				t.Fatalf("trial %d: 应无解却得到 %+v", trial, p)
			}
			continue
		}
		if err != nil {
			t.Fatalf("trial %d: 应有解却报错 %v", trial, err)
		}
		if p.TotalCost != want {
			t.Fatalf("trial %d: 成本 %v，穷举最优 %v", trial, p.TotalCost, want)
		}
		verifyPlan(t, spec, p)
	}
}

// bruteForce 枚举所有可切边子集，返回最小可行成本；不可行为 +Inf。
func bruteForce(spec GraphSpec) float64 {
	s, err := newSolver(spec)
	if err != nil {
		return 0
	}
	best := math.Inf(1)
	k := len(s.cutIdx)
	for mask := uint32(0); ; mask++ {
		if len(s.findCycle(mask)) == 0 {
			cost := 0.0
			for i, ei := range s.cutIdx {
				if mask&(1<<uint(i)) == 0 {
					cost += s.edges[ei].Cost
				}
			}
			if cost < best {
				best = cost
			}
		}
		if mask == uint32(1)<<uint(k)-1 {
			break
		}
	}
	return best
}
