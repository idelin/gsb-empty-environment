// Package clique 提供无向图的极大团（maximal clique）枚举与动态追踪。
//
// 枚举采用带主元（pivot）剪枝的 Bron–Kerbosch 算法；
// 动态增删边时，仅在受影响节点的闭邻域内做局部重算，
// 并与未受影响的全局团集合做包含关系合并，
// 从而报告每次变更后“诞生”与“消解”的极大团。
package clique

import (
	"sort"
	"strconv"
	"strings"
)

// Graph 是一个无向图，内部增量维护着当前的极大团集合。
type Graph struct {
	adj     map[uint64]map[uint64]struct{}
	cliques map[string][]uint64 // key 为升序顶点拼接，value 为升序顶点切片
}

// New 返回一个空图。
func New() *Graph {
	return &Graph{
		adj:     make(map[uint64]map[uint64]struct{}),
		cliques: make(map[string][]uint64),
	}
}

// AddNode 加入一个节点。孤立节点自身构成一个大小为 1 的极大团。
func (g *Graph) AddNode(n uint64) {
	if _, ok := g.adj[n]; ok {
		return
	}
	g.adj[n] = make(map[uint64]struct{})
	g.cliques[cliqueKey([]uint64{n})] = []uint64{n}
}

// HasEdge 报告边 (u,v) 是否存在。
func (g *Graph) HasEdge(u, v uint64) bool {
	nbrs, ok := g.adj[u]
	if !ok {
		return false
	}
	_, ok = nbrs[v]
	return ok
}

// AddEdge 加入边 (u,v)，返回本次变更诞生与消解的极大团。
// 自环或重复边为空操作，返回 nil。
func (g *Graph) AddEdge(u, v uint64) (born, died [][]uint64) {
	if u == v || g.HasEdge(u, v) {
		return nil, nil
	}
	g.AddNode(u)
	g.AddNode(v)
	g.adj[u][v] = struct{}{}
	g.adj[v][u] = struct{}{}
	return g.refresh(u, v)
}

// RemoveEdge 删除边 (u,v)，返回本次变更诞生与消解的极大团。
// 边不存在时为空操作，返回 nil。
func (g *Graph) RemoveEdge(u, v uint64) (born, died [][]uint64) {
	if !g.HasEdge(u, v) {
		return nil, nil
	}
	delete(g.adj[u], v)
	delete(g.adj[v], u)
	return g.refresh(u, v)
}

// MaximalCliques 返回当前全部极大团，每个团顶点升序，团之间按字典序排列。
func (g *Graph) MaximalCliques() [][]uint64 {
	out := make([][]uint64, 0, len(g.cliques))
	for _, c := range g.cliques {
		out = append(out, append([]uint64(nil), c...))
	}
	sortCliques(out)
	return out
}

// CliquesContaining 返回包含指定节点集合的全部极大团清单。
func (g *Graph) CliquesContaining(nodes ...uint64) [][]uint64 {
	var out [][]uint64
	for _, c := range g.cliques {
		ok := true
		for _, n := range nodes {
			if !containsVertex(c, n) {
				ok = false
				break
			}
		}
		if ok {
			out = append(out, append([]uint64(nil), c...))
		}
	}
	sortCliques(out)
	return out
}

// Recompute 用 Bron–Kerbosch 对全图从头枚举极大团，
// 结果与增量维护的状态一致，可用于校验。
func (g *Graph) Recompute() [][]uint64 {
	region := make(map[uint64]struct{}, len(g.adj))
	for n := range g.adj {
		region[n] = struct{}{}
	}
	out := maximalCliquesInRegion(g.adj, region)
	sortCliques(out)
	return out
}

// refresh 在边 (u,v) 变更后做局部重算并与全局团集合合并。
//
// 正确性要点：一次边变更只会影响包含 u 或 v 的极大团，且这些团都落在
// N[u] ∪ N[v] 的闭邻域内。因此：
//  1. 保留既不含 u 也不含 v 的旧极大团（它们在新图中仍然极大）；
//  2. 在闭邻域诱导子图上重新枚举极大团；
//  3. 丢弃被某个保留团包含的局部团（它们在全图中并非极大），
//     同时剔除被新团包含的保留团（理论上的防御，正常不会发生）。
func (g *Graph) refresh(u, v uint64) (born, died [][]uint64) {
	region := map[uint64]struct{}{u: {}, v: {}}
	for w := range g.adj[u] {
		region[w] = struct{}{}
	}
	for w := range g.adj[v] {
		region[w] = struct{}{}
	}

	old := g.cliques
	kept := make(map[string][]uint64, len(old))
	for key, c := range old {
		if !containsVertex(c, u) && !containsVertex(c, v) {
			kept[key] = c
		}
	}

	next := kept
	for _, m := range maximalCliquesInRegion(g.adj, region) {
		if subsumedByAny(m, kept) {
			continue
		}
		mKey := cliqueKey(m)
		for key, c := range next {
			if key != mKey && len(c) < len(m) && isSubset(c, m) {
				delete(next, key)
			}
		}
		next[mKey] = m
	}

	for key, c := range next {
		if _, ok := old[key]; !ok {
			born = append(born, c)
		}
	}
	for key, c := range old {
		if _, ok := next[key]; !ok {
			died = append(died, c)
		}
	}
	sortCliques(born)
	sortCliques(died)
	g.cliques = next
	return born, died
}

