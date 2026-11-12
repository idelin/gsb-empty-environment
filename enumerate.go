package netsec

// DefaultEnumerationLimit 是 EnumerateMatchings 默认返回的匹配数量上限。
//
// 完整匹配的数量在最坏情况下是阶乘级的，宿主可用更小的 limit 控制
// 枚举成本；传 0 或负数即采用本默认值。
const DefaultEnumerationLimit = 10000

// Enumeration 是全量枚举的结果。
type Enumeration struct {
	// Matchings 是枚举出的完整合法匹配，每个匹配都覆盖全部 end。
	Matchings [][]Pair
	// Truncated 为 true 表示达到 limit 后提前停止，仍有匹配未列出。
	Truncated bool
}

// EnumerateMatchings 枚举事件流中所有覆盖全部 end 的合法匹配。
//
// 枚举采用按 end 顺序的深度优先回溯：每一步只为当前 end 选择一个
// 更早、字段相容且尚未使用的 begin。输出顺序由 begin 下标决定，
// 对同一输入稳定可复现。
func EnumerateMatchings(events []Event, limit int) (*Enumeration, error) {
	for i := range events {
		if !validKind(events[i].Kind) {
			return nil, ErrInvalidEvent
		}
	}
	if limit <= 0 {
		limit = DefaultEnumerationLimit
	}

	g := buildGraph(events)
	options := make([][]int, len(g.ends))
	for v := range g.ends {
		options[v] = g.endNeighbors(v)
		sortInts(options[v])
	}

	out := &Enumeration{}
	used := make([]bool, len(g.begins))
	chosen := make([]int, len(g.ends))

	var dfs func(v int)
	dfs = func(v int) {
		if len(out.Matchings) >= limit {
			out.Truncated = true
			return
		}
		if v == len(g.ends) {
			matching := make([]Pair, len(g.ends))
			for endNode, beginNode := range chosen {
				matching[endNode] = Pair{
					Begin: g.begins[beginNode],
					End:   g.ends[endNode],
				}
			}
			sortPairsByEnd(matching)
			out.Matchings = append(out.Matchings, matching)
			return
		}
		for _, u := range options[v] {
			if used[u] {
				continue
			}
			used[u] = true
			chosen[v] = u
			dfs(v + 1)
			used[u] = false
			if out.Truncated {
				return
			}
		}
	}

	if len(g.ends) > 0 {
		dfs(0)
	} else {
		// 没有 end 时，空匹配是唯一的完整匹配。
		out.Matchings = [][]Pair{{}}
	}
	return out, nil
}
