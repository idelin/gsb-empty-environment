package main

import (
	"fmt"
	"sort"
)

// solution 是全局最优解：保留集合与其总权重。
type solution struct {
	keptMask   uint32
	keptWeight int64
}

// Solve 在「唯一键互斥 + 指定冲突互斥 + 外键蕴含」三类约束下，
// 对所有行做统一的分支定界搜索，直接求全局最优（保留权重最大 <=> 删除权重最小）。
// 它不会按约束分别求解再合并——约束在同一搜索空间内联合生效。
func Solve(m *model) (solution, error) {
	n := len(m.rowRef)

	// 每个外键需求的候选位掩码，以及按子行/候选行索引的需求表，用于增量剪枝。
	candMask := make([]uint32, len(m.reqs))
	reqsByChild := make(map[int][]int)
	reqsByCand := make(map[int][]int)
	for ri, req := range m.reqs {
		for _, c := range req.candidates {
			candMask[ri] |= 1 << uint(c)
			reqsByCand[c] = append(reqsByCand[c], ri)
		}
		reqsByChild[req.child] = append(reqsByChild[req.child], ri)
	}

	// 悬空外键行：只能删除（修复器只允许删行），预先固定。
	var fixedDeleted uint32
	for i := 0; i < n; i++ {
		if m.dangling[i] {
			fixedDeleted |= 1 << uint(i)
		}
	}

	// 决策顺序：权重大的行先决策，上界更紧。
	order := make([]int, n)
	for i := range order {
		order[i] = i
	}
	sort.Slice(order, func(a, b int) bool {
		if m.weight[order[a]] != m.weight[order[b]] {
			return m.weight[order[a]] > m.weight[order[b]]
		}
		return order[a] < order[b]
	})

	// remaining[p] = 尚未决策且未固定删除的行的权重后缀和，作为乐观上界。
	remaining := make([]int64, n+1)
	for p := n - 1; p >= 0; p-- {
		i := order[p]
		remaining[p] = remaining[p+1]
		if fixedDeleted&(1<<uint(i)) == 0 {
			remaining[p] += m.weight[i]
		}
	}

	best := solution{keptWeight: -1}

	var rec func(pos int, kept, deleted uint32, keptW int64)
	rec = func(pos int, kept, deleted uint32, keptW int64) {
		if best.keptWeight >= 0 && keptW+remaining[pos] <= best.keptWeight {
			return // 上界不超过已知最优，剪枝
		}
		if pos == n {
			if keptW > best.keptWeight {
				best = solution{keptMask: kept, keptWeight: keptW}
			}
			return
		}
		i := order[pos]
		bit := uint32(1) << uint(i)
		if fixedDeleted&bit != 0 {
			rec(pos+1, kept, deleted|bit, keptW)
			return
		}

		// 分支一：保留 i。要求不与已保留行互斥，且其外键候选未全部删除。
		if kept&m.mutex[i] == 0 {
			ok := true
			for _, ri := range reqsByChild[i] {
				if candMask[ri]&^deleted == 0 {
					ok = false
					break
				}
			}
			if ok {
				rec(pos+1, kept|bit, deleted, keptW+m.weight[i])
			}
		}

		// 分支二：删除 i。要求任何已保留子行的候选集不被删空。
		{
			ok := true
			for _, ri := range reqsByCand[i] {
				if kept&(1<<uint(m.reqs[ri].child)) == 0 {
					continue
				}
				if candMask[ri]&^(deleted|bit) == 0 {
					ok = false
					break
				}
			}
			if ok {
				rec(pos+1, kept, deleted|bit, keptW)
			}
		}
	}
	rec(0, 0, fixedDeleted, 0)

	if best.keptWeight < 0 {
		return solution{}, fmt.Errorf("无可行解（约束本身矛盾，例如悬空外键行被迫保留）")
	}
	return best, nil
}
