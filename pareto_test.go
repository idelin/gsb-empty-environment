package paretopath

import (
	"math/rand"
	"reflect"
	"sort"
	"testing"
)

func bruteForcePareto(g *Graph, s, t int) []Label {
	return ParetoFilter(g.SimplePathLabels(s, t))
}

func assertParetoEqual(t *testing.T, g *Graph, s, tgt int) []Label {
	t.Helper()
	got := g.ParetoPaths(s, tgt)
	want := bruteForcePareto(g, s, tgt)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("s=%d t=%d\n got: %v\nwant: %v", s, tgt, got, want)
	}
	return got
}

// 两条路径指标完全相同，只保留字典序最小的边序列。
func TestLexicographicTieBreak(t *testing.T) {
	g := NewGraph(4)
	g.AddEdge(0, 1, 1, 1)
	g.AddEdge(0, 2, 1, 1)
	g.AddEdge(1, 3, 1, 1)
	g.AddEdge(2, 3, 1, 1)
	got := assertParetoEqual(t, g, 0, 3)
	if len(got) != 1 {
		t.Fatalf("期望 1 个标签，得到 %v", got)
	}
	if !reflect.DeepEqual(got[0].Path, []int{0, 1, 3}) {
		t.Fatalf("期望字典序最小路径 [0 1 3]，得到 %v", got[0].Path)
	}
}

// Pareto 前沿含多个标签，不能退化为单一加权和最优：
// 中间标签 (5,7) 对任何非负权重 lambda*time+cost 都不是唯一最优，
// 但仍属于非支配解，必须输出。
func TestParetoFrontNotSingleWeightedSum(t *testing.T) {
	g := NewGraph(4)
	g.AddEdge(0, 1, 1, 10)
	g.AddEdge(1, 3, 0, 0)
	g.AddEdge(0, 3, 5, 7)
	g.AddEdge(0, 2, 10, 1)
	g.AddEdge(2, 3, 0, 0)
	got := assertParetoEqual(t, g, 0, 3)
	want := []Label{
		{Time: 1, Cost: 10, Path: []int{0, 1, 3}},
		{Time: 5, Cost: 7, Path: []int{0, 3}},
		{Time: 10, Cost: 1, Path: []int{0, 2, 3}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf(" got: %v\nwant: %v", got, want)
	}
}

// 简单路径约束：含环图上不得重复顶点。
func TestSimplePathConstraint(t *testing.T) {
	g := NewGraph(4)
	g.AddEdge(0, 1, 1, 5)
	g.AddEdge(1, 2, 1, 1)
	g.AddEdge(2, 1, 1, 1) // 环 1<->2
	g.AddEdge(2, 3, 1, 1)
	g.AddEdge(0, 3, 10, 0)
	got := assertParetoEqual(t, g, 0, 3)
	for _, l := range got {
		seen := make(map[int]bool)
		for _, v := range l.Path {
			if seen[v] {
				t.Fatalf("路径含重复顶点: %v", l.Path)
			}
			seen[v] = true
		}
	}
}

func TestSourceEqualsTarget(t *testing.T) {
	g := NewGraph(3)
	g.AddEdge(0, 1, 1, 1)
	g.AddEdge(1, 0, 1, 1)
	got := g.ParetoPaths(1, 1)
	want := []Label{{Time: 0, Cost: 0, Path: []int{1}}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf(" got: %v\nwant: %v", got, want)
	}
}

func TestUnreachable(t *testing.T) {
	g := NewGraph(3)
	g.AddEdge(0, 1, 1, 1)
	if got := g.ParetoPaths(0, 2); len(got) != 0 {
		t.Fatalf("期望空结果，得到 %v", got)
	}
}

// 零指标边：支配判定与去重在退化指标下仍须正确。
func TestZeroMetricEdges(t *testing.T) {
	g := NewGraph(4)
	g.AddEdge(0, 1, 0, 0)
	g.AddEdge(1, 2, 0, 0)
	g.AddEdge(0, 2, 0, 0)
	g.AddEdge(2, 3, 2, 3)
	got := assertParetoEqual(t, g, 0, 3)
	if len(got) != 1 || !reflect.DeepEqual(got[0].Path, []int{0, 1, 2, 3}) {
		t.Fatalf("期望字典序最小的零指标路径 [0 1 2 3]，得到 %v", got)
	}
}

// 随机图交叉核对：标签校正算法 vs 简单路径穷举。
func TestFuzzAgainstBruteForce(t *testing.T) {
	rng := rand.New(rand.NewSource(20261006))
	for iter := 0; iter < 400; iter++ {
		n := 2 + rng.Intn(9) // 2..10 个顶点
		g := NewGraph(n)
		for u := 0; u < n; u++ {
			for v := 0; v < n; v++ {
				if u != v && rng.Float64() < 0.35 {
					g.AddEdge(u, v, int64(rng.Intn(10)), int64(rng.Intn(10)))
				}
			}
		}
		s, tt := rng.Intn(n), rng.Intn(n)
		assertParetoEqual(t, g, s, tt)
	}
}

// 随机核对结果的有序性与非支配性。
func TestFuzzResultProperties(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	for iter := 0; iter < 200; iter++ {
		n := 2 + rng.Intn(9)
		g := NewGraph(n)
		for u := 0; u < n; u++ {
			for v := 0; v < n; v++ {
				if u != v && rng.Float64() < 0.4 {
					g.AddEdge(u, v, int64(rng.Intn(6)), int64(rng.Intn(6)))
				}
			}
		}
		s, tt := rng.Intn(n), rng.Intn(n)
		got := g.ParetoPaths(s, tt)
		if !sort.SliceIsSorted(got, func(i, j int) bool {
			return got[i].Time < got[j].Time || (got[i].Time == got[j].Time && got[i].Cost < got[j].Cost)
		}) {
			t.Fatalf("结果未按 (Time, Cost) 升序: %v", got)
		}
		for i, a := range got {
			for j, b := range got {
				if i != j && dominates(b, a) {
					t.Fatalf("标签 %v 被 %v 支配", a, b)
				}
			}
		}
	}
}
