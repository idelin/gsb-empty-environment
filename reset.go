package netsec

import (
	"errors"
	"fmt"
	"math/bits"
)

// MaxBlocks 是精确枚举所支持的功能块数量上限。
const MaxBlocks = 20

// Block 描述一个共享功能块。
type Block struct {
	Name string
	Cost int64 // 复位该块的代价，必须非负
	// Tainted 表示该块持有旧租户遗留下来的可观察状态（污染源）。
	Tainted bool
	// Observable 表示该块的状态对新租户可见（观察汇）。
	Observable bool
}

// Edge 是一条共享资源边：状态可沿 From 流向 To。
type Edge struct {
	From, To int
}

// Spec 是重置规划问题的完整输入。
type Spec struct {
	Blocks []Block
	Edges  []Edge
	// Pre[i] 列出在复位块 i 之前必须已经完成复位的块（复位前置条件）。
	Pre [][]int
}

// Plan 是一次精确求解得到的复位计划。
type Plan struct {
	// Cut 是切断全部可观察状态路径的核心集合（升序，不含仅由前置条件引入的块）。
	Cut []int
	// Set 是实际要复位的完整集合：Cut 在前置条件闭包下的扩展（升序）。
	Set []int
	// Order 是满足全部前置条件的复位执行顺序，覆盖 Set 中的每个块。
	Order []int
	// Cost 是 Set 中所有块的复位成本之和，为全局最小值。
	Cost int64
}

// PlanReset 精确求解最低成本复位集合并生成执行顺序。
//
// 复位集合 S 必须满足：在共享资源边构成的有向图上，删除 S 后不存在
// 从任一 Tainted 块到任一 Observable 块的路径。S 同时必须在复位前置
// 条件下封闭且可拓扑排序。块数不超过 MaxBlocks，采用子集枚举精确求解。
func PlanReset(spec Spec) (Plan, error) {
	n := len(spec.Blocks)
	if n > MaxBlocks {
		return Plan{}, fmt.Errorf("netsec: %d blocks exceed limit %d", n, MaxBlocks)
	}
	if n == 0 {
		return Plan{}, nil
	}
	for i, b := range spec.Blocks {
		if b.Cost < 0 {
			return Plan{}, fmt.Errorf("netsec: block %d (%q) has negative cost %d", i, b.Name, b.Cost)
		}
	}

	adj := make([]uint64, n)
	for _, e := range spec.Edges {
		if e.From < 0 || e.From >= n || e.To < 0 || e.To >= n {
			return Plan{}, fmt.Errorf("netsec: edge (%d,%d) out of range [0,%d)", e.From, e.To, n)
		}
		adj[e.From] |= 1 << uint(e.To)
	}

	// 前置条件的自反传递闭包（位掩码，Warshall）。
	pre := make([]uint64, n)
	for i := 0; i < n; i++ {
		pre[i] = 1 << uint(i)
		for _, p := range spec.Pre[i] {
			if p < 0 || p >= n {
				return Plan{}, fmt.Errorf("netsec: precondition %d of block %d out of range [0,%d)", p, i, n)
			}
			pre[i] |= 1 << uint(p)
		}
	}
	for k := 0; k < n; k++ {
		for i := 0; i < n; i++ {
			if pre[i]>>uint(k)&1 == 1 {
				pre[i] |= pre[k]
			}
		}
	}
	// 前置条件成环的块永远无法按序复位，包含它们的闭包均不可行。
	var cyclic uint64
	for i := 0; i < n; i++ {
		for j := i + 1; j < n; j++ {
			if pre[i]>>uint(j)&1 == 1 && pre[j]>>uint(i)&1 == 1 {
				cyclic |= 1<<uint(i) | 1<<uint(j)
			}
		}
	}

	var tainted, observable uint64
	for i, b := range spec.Blocks {
		if b.Tainted {
			tainted |= 1 << uint(i)
		}
		if b.Observable {
			observable |= 1 << uint(i)
		}
	}

	// 只有位于某条 污染源→观察汇 路径上的块才可能参与割集。
	radj := make([]uint64, n)
	for _, e := range spec.Edges {
		radj[e.To] |= 1 << uint(e.From)
	}
	relevant := reach(adj, tainted, 0) & reach(radj, observable, 0) &^ cyclic

	costOf := func(set uint64) int64 {
		var c int64
		for s := set; s != 0; s &= s - 1 {
			c += spec.Blocks[bits.TrailingZeros64(s)].Cost
		}
		return c
	}

	bestCost := int64(-1)
	var bestSet, bestCut uint64
	for sub := relevant; ; sub = (sub - 1) & relevant {
		var closure uint64
		for s := sub; s != 0; s &= s - 1 {
			closure |= pre[bits.TrailingZeros64(s)]
		}
		if closure&cyclic == 0 && reach(adj, tainted, closure)&observable == 0 {
			cost := costOf(closure)
			if bestCost < 0 || cost < bestCost ||
				(cost == bestCost && lessSet(closure, bestSet)) {
				bestCost, bestSet, bestCut = cost, closure, sub
			}
		}
		if sub == 0 {
			break
		}
	}
	if bestCost < 0 {
		return Plan{}, errors.New("netsec: no feasible reset plan cuts all observable state paths")
	}

	order, err := topoOrder(bestSet, spec.Pre, n)
	if err != nil {
		return Plan{}, err
	}
	return Plan{
		Cut:   maskToSlice(bestCut),
		Set:   maskToSlice(bestSet),
		Order: order,
		Cost:  bestCost,
	}, nil
}

