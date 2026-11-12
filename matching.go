package netsec

import "errors"

// ErrInvalidEvent 在事件流包含未知事件类型或越界数据时返回。
var ErrInvalidEvent = errors.New("netsec: invalid event in stream")

// graph 是以 begin 为左部、end 为右部的相容二分图。
//
// beginID/endID 是压缩后的连续节点号；begins/ends 保存它们在原始
// 事件切片中的下标。adj[u] 是 begin 节点 u 可匹配的 end 节点号列表。
type graph struct {
	begins  []int
	ends    []int
	events  []Event
	adj     [][]int
	beginOf map[int]int
	endOf   map[int]int
}

// Analyze 判断事件流中是否存在合法的注入匹配。
//
// 匹配规则：每个 end 必须匹配一个同时满足以下条件的 begin：
//   - 主体、会话随机数、操作摘要三个字段完全相同；
//   - 在事件流中早于该 end（下标更小）；
//   - 未被其他 end 使用（一对一，杜绝同一 begin 被重复消费）。
//
// 允许存在并发会话（不同会话随机数互不干扰）与部分未结束的 begin。
// 当合法完整匹配不存在时，报告中附带 Hall 型缺口证据。
func Analyze(events []Event) (*Report, error) {
	for i := range events {
		if !validKind(events[i].Kind) {
			return nil, ErrInvalidEvent
		}
	}

	g := buildGraph(events)

	// pairEnd[v] 是当前与 end 节点 v 配对的 begin 节点号，-1 表示未配对。
	pairEnd := make([]int, len(g.ends))
	for v := range pairEnd {
		pairEnd[v] = -1
	}
	// pairBegin[u] 是当前与 begin 节点 u 配对的 end 节点号，-1 表示未配对。
	pairBegin := make([]int, len(g.begins))
	for u := range pairBegin {
		pairBegin[u] = -1
	}

	// Kuhn 增广路算法：逐个 end 寻找增广路。
	visited := make([]bool, len(g.begins))
	var augment func(beginNode int) bool
	augment = func(u int) bool {
		if visited[u] {
			return false
		}
		visited[u] = true
		for _, v := range g.adj[u] {
			if pairEnd[v] == -1 || augment(pairEnd[v]) {
				pairEnd[v] = u
				pairBegin[u] = v
				return true
			}
		}
		return false
	}

	for u := range g.begins {
		clear(visited)
		augment(u)
	}

	matchedBegins := make(map[int]bool, len(g.begins))
	pairs := make([]Pair, 0, len(g.ends))
	for v, u := range pairEnd {
		if u == -1 {
			// 存在未覆盖的 end：提取 Hall 型缺口证据。
			witness := hallWitness(g, pairBegin, pairEnd)
			return &Report{Feasible: false, Witness: witness}, nil
		}
		matchedBegins[u] = true
		pairs = append(pairs, Pair{Begin: g.begins[u], End: g.ends[v]})
	}

	unmatched := make([]int, 0, len(g.begins)-len(matchedBegins))
	for u, eventIdx := range g.begins {
		if !matchedBegins[u] {
			unmatched = append(unmatched, eventIdx)
		}
	}

	// 按 end 在事件流中的顺序稳定输出，便于宿主比对。
	sortPairsByEnd(pairs)
	return &Report{
		Feasible:        true,
		Matching:        pairs,
		UnmatchedBegins: unmatched,
	}, nil
}

// hallWitness 在最大匹配未覆盖全部 end 时构造 Hall 缺口。
//
// 取一个未匹配 end 作为起点，在“end→相邻 begin→该 begin 的匹配 end”
// 的交替路上做 BFS。由 Hall 定理的标准证明，可达 end 集合 S 的邻居
// 全部处于已匹配状态且数量为 |S|-1，即 |N(S)| < |S|。
func hallWitness(g *graph, pairBegin, pairEnd []int) *HallWitness {
	rootEnd := -1
	for v, u := range pairEnd {
		if u == -1 {
			rootEnd = v
			break
		}
	}

	seenEnd := map[int]bool{rootEnd: true}
	seenBegin := make(map[int]bool)
	queue := []int{rootEnd}

	for len(queue) > 0 {
		v := queue[0]
		queue = queue[1:]
		for _, u := range g.endNeighbors(v) {
			if seenBegin[u] {
				continue
			}
			seenBegin[u] = true
			// 残量边上每个可达 begin 都已匹配；沿匹配边回到其 end。
			if next := pairBegin[u]; next != -1 && !seenEnd[next] {
				seenEnd[next] = true
				queue = append(queue, next)
			}
		}
	}

	endSet := make([]int, 0, len(seenEnd))
	for v := range seenEnd {
		endSet = append(endSet, g.ends[v])
	}
	neighborSet := make([]int, 0, len(seenBegin))
	for u := range seenBegin {
		neighborSet = append(neighborSet, g.begins[u])
	}
	sortInts(endSet)
	sortInts(neighborSet)

	return &HallWitness{Ends: endSet, NeighborBegins: neighborSet}
}

