package netsec

import (
	"sort"
	"time"
)

// SourceExplanation 解释某个节点为何受到某个原始来源的期限约束。
type SourceExplanation struct {
	// SourceID 是原始来源节点的 ID。
	SourceID string
	// SourceDeadline 是该来源当前的期限。
	SourceDeadline time.Time
	// Path 是从来源到被解释节点的一条依赖路径，Path[0] 为来源，末元素为被解释节点。
	Path []string
	// Determining 为 true 时表示该来源（之一）决定了当前的最早期限。
	Determining bool
}

// Explanation 是 Explain 的结果，完整解释一个节点的最早期限从何而来。
type Explanation struct {
	// NodeID 是被解释的节点。
	NodeID string
	// Deadline 是该节点生效的期限；零值表示不受任何保留期限约束。
	Deadline time.Time
	// Independent 为 true 时表示该匿名化节点凭借有效证明采用独立期限，
	// 此时 Determining 与 OtherSources 均为空。
	Independent bool
	// Determining 是真正决定最早期限的来源集合。
	Determining []SourceExplanation
	// OtherSources 是其余仍约束该节点、但期限更晚的来源。
	OtherSources []SourceExplanation
	// InCycle 表示该节点处于某个依赖循环中。
	InCycle bool
	// Cycles 给出包含该节点的循环；每个循环是一条首尾相连的节点 ID 序列，
	// 末元素等于首元素。
	Cycles [][]string
}

// Explain 解释节点 id 的生效期限由哪些原始来源决定。
//
// 它沿“依赖的上游”方向追溯，遇到携带有效证明的匿名化屏障节点即停止；
// 追溯到的 KindSource 节点按期限分为决定来源（最早）与其他来源，
// 并各自附带一条具体依赖路径。节点不存在时返回 ErrNodeNotFound。
func (g *Graph) Explain(id string) (Explanation, error) {
	g.mu.RLock()
	defer g.mu.RUnlock()

	node, ok := g.nodes[id]
	if !ok {
		return Explanation{}, ErrNodeNotFound
	}

	explanation := Explanation{
		NodeID:   id,
		Deadline: node.Deadline,
	}

	if node.IsIndependent() {
		explanation.Independent = true
		explanation.Determining = []SourceExplanation{}
		explanation.OtherSources = []SourceExplanation{}
		explanation.Cycles = g.cyclesThroughLocked(id)
		explanation.InCycle = len(explanation.Cycles) > 0
		return explanation, nil
	}

	// 沿上游追溯，屏障节点与来源节点都是追溯的终点。
	roots := newStringSet()
	queue := []string{id}
	seen := newStringSet()
	seen.add(id)
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		curNode := g.nodes[cur]
		if curNode.Kind == KindSource || curNode.IsIndependent() {
			roots.add(cur)
			continue
		}
		for _, upstream := range setToSorted(g.out[cur]) {
			if seen.add(upstream) {
				queue = append(queue, upstream)
			}
		}
	}

	determiningDeadline := node.Deadline
	for _, rootID := range roots.sorted() {
		rootNode := g.nodes[rootID]
		rootDeadline := rootNode.Deadline
		if rootNode.IsIndependent() {
			rootDeadline = rootNode.Independent
		}
		entry := SourceExplanation{
			SourceID:       rootID,
			SourceDeadline: rootDeadline,
			Path:           g.pathToLocked(rootID, id),
		}
		// 零值期限代表“无约束”，不会成为决定来源。
		if !rootDeadline.IsZero() && rootDeadline.Equal(determiningDeadline) {
			entry.Determining = true
			explanation.Determining = append(explanation.Determining, entry)
		} else {
			explanation.OtherSources = append(explanation.OtherSources, entry)
		}
	}
	if explanation.Determining == nil {
		explanation.Determining = []SourceExplanation{}
	}
	if explanation.OtherSources == nil {
		explanation.OtherSources = []SourceExplanation{}
	}

	explanation.Cycles = g.cyclesThroughLocked(id)
	explanation.InCycle = len(explanation.Cycles) > 0
	return explanation, nil
}

// pathToLocked 返回一条从上游 from 到下游 to 的依赖路径（沿 in 边方向 BFS）。
// 调用方需保证 from 可追溯到达 to；结果包含两端。
func (g *Graph) pathToLocked(from, to string) []string {
	if from == to {
		return []string{from}
	}
	predecessor := map[string]string{from: ""}
	queue := []string{from}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		for _, next := range setToSorted(g.in[cur]) {
			if _, visited := predecessor[next]; visited {
				continue
			}
			predecessor[next] = cur
			if next == to {
				queue = nil
				break
			}
			queue = append(queue, next)
		}
	}

	var reversed []string
	for cur := to; cur != ""; cur = predecessor[cur] {
		reversed = append(reversed, cur)
		if cur == from {
			break
		}
	}
	path := make([]string, len(reversed))
	for i, nodeID := range reversed {
		path[len(reversed)-1-i] = nodeID
	}
	return path
}

// cyclesThroughLocked 找出包含节点 id 的简单循环。
//
// 沿 in 边（下游方向）做 DFS：从 id 的每个下游邻居出发，在不再次经过 id 的
// 前提下寻找回到 id 的简单路径，找到一条即构成一个简单循环。
// 自环（id 直接依赖自己）单独报告。
func (g *Graph) cyclesThroughLocked(id string) [][]string {
	var cycles [][]string

	if _, selfLoop := g.out[id][id]; selfLoop {
		cycles = append(cycles, []string{id, id})
	}

	onStack := map[string]bool{id: true}
	var dfs func(string, []string)
	dfs = func(cur string, trail []string) {
		for _, next := range setToSorted(g.in[cur]) {
			if next == id {
				// trail 是从 id 的邻居到 cur 的路径；首尾补上 id 形成闭环。
				cycle := make([]string, 0, len(trail)+2)
				cycle = append(cycle, id)
				cycle = append(cycle, trail...)
				cycle = append(cycle, id)
				cycles = append(cycles, cycle)
				continue
			}
			if onStack[next] {
				continue
			}
			onStack[next] = true
			dfs(next, append(trail, next))
			delete(onStack, next)
		}
	}

	for _, neighbor := range setToSorted(g.in[id]) {
		if neighbor == id {
			continue
		}
		onStack[neighbor] = true
		dfs(neighbor, []string{neighbor})
		delete(onStack, neighbor)
	}

	sort.Slice(cycles, func(i, j int) bool {
		return joinPath(cycles[i]) < joinPath(cycles[j])
	})
	return cycles
}

func joinPath(path []string) string {
	result := ""
	for i, nodeID := range path {
		if i > 0 {
			result += "/"
		}
		result += nodeID
	}
	return result
}