// reach 返回从 seeds 出发、不经过 blocked 块可达的全部块（含 seeds）。
func reach(adj []uint64, seeds, blocked uint64) uint64 {
	seen := seeds &^ blocked
	frontier := seen
	for frontier != 0 {
		var next uint64
		for f := frontier; f != 0; f &= f - 1 {
			next |= adj[bits.TrailingZeros64(f)]
		}
		next &^= seen | blocked
		seen |= next
		frontier = next
	}
	return seen
}

// lessSet 在成本相同的情况下做确定性决胜：块数更少者优先，
// 其次按下标字典序（位掩码数值小者优先）。
func lessSet(a, b uint64) bool {
	if pa, pb := bits.OnesCount64(a), bits.OnesCount64(b); pa != pb {
		return pa < pb
	}
	return a < b
}

// topoOrder 对 set 中的块按前置条件做拓扑排序，同层按下标升序保证确定性。
func topoOrder(set uint64, pre [][]int, n int) ([]int, error) {
	indeg := make([]int, n)
	for i := 0; i < n; i++ {
		if set>>uint(i)&1 == 0 {
			continue
		}
		for _, p := range pre[i] {
			if set>>uint(p)&1 == 1 {
				indeg[i]++
			}
		}
	}
	remaining := set
	order := make([]int, 0, bits.OnesCount64(set))
	for remaining != 0 {
		picked := -1
		for i := 0; i < n; i++ {
			if remaining>>uint(i)&1 == 1 && indeg[i] == 0 {
				picked = i
				break
			}
		}
		if picked < 0 {
			return nil, errors.New("netsec: reset preconditions contain a cycle")
		}
		order = append(order, picked)
		remaining &^= 1 << uint(picked)
		for i := 0; i < n; i++ {
			if remaining>>uint(i)&1 == 0 {
				continue
			}
			for _, p := range pre[i] {
				if p == picked {
					indeg[i]--
				}
			}
		}
	}
	return order, nil
}

func maskToSlice(mask uint64) []int {
	out := make([]int, 0, bits.OnesCount64(mask))
	for m := mask; m != 0; m &= m - 1 {
		out = append(out, bits.TrailingZeros64(m))
	}
	return out
}