// buildGraph 依据字段相容性与时序构造相容二分图。
func buildGraph(events []Event) *graph {
	g := &graph{
		events:  events,
		beginOf: make(map[int]int),
		endOf:   make(map[int]int),
	}
	for i, ev := range events {
		switch ev.Kind {
		case KindBegin:
			g.beginOf[i] = len(g.begins)
			g.begins = append(g.begins, i)
		case KindEnd:
			g.endOf[i] = len(g.ends)
			g.ends = append(g.ends, i)
		}
	}

	g.adj = make([][]int, len(g.begins))
	for u, beginIdx := range g.begins {
		beginEvent := events[beginIdx]
		for v, endIdx := range g.ends {
			if beginIdx < endIdx && compatible(beginEvent, events[endIdx]) {
				g.adj[u] = append(g.adj[u], v)
			}
		}
	}
	return g
}

// endNeighbors 返回与 end 节点 v 相容的所有 begin 节点号。
func (g *graph) endNeighbors(v int) []int {
	neighbors := make([]int, 0)
	for u, ends := range g.adj {
		for _, cand := range ends {
			if cand == v {
				neighbors = append(neighbors, u)
				break
			}
		}
	}
	return neighbors
}

// ValidMatching 独立校验宿主给出的配对集合是否满足全部匹配约束，
// 不要求覆盖全部 end（完整匹配可用 IsComplete 判断）。
func ValidMatching(events []Event, matching []Pair) bool {
	usedBegin := make(map[int]bool, len(matching))
	usedEnd := make(map[int]bool, len(matching))
	for _, p := range matching {
		if p.Begin < 0 || p.Begin >= len(events) || p.End < 0 || p.End >= len(events) {
			return false
		}
		b, e := events[p.Begin], events[p.End]
		if b.Kind != KindBegin || e.Kind != KindEnd {
			return false
		}
		if p.Begin >= p.End || !compatible(b, e) {
			return false
		}
		if usedBegin[p.Begin] || usedEnd[p.End] {
			return false
		}
		usedBegin[p.Begin] = true
		usedEnd[p.End] = true
	}
	return true
}

// IsComplete 报告合法 matching 是否覆盖了事件流中的全部 end。
func IsComplete(events []Event, matching []Pair) bool {
	endCount := 0
	for _, ev := range events {
		if ev.Kind == KindEnd {
			endCount++
		}
	}
	return len(matching) == endCount
}

// ValidWitness 独立校验 Hall 缺口证据：邻居必须恰好覆盖 Ends 中每个
// end 的所有相容 begin，且严格满足 |N(S)| < |S|。
func ValidWitness(events []Event, w *HallWitness) bool {
	if w == nil || len(w.Ends) == 0 {
		return false
	}
	neighbors := make(map[int]bool)
	for _, endIdx := range w.Ends {
		if endIdx < 0 || endIdx >= len(events) || events[endIdx].Kind != KindEnd {
			return false
		}
		for beginIdx, ev := range events {
			if ev.Kind == KindBegin && beginIdx < endIdx &&
				compatible(ev, events[endIdx]) {
				neighbors[beginIdx] = true
			}
		}
	}
	if len(neighbors) != len(w.NeighborBegins) {
		return false
	}
	for _, beginIdx := range w.NeighborBegins {
		if !neighbors[beginIdx] {
			return false
		}
	}
	return len(w.NeighborBegins) < len(w.Ends)
}

func sortInts(s []int) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j-1] > s[j]; j-- {
			s[j-1], s[j] = s[j], s[j-1]
		}
	}
}

func sortPairsByEnd(pairs []Pair) {
	for i := 1; i < len(pairs); i++ {
		for j := i; j > 0 && pairs[j-1].End > pairs[j].End; j-- {
			pairs[j-1], pairs[j] = pairs[j], pairs[j-1]
		}
	}
}
