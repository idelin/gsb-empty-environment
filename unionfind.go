package entitymerge

// unionFind 按秩合并 + 路径压缩的并查集，用于匹配置信边的传递合并。
type unionFind struct {
	parent map[string]string
	rank   map[string]int
}

func newUnionFind() *unionFind {
	return &unionFind{parent: map[string]string{}, rank: map[string]int{}}
}

func (u *unionFind) add(x string) {
	if _, ok := u.parent[x]; !ok {
		u.parent[x] = x
		u.rank[x] = 0
	}
}

// reset 将节点恢复为孤立状态，用于撤销证据后重算单个连通分量。
// 只有同一分量内的节点才可安全 reset（分量的祖先必然仍是分量成员）。
func (u *unionFind) reset(x string) {
	u.parent[x] = x
	u.rank[x] = 0
}

func (u *unionFind) find(x string) string {
	root := x
	for u.parent[root] != root {
		root = u.parent[root]
	}
	for u.parent[x] != root {
		u.parent[x], x = root, u.parent[x]
	}
	return root
}

func (u *unionFind) union(x, y string) {
	rx, ry := u.find(x), u.find(y)
	if rx == ry {
		return
	}
	if u.rank[rx] < u.rank[ry] {
		rx, ry = ry, rx
	}
	u.parent[ry] = rx
	if u.rank[rx] == u.rank[ry] {
		u.rank[rx]++
	}
}
