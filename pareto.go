package paretopath

import "sort"

// pathLess 按字典序比较两条顶点序列。
// 由于所有路径共享同一源点，顶点序列的字典序与边序列的字典序一致。
func pathLess(a, b []int) bool {
	for i := 0; i < len(a) && i < len(b); i++ {
		if a[i] != b[i] {
			return a[i] < b[i]
		}
	}
	return len(a) < len(b)
}

// dominates 判断标签 a 是否支配标签 b：
// 时间与费用均不劣，且至少一项严格更优；
// 若两项指标完全相等，则要求 a 的边序列字典序不大于 b。
func dominates(a, b Label) bool {
	if a.Time > b.Time || a.Cost > b.Cost {
		return false
	}
	if a.Time == b.Time && a.Cost == b.Cost {
		return !pathLess(b.Path, a.Path)
	}
	return true
}

// nodeLabel 是算法内部使用的标签，附带已访问顶点 bitmask。
type nodeLabel struct {
	Label
	mask uint16
}

// subsetOf 判断 a 的已访问集合是否为 b 的子集。
func subsetOf(a, b uint16) bool { return a&^b == 0 }

// insert 尝试把 nl 加入 bucket（某顶点的非支配标签集合）。
// 若 nl 被支配则丢弃并返回 false；否则插入并移除被 nl 支配的旧标签，返回 true。
//
// 为保证简单路径约束下的正确性，支配剪枝要求支配方的已访问集合
// 是被支配方的子集：此时被支配方的任何简单后续扩展，支配方都可照搬，
// 且扩展后的指标同样不劣，故剪枝不会丢失任何 Pareto 标签。
func insert(bucket *[]nodeLabel, nl nodeLabel) bool {
	for _, ex := range *bucket {
		if subsetOf(ex.mask, nl.mask) && dominates(ex.Label, nl.Label) {
			return false
		}
	}
	kept := (*bucket)[:0]
	for _, ex := range *bucket {
		if subsetOf(nl.mask, ex.mask) && dominates(nl.Label, ex.Label) {
			continue
		}
		kept = append(kept, ex)
	}
	*bucket = append(kept, nl)
	return true
}

// ParetoPaths 返回从 s 到 t 的全部 Pareto 最优简单路径标签，
// 按 (Time, Cost) 升序排列。指标相同的标签只保留字典序最小的边序列。
// s == t 时返回仅含零指标平凡路径的单元素切片。
func (g *Graph) ParetoPaths(s, t int) []Label {
	g.checkVertex(s)
	g.checkVertex(t)
	if s == t {
		return []Label{{Time: 0, Cost: 0, Path: []int{s}}}
	}
	buckets := make([][]nodeLabel, g.N)
	seed := nodeLabel{Label: Label{Time: 0, Cost: 0, Path: []int{s}}, mask: 1 << uint(s)}
	buckets[s] = []nodeLabel{seed}

	var extend func(l nodeLabel)
	extend = func(l nodeLabel) {
		u := l.Path[len(l.Path)-1]
		if u == t {
			return // 到达目标后不再扩展，保证路径简单
		}
		for _, e := range g.Adj[u] {
			bit := uint16(1) << uint(e.To)
			if l.mask&bit != 0 {
				continue // 不重复顶点
			}
			path := make([]int, len(l.Path)+1)
			copy(path, l.Path)
			path[len(l.Path)] = e.To
			nl := nodeLabel{
				Label: Label{Time: l.Time + e.Time, Cost: l.Cost + e.Cost, Path: path},
				mask:  l.mask | bit,
			}
			if insert(&buckets[e.To], nl) {
				extend(nl)
			}
		}
	}
	extend(seed)

	// 中间顶点的剪枝带有 visited 子集条件，目标点处可能仍残留
	// 被支配标签，最后做一次完整过滤。
	labels := make([]Label, len(buckets[t]))
	for i, nl := range buckets[t] {
		labels[i] = nl.Label
	}
	return ParetoFilter(labels)
}

// ParetoFilter 从标签集合中滤出非支配标签：
// 指标完全相同的只保留字典序最小的边序列，其余被支配标签全部移除。
// 结果按 (Time, Cost) 升序排列。
func ParetoFilter(labels []Label) []Label {
	best := make(map[[2]int64]Label, len(labels))
	for _, l := range labels {
		key := [2]int64{l.Time, l.Cost}
		if cur, ok := best[key]; !ok || pathLess(l.Path, cur.Path) {
			best[key] = l
		}
	}
	uniq := make([]Label, 0, len(best))
	for _, l := range best {
		uniq = append(uniq, l)
	}
	res := make([]Label, 0, len(uniq))
outer:
	for _, a := range uniq {
		for _, b := range uniq {
			if b.Time <= a.Time && b.Cost <= a.Cost && (b.Time < a.Time || b.Cost < a.Cost) {
				continue outer // a 被 b 严格支配
			}
		}
		res = append(res, a)
	}
	sort.Slice(res, func(i, j int) bool {
		if res[i].Time != res[j].Time {
			return res[i].Time < res[j].Time
		}
		return res[i].Cost < res[j].Cost
	})
	return res
}

// SimplePathLabels 枚举从 s 到 t 的全部简单路径标签（不做任何支配过滤），
// 用于穷举核对。s == t 时返回零指标平凡路径。
func (g *Graph) SimplePathLabels(s, t int) []Label {
	g.checkVertex(s)
	g.checkVertex(t)
	if s == t {
		return []Label{{Time: 0, Cost: 0, Path: []int{s}}}
	}
	var res []Label
	visited := make([]bool, g.N)
	visited[s] = true
	var dfs func(u int, time, cost int64, path []int)
	dfs = func(u int, time, cost int64, path []int) {
		if u == t {
			cp := make([]int, len(path))
			copy(cp, path)
			res = append(res, Label{Time: time, Cost: cost, Path: cp})
			return
		}
		for _, e := range g.Adj[u] {
			if visited[e.To] {
				continue
			}
			visited[e.To] = true
			dfs(e.To, time+e.Time, cost+e.Cost, append(path, e.To))
			visited[e.To] = false
		}
	}
	dfs(s, 0, 0, []int{s})
	return res
}
