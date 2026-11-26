package spp

import (
	"fmt"
	"sort"
)

// Graph 是 Config 经过结构校验后的内部表示。
type Graph struct {
	Destination int
	Nodes       []int
	nodeSet     map[int]bool
	edges       map[int][]Edge // From -> 出边
	preferences map[int][]Path
	ranks       map[int]map[string]int
}

// Load 校验 Config 的结构完整性并构造 Graph。
//
// 校验内容包括：节点/边合法性、路径简单性与端点约束、偏好序严格无重复、
// 每条路径的每一跳都必须由真实有向边支撑（无不可通告的幻影路径）。
func Load(cfg *Config) (*Graph, error) {
	if cfg == nil {
		return nil, fmt.Errorf("输入配置为空")
	}
	if len(cfg.Nodes) == 0 {
		return nil, fmt.Errorf("节点列表为空")
	}
	g := &Graph{
		Destination: cfg.Destination,
		nodeSet:     make(map[int]bool),
		edges:       make(map[int][]Edge),
		preferences: make(map[int][]Path),
		ranks:       make(map[int]map[string]int),
	}
	for _, as := range cfg.Nodes {
		if g.nodeSet[as] {
			return nil, fmt.Errorf("节点 %d 重复出现", as)
		}
		g.nodeSet[as] = true
		g.Nodes = append(g.Nodes, as)
	}
	if !g.nodeSet[cfg.Destination] {
		return nil, fmt.Errorf("源站 %d 不在节点列表中", cfg.Destination)
	}

	seenEdge := make(map[[2]int]bool)
	for _, e := range cfg.Edges {
		if !g.nodeSet[e.From] || !g.nodeSet[e.To] {
			return nil, fmt.Errorf("边 %d->%d 引用了不存在的节点", e.From, e.To)
		}
		if e.From == e.To {
			return nil, fmt.Errorf("不允许自环边 %d->%d", e.From, e.To)
		}
		kind := EdgeKind(e.Kind)
		if kind != KindC2P && kind != KindP2C && kind != KindP2P {
			return nil, fmt.Errorf("边 %d->%d 的类型 %q 非法", e.From, e.To, e.Kind)
		}
		key := [2]int{e.From, e.To}
		if seenEdge[key] {
			return nil, fmt.Errorf("边 %d->%d 重复定义", e.From, e.To)
		}
		seenEdge[key] = true
		g.edges[e.From] = append(g.edges[e.From], Edge{From: e.From, To: e.To, Kind: kind})
	}
	for from := range g.edges {
		sort.Slice(g.edges[from], func(i, j int) bool { return g.edges[from][i].To < g.edges[from][j].To })
	}

	pref, err := cfg.preferenceMap()
	if err != nil {
		return nil, err
	}
	for _, as := range g.Nodes {
		paths, ok := pref[as]
		if !ok {
			if as == cfg.Destination {
				paths = []Path{{as}}
			} else {
				paths = nil
			}
		}
		seen := make(map[string]bool)
		g.ranks[as] = make(map[string]int)
		for rank, p := range paths {
			if len(p) == 0 {
				return nil, fmt.Errorf("AS %d 的偏好序中存在空路径", as)
			}
			if p[0] != as {
				return nil, fmt.Errorf("AS %d 的路径 %s 必须以自身开头", as, p)
			}
			if p[len(p)-1] != cfg.Destination {
				return nil, fmt.Errorf("AS %d 的路径 %s 必须以源站 %d 结尾", as, p, cfg.Destination)
			}
			if !simple(p) {
				return nil, fmt.Errorf("AS %d 的路径 %s 含重复 AS（违反 AS_PATH 防环）", as, p)
			}
			if err := g.checkEdges(p); err != nil {
				return nil, fmt.Errorf("AS %d 的路径 %s 不被图支持: %w", as, p, err)
			}
			if seen[p.Key()] {
				return nil, fmt.Errorf("AS %d 的偏好序中路径 %s 重复（必须是严格全序）", as, p)
			}
			seen[p.Key()] = true
			g.ranks[as][p.Key()] = rank
		}
		g.preferences[as] = make([]Path, len(paths))
		for i, p := range paths {
			g.preferences[as][i] = copyPath(p)
		}
	}
	return g, nil
}

func simple(p Path) bool {
	seen := make(map[int]bool, len(p))
	for _, as := range p {
		if seen[as] {
			return false
		}
		seen[as] = true
	}
	return true
}

func (g *Graph) checkEdges(p Path) error {
	for i := 0; i+1 < len(p); i++ {
		if g.edge(p[i], p[i+1]) == nil {
			return fmt.Errorf("跳 %d->%d 不存在", p[i], p[i+1])
		}
	}
	return nil
}

func (g *Graph) edge(from, to int) *Edge {
	for i := range g.edges[from] {
		if g.edges[from][i].To == to {
			return &g.edges[from][i]
		}
	}
	return nil
}

// SortedNodes 返回升序节点列表，保证所有枚举结果确定可复现。
func (g *Graph) SortedNodes() []int {
	out := make([]int, len(g.Nodes))
	copy(out, g.Nodes)
	sort.Ints(out)
	return out
}

// HasNode 判断 AS 是否属于实例。
func (g *Graph) HasNode(as int) bool { return g.nodeSet[as] }

// Out 返回 from 的全部出边（按邻居 AS 升序）。
func (g *Graph) Out(from int) []Edge {
	out := make([]Edge, len(g.edges[from]))
	copy(out, g.edges[from])
	return out
}

// Preferences 返回 AS 的严格偏好序副本（下标越小越优先）。
func (g *Graph) Preferences(as int) []Path {
	src := g.preferences[as]
	out := make([]Path, len(src))
	for i, p := range src {
		out[i] = copyPath(p)
	}
	return out
}

// Rank 返回路径在该 AS 偏好序中的排名（0 为最优），路径不允许时返回 -1。
func (g *Graph) Rank(as int, p Path) int {
	if r, ok := g.ranks[as][p.Key()]; ok {
		return r
	}
	return -1
}

// Better 判断路径 a 是否严格优于路径 b（二者均需被 AS as 允许）。
func (g *Graph) Better(as int, a, b Path) bool {
	ra, rb := g.Rank(as, a), g.Rank(as, b)
	if ra < 0 || rb < 0 {
		return false
	}
	return ra < rb
}

// NextHops 返回 AS as 的允许路径所使用的全部直连下一跳。
func (g *Graph) NextHops(as int) []int {
	seen := make(map[int]bool)
	var out []int
	for _, p := range g.preferences[as] {
		if len(p) >= 2 && !seen[p[1]] {
			seen[p[1]] = true
			out = append(out, p[1])
		}
	}
	sort.Ints(out)
	return out
}

// PathsVia 返回 AS as 所有以下一跳 hop 开头的允许路径（保持偏好序）。
func (g *Graph) PathsVia(as, hop int) []Path {
	var out []Path
	for _, p := range g.preferences[as] {
		if len(p) >= 2 && p[1] == hop {
			out = append(out, copyPath(p))
		}
	}
	return out
}