// maximalCliquesInRegion 在 region 的诱导子图上用带主元的 Bron–Kerbosch
// 枚举全部极大团。
func maximalCliquesInRegion(adj map[uint64]map[uint64]struct{}, region map[uint64]struct{}) [][]uint64 {
	sub := make(map[uint64]map[uint64]struct{}, len(region))
	for n := range region {
		nbrs := make(map[uint64]struct{})
		for w := range adj[n] {
			if _, ok := region[w]; ok {
				nbrs[w] = struct{}{}
			}
		}
		sub[n] = nbrs
	}
	p := make(map[uint64]struct{}, len(sub))
	for n := range sub {
		p[n] = struct{}{}
	}
	var out [][]uint64
	bronKerbosch(sub, map[uint64]struct{}{}, p, map[uint64]struct{}{}, &out)
	return out
}

// bronKerbosch 是带主元剪枝的 Bron–Kerbosch 递归：
// 选择使 |P ∩ N(u)| 最大的主元 u ∈ P ∪ X，只需对 P \ N(u) 中的顶点递归。
func bronKerbosch(adj map[uint64]map[uint64]struct{}, r, p, x map[uint64]struct{}, out *[][]uint64) {
	if len(p) == 0 && len(x) == 0 {
		c := make([]uint64, 0, len(r))
		for v := range r {
			c = append(c, v)
		}
		sort.Slice(c, func(i, j int) bool { return c[i] < c[j] })
		*out = append(*out, c)
		return
	}
	pivot, hasPivot := choosePivot(adj, p, x)
	var candidates []uint64
	for v := range p {
		if hasPivot {
			if _, isNbr := adj[pivot][v]; isNbr {
				continue
			}
		}
		candidates = append(candidates, v)
	}
	for _, v := range candidates {
		if _, ok := p[v]; !ok {
			continue
		}
		nr := make(map[uint64]struct{}, len(r)+1)
		for w := range r {
			nr[w] = struct{}{}
		}
		nr[v] = struct{}{}
		bronKerbosch(adj, nr, intersect(p, adj[v]), intersect(x, adj[v]), out)
		delete(p, v)
		x[v] = struct{}{}
	}
}

// choosePivot 在 P ∪ X 中选择与 P 交最大的主元。
func choosePivot(adj map[uint64]map[uint64]struct{}, p, x map[uint64]struct{}) (uint64, bool) {
	best := uint64(0)
	bestCount := -1
	consider := func(u uint64) {
		count := 0
		for w := range adj[u] {
			if _, ok := p[w]; ok {
				count++
			}
		}
		if count > bestCount {
			bestCount = count
			best = u
		}
	}
	for u := range p {
		consider(u)
	}
	for u := range x {
		consider(u)
	}
	return best, bestCount >= 0
}

func intersect(a, b map[uint64]struct{}) map[uint64]struct{} {
	if len(a) > len(b) {
		a, b = b, a
	}
	out := make(map[uint64]struct{}, len(a))
	for k := range a {
		if _, ok := b[k]; ok {
			out[k] = struct{}{}
		}
	}
	return out
}

func cliqueKey(c []uint64) string {
	parts := make([]string, len(c))
	for i, n := range c {
		parts[i] = strconv.FormatUint(n, 10)
	}
	return strings.Join(parts, ",")
}

func containsVertex(c []uint64, n uint64) bool {
	i := sort.Search(len(c), func(i int) bool { return c[i] >= n })
	return i < len(c) && c[i] == n
}

// subsumedByAny 报告 m 是否为某个团的子集。
func subsumedByAny(m []uint64, cliques map[string][]uint64) bool {
	for _, c := range cliques {
		if len(c) >= len(m) && isSubset(m, c) {
			return true
		}
	}
	return false
}

// isSubset 判断升序切片 a 是否为升序切片 b 的子集。
func isSubset(a, b []uint64) bool {
	i := 0
	for _, x := range a {
		for i < len(b) && b[i] < x {
			i++
		}
		if i == len(b) || b[i] != x {
			return false
		}
	}
	return true
}

// sortCliques 将每个团内部顶点升序，并将团列表按字典序排序。
func sortCliques(cs [][]uint64) {
	for _, c := range cs {
		sort.Slice(c, func(i, j int) bool { return c[i] < c[j] })
	}
	sort.Slice(cs, func(i, j int) bool {
		a, b := cs[i], cs[j]
		for k := 0; k < len(a) && k < len(b); k++ {
			if a[k] != b[k] {
				return a[k] < b[k]
			}
		}
		return len(a) < len(b)
	})
}
