package main

import "sort"

// ID 支持字符串或整数两种 JSON 形式；排序时整数排在字符串之前。
type ID struct {
	isNum bool
	num   int64
	str   string
}

func (a ID) less(b ID) bool {
	if a.isNum != b.isNum {
		return a.isNum
	}
	if a.isNum {
		return a.num < b.num
	}
	return a.str < b.str
}

func (a ID) equal(b ID) bool {
	return a.isNum == b.isNum && a.num == b.num && a.str == b.str
}

type offer struct {
	id   ID
	a, b int64 // 裁剪到 need 范围内的半开区间 [a, b)
	cost int64
}

// state 为某一覆盖目标的当前最优解：先比总费用，再比窗口数，再比升序 ID 序列字典序。
type state struct {
	ok   bool
	cost int64
	ids  []ID // 升序
}

func lessState(x, y state) bool {
	if x.cost != y.cost {
		return x.cost < y.cost
	}
	if len(x.ids) != len(y.ids) {
		return len(x.ids) < len(y.ids)
	}
	for i := range x.ids {
		if !x.ids[i].equal(y.ids[i]) {
			return x.ids[i].less(y.ids[i])
		}
	}
	return false
}

func insertSorted(ids []ID, id ID) []ID {
	i := sort.Search(len(ids), func(i int) bool { return !ids[i].less(id) })
	out := make([]ID, 0, len(ids)+1)
	out = append(out, ids[:i]...)
	out = append(out, id)
	out = append(out, ids[i:]...)
	return out
}

// solve 求覆盖 [needL, needR) 的最省窗口组合。
// 坐标压缩后扫描线 DP：dp[j] 为覆盖 [needL, xs[j]) 的最优解，
// 对每个满足 a < xs[j] <= b 的窗口 o，用 dp[idx(o.a)] + o 转移。
// 最优性：取最优解中覆盖 xs[j) 左侧邻近点的窗口 o，o 不覆盖 [needL, o.a)
// 内任何点，故其余窗口必然完整覆盖 [needL, o.a)，可用归纳。
func solve(needL, needR int64, offers []offer) state {
	if needL >= needR {
		return state{ok: true, ids: []ID{}}
	}
	coordSet := map[int64]struct{}{needL: {}, needR: {}}
	var clipped []offer
	for _, o := range offers {
		a := max(o.a, needL)
		b := min(o.b, needR)
		if a >= b {
			continue
		}
		clipped = append(clipped, offer{id: o.id, a: a, b: b, cost: o.cost})
		coordSet[a] = struct{}{}
		coordSet[b] = struct{}{}
	}
	xs := make([]int64, 0, len(coordSet))
	for x := range coordSet {
		xs = append(xs, x)
	}
	sort.Slice(xs, func(i, j int) bool { return xs[i] < xs[j] })
	idx := make(map[int64]int, len(xs))
	for i, x := range xs {
		idx[x] = i
	}
	dp := make([]state, len(xs))
	dp[0] = state{ok: true, ids: []ID{}}
	for j := 1; j < len(xs); j++ {
		best := state{}
		for _, o := range clipped {
			ia, ib := idx[o.a], idx[o.b]
			if ia >= j || ib < j {
				continue
			}
			pre := dp[ia]
			if !pre.ok {
				continue
			}
			cand := state{ok: true, cost: pre.cost + o.cost, ids: insertSorted(pre.ids, o.id)}
			if !best.ok || lessState(cand, best) {
				best = cand
			}
		}
		dp[j] = best
	}
	return dp[len(xs)-1]
}
