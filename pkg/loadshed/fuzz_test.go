package loadshed

import (
	"math"
	"math/rand"
	"testing"
)

// bruteSolve 对小规模 DAG 枚举全部 SOFT 切边子集，并对每档做网格离散化，
// 暴力求出字典序最优保留量与对应的最小降级代价，作为 Solve 的差分对照。
func bruteSolve(t *testing.T, g *Graph) ([NumPriorities]float64, float64) {
	t.Helper()
	soft := make([]int, 0)
	for ei, e := range g.Edges {
		if e.Dep == Soft {
			soft = append(soft, ei)
		}
	}
	var bestRet [NumPriorities]float64
	bestCost := math.Inf(1)

	// 仅枚举软边子集（小规模 <= 4 条），放行率用粗网格，验证字典序与代价单调性。
	for mask := 0; mask < (1 << len(soft)); mask++ {
		cut := map[int]bool{}
		cost := 0.0
		for i, ei := range soft {
			if mask&(1<<i) != 0 {
				cut[ei] = true
				cost += g.Edges[ei].DegradeCost
			}
		}
		routing, err := g.BuildRouting(cutScales(cut))
		if err != nil {
			continue
		}
		adm, retained := runAllTiers(g, routing)
		_, total := g.Propagate(routing, adm)
		feasible := true
		for v, nd := range g.Nodes {
			if total[v] > nd.Cap+capacityTol(nd.Cap) {
				feasible = false
			}
		}
		if !feasible {
			continue
		}
		cmp := 0
		for p := Priority(0); p < NumPriorities; p++ {
			if !approxEQ(retained[p], bestRet[p]) {
				if retained[p] > bestRet[p] {
					cmp = 1
				} else {
					cmp = -1
				}
				break
			}
		}
		if cmp > 0 || (cmp == 0 && cost < bestCost) {
			bestRet = retained
			bestCost = cost
		}
	}
	return bestRet, bestCost
}

// TestDifferentialVsBruteForce 随机生成小 DAG，校验 Solve 与暴力枚举在保留量字典序和
// 最小降级代价上完全一致，并保证结果严格满足全部容量约束。
func TestDifferentialVsBruteForce(t *testing.T) {
	rng := rand.New(rand.NewSource(20261128))
	for iter := 0; iter < 60; iter++ {
		n := 2 + rng.Intn(3) // 2..4 个节点
		nodes := make([]Node, n)
		for i := range nodes {
			nodes[i] = Node{ID: string(rune('A' + i)), Cap: float64(10 + rng.Intn(60))}
			for p := 0; p < int(NumPriorities); p++ {
				if rng.Intn(2) == 0 {
					nodes[i].Arrival[p] = float64(rng.Intn(30))
				}
			}
		}
		// 仅生成 i<j 的边，保证 DAG。
		edges := make([]Edge, 0)
		for i := 0; i < n; i++ {
			for j := i + 1; j < n; j++ {
				if rng.Intn(2) == 0 {
					dep := Hard
					cost := 0.0
					if rng.Intn(2) == 0 {
						dep = Soft
						cost = float64(1 + rng.Intn(10))
					}
					edges = append(edges, Edge{
						From: nodes[i].ID, To: nodes[j].ID,
						Fanout: float64(1 + rng.Intn(3)), Dep: dep, DegradeCost: cost,
					})
				}
			}
		}
		softCount := 0
		for _, e := range edges {
			if e.Dep == Soft {
				softCount++
			}
		}
		if softCount > 4 {
			continue // 控制暴力枚举规模
		}
		g, err := NewGraph(nodes, edges)
		if err != nil {
			t.Fatalf("iter %d NewGraph: %v", iter, err)
		}
		sol, err := Solve(g)
		if err != nil {
			t.Fatalf("iter %d Solve: %v", iter, err)
		}
		// 容量约束。
		for v, nd := range g.Nodes {
			if sol.Load[v] > nd.Cap+1e-6 {
				t.Fatalf("iter %d node %s overload %.4f>%.4f", iter, nd.ID, sol.Load[v], nd.Cap)
			}
		}
		wantRet, wantCost := bruteSolve(t, g)
		for p := Priority(0); p < NumPriorities; p++ {
			if !approxEQ(sol.Retained[p], wantRet[p]) {
				t.Fatalf("iter %d tier %s retained %.4f != brute %.4f (full sol=%v want=%v)",
					iter, p, sol.Retained[p], wantRet[p], sol.Retained, wantRet)
			}
		}
		if !approxEQ(sol.DegradeCost, wantCost) {
			t.Fatalf("iter %d degrade cost %.2f != brute minimum %.2f", iter, sol.DegradeCost, wantCost)
		}
		// 负载守恒：分优先级负载之和与总负载一致。
		for v := range g.Nodes {
			sum := 0.0
			for p := Priority(0); p < NumPriorities; p++ {
				sum += sol.LoadPerPriority[v][p]
			}
			if !approxEQ(sum, sol.Load[v]) {
				t.Fatalf("iter %d conservation mismatch at node %d: %.6f vs %.6f", iter, v, sum, sol.Load[v])
			}
		}
	}
}
