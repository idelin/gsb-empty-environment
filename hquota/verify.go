package hquota

import (
	"fmt"
)

// Violation 描述一条公平性/可行性违例。
type Violation struct {
	Leaf   int    // 相关叶编号；节点级违例时为 -1
	Node   string // 相关节点 ID；叶级违例时为空
	Reason string
}

func (v Violation) String() string {
	if v.Leaf >= 0 {
		return fmt.Sprintf("leaf %d: %s", v.Leaf, v.Reason)
	}
	return fmt.Sprintf("node %q: %s", v.Node, v.Reason)
}

// Verify 核验最近一次 Allocate 的结果，返回全部违例（空切片表示通过）。
//
// 判定标准：
//  1. 可行性与瓶颈公平准则：同 VerifyAllocation；
//  2. 严格不浪费：任一未满足叶必有某个祖先恰好饱和（子树合计 == cap），
//     即不存在可用容量被闲置的情况。
func (a *Allocator) Verify() []Violation {
	out := a.tree.VerifyAllocation(a.alloc)
	out = append(out, a.tree.checkMaximal(a.alloc)...)
	return out
}

// VerifyAllocation 独立核验任意一份整数分配（不依赖分配器内部状态）。
//
// 判定标准（瓶颈准则，含整数取整容差）：对任一未满足的叶 i，
// 必存在某个近似饱和的祖先节点 n（子树合计与 cap 的差距小于子树叶数——
// 向下取整固有的空隙），且 n 子树内不存在“明显更占便宜”的叶 j——
// 即 j 未达需求且 alloc[j]*w[i] > (alloc[i]+1)*w[j] + w[i]。
// 不等式右端的 +1 是取整与零头轮转引入的每叶最多 1 单位容差。
func (t *Tree) VerifyAllocation(alloc map[int]int64) []Violation {
	var out []Violation
	out = append(out, t.checkFeasible(alloc)...)

	sums := make(map[*Node]int64, len(t.nodes))
	for _, lf := range t.order {
		for n := lf.Parent; n != nil; n = n.Parent {
			sums[n] += alloc[lf.ID]
		}
	}
	for _, lf := range t.order {
		ai := alloc[lf.ID]
		if ai >= lf.Demand {
			continue
		}
		saturated := false
		fair := false
		for n := lf.Parent; n != nil; n = n.Parent {
			// 近似饱和：空隙小于子树叶数（取整固有损耗上限）。
			if n.Cap-sums[n] >= int64(len(t.leavesUnder(n))) {
				continue
			}
			saturated = true
			bottleneckOK := true
			for _, other := range t.leavesUnder(n) {
				aj := alloc[other.ID]
				if aj >= other.Demand {
					continue // j 受自身需求限制，不参与比较
				}
				if aj*lf.Weight > (ai+1)*other.Weight+lf.Weight {
					bottleneckOK = false
					break
				}
			}
			if bottleneckOK {
				fair = true
				break
			}
		}
		switch {
		case !saturated:
			out = append(out, Violation{Leaf: lf.ID, Reason: fmt.Sprintf("unsatisfied (alloc %d < demand %d) but no saturated ancestor; capacity wasted", ai, lf.Demand)})
		case !fair:
			out = append(out, Violation{Leaf: lf.ID, Reason: fmt.Sprintf("unsatisfied (alloc %d < demand %d) and every saturated ancestor contains a leaf with disproportionately larger share per weight", ai, lf.Demand)})
		}
	}
	return out
}

// checkFeasible 检查叶需求与节点上限约束。
func (t *Tree) checkFeasible(alloc map[int]int64) []Violation {
	var out []Violation
	sums := make(map[*Node]int64, len(t.nodes))
	for _, lf := range t.order {
		a := alloc[lf.ID]
		if a < 0 {
			out = append(out, Violation{Leaf: lf.ID, Reason: fmt.Sprintf("negative allocation %d", a)})
		}
		if a > lf.Demand {
			out = append(out, Violation{Leaf: lf.ID, Reason: fmt.Sprintf("allocation %d exceeds demand %d", a, lf.Demand)})
		}
		for n := lf.Parent; n != nil; n = n.Parent {
			sums[n] += a
		}
	}
	for n, sum := range sums {
		if sum > n.Cap {
			out = append(out, Violation{Leaf: -1, Node: n.ID, Reason: fmt.Sprintf("subtree sum %d exceeds cap %d", sum, n.Cap)})
		}
	}
	return out
}

// checkMaximal 检查不存在“叶未满足但沿途各节点都有剩余容量”的浪费。
func (t *Tree) checkMaximal(alloc map[int]int64) []Violation {
	var out []Violation
	sums := make(map[*Node]int64, len(t.nodes))
	for _, lf := range t.order {
		for n := lf.Parent; n != nil; n = n.Parent {
			sums[n] += alloc[lf.ID]
		}
	}
	for _, lf := range t.order {
		if alloc[lf.ID] >= lf.Demand {
			continue
		}
		blocked := false
		for n := lf.Parent; n != nil; n = n.Parent {
			if sums[n] >= n.Cap {
				blocked = true
				break
			}
		}
		if !blocked {
			out = append(out, Violation{Leaf: lf.ID, Reason: fmt.Sprintf("unsatisfied (alloc %d < demand %d) while every ancestor has spare capacity", alloc[lf.ID], lf.Demand)})
		}
	}
	return out
}
