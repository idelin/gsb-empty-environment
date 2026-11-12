package taskpkg

import "errors"

// orderGraph 把普通事件与撤销节点统一放进一个有向无环图：边 u->v
// 表示 u 必须先于 v 发生（v.After 包含 u）。
type orderGraph struct {
	index map[string]int
	ids   []string
	edges [][]int
	reach [][]bool // 传递闭包，reach[u][v] 为真表示 u 先于 v（含间接边）
}

// before 判断节点 a 是否在偏序上先于节点 b。
func (g *orderGraph) before(a, b int) bool { return g.reach[a][b] }

// buildOrderGraph 构建并校验偏序图，返回的错误描述第一处非法输入。
func buildOrderGraph(events []Event, revs []Revocation) (*orderGraph, error) {
	g := &orderGraph{index: make(map[string]int)}
	addNode := func(id string) error {
		if id == "" {
			return errors.New("event or revocation id must not be empty")
		}
		if _, dup := g.index[id]; dup {
			return errors.New("duplicated node id: " + id)
		}
		g.index[id] = len(g.ids)
		g.ids = append(g.ids, id)
		return nil
	}
	for i := range events {
		if err := addNode(events[i].ID); err != nil {
			return nil, err
		}
	}
	for i := range revs {
		if err := addNode(revs[i].ID); err != nil {
			return nil, err
		}
	}

	n := len(g.ids)
	g.edges = make([][]int, n)
	g.reach = make([][]bool, n)
	for i := range g.reach {
		g.reach[i] = make([]bool, n)
	}

	addEdge := func(from, to string) error {
		f, ok1 := g.index[from]
		t, ok2 := g.index[to]
		if !ok1 || !ok2 {
			return errors.New("order edge references unknown node: " + from + " -> " + to)
		}
		if f == t {
			return errors.New("self ordering edge on node: " + from)
		}
		if !g.reach[f][t] {
			g.reach[f][t] = true
			g.edges[f] = append(g.edges[f], t)
		}
		return nil
	}
	for i := range events {
		for _, pred := range events[i].After {
			if err := addEdge(pred, events[i].ID); err != nil {
				return nil, err
			}
		}
	}
	for i := range revs {
		for _, pred := range revs[i].After {
			if err := addEdge(pred, revs[i].ID); err != nil {
				return nil, err
			}
		}
	}

	if hasCycle(g.reach) {
		return nil, errors.New("order constraints contain a cycle")
	}
	// Warshall 求传递闭包。当前 reach 只含直接边，循环补齐间接可达。
	for k := 0; k < n; k++ {
		for i := 0; i < n; i++ {
			if !g.reach[i][k] {
				continue
			}
			row := g.reach[i]
			krow := g.reach[k]
			for j := 0; j < n; j++ {
				if krow[j] {
					row[j] = true
				}
			}
		}
	}
	return g, nil
}

// hasCycle 在布尔邻接矩阵上判断是否存在环（含自环，自环在入图时已拦）。
func hasCycle(adj [][]bool) bool {
	n := len(adj)
	closure := make([][]bool, n)
	for i := range adj {
		closure[i] = append([]bool(nil), adj[i]...)
	}
	for k := 0; k < n; k++ {
		for i := 0; i < n; i++ {
			if !closure[i][k] {
				continue
			}
			for j := 0; j < n; j++ {
				if closure[k][j] {
					closure[i][j] = true
				}
			}
		}
	}
	for i := 0; i < n; i++ {
		if closure[i][i] {
			return true
		}
	}
	return false
}

// feasibleWith 在偏序之上额外要求 extra 中的每条边（from 先于 to），
// 判断补边后是否仍无环。环意味着该要求与已知偏序矛盾，任何拓扑序都
// 无法满足，候选证据即不成立。
func (g *orderGraph) feasibleWith(extra []OrderedPair) bool {
	n := len(g.ids)
	adj := make([][]bool, n)
	for i := range adj {
		adj[i] = append([]bool(nil), g.reach[i]...)
	}
	for _, p := range extra {
		from, ok1 := g.index[p.From]
		to, ok2 := g.index[p.To]
		if !ok1 || !ok2 {
			return false
		}
		if from == to {
			return false
		}
		adj[from][to] = true
	}
	return !hasCycle(adj)
}

// LinearExtensions 枚举偏序下的全部合法拓扑序，每个拓扑序以节点 ID
// 切片返回。limit <= 0 时使用默认上限；达到上限后提前停止并把
// truncated 置真。输入节点很少（业务事件至多 20 个，另有撤销节点），
// 该函数用于与判定器结论做穷举对照。
func LinearExtensions(in Input, limit int) (orders [][]string, truncated bool, err error) {
	g, err := buildOrderGraph(in.Events, in.Revocations)
	if err != nil {
		return nil, false, err
	}
	if limit <= 0 {
		limit = 100000
	}
	n := len(g.ids)
	indeg := make([]int, n)
	adj := make([][]int, n)
	for u := range g.edges {
		for _, v := range g.edges[u] {
			adj[u] = append(adj[u], v)
			indeg[v]++
		}
	}
	current := make([]string, 0, n)
	var backtrack func()
	backtrack = func() {
		if len(orders) >= limit {
			truncated = true
			return
		}
		zero := -1
		for i := 0; i < n; i++ {
			if indeg[i] == 0 {
				zero = i
				break
			}
		}
		if zero == -1 {
			if len(current) == n {
				orders = append(orders, append([]string(nil), current...))
			}
			return
		}
		// 逐个尝试每个入度为零的节点，保证枚举到全部合法拓扑序。
		for u := 0; u < n; u++ {
			if indeg[u] != 0 {
				continue
			}
			indeg[u] = -1
			for _, v := range adj[u] {
				indeg[v]--
			}
			current = append(current, g.ids[u])
			backtrack()
			current = current[:len(current)-1]
			for _, v := range adj[u] {
				indeg[v]++
			}
			indeg[u] = 0
			if len(orders) >= limit {
				truncated = true
				return
			}
		}
	}
	backtrack()
	return orders, truncated, nil
}

// lineage 是主体或设备的受信任派生谱系图，边 from->to 表示 to 由 from
// 派生，因此 from 持有的令牌沿派生关系流到 to 是可信的。
type lineage struct {
	index map[Instance]int
	nodes []Instance
	adj   [][]int
	reach [][]bool
}

func buildLineage(edges []DeriveEdge) (*lineage, error) {
	l := &lineage{index: make(map[Instance]int)}
	intern := func(x Instance) int {
		if id, ok := l.index[x]; ok {
			return id
		}
		id := len(l.nodes)
		l.index[x] = id
		l.nodes = append(l.nodes, x)
		return id
	}
	for _, e := range edges {
		u := intern(e.From)
		v := intern(e.To)
		if u == v {
			return nil, errors.New("derivation edge references the same instance")
		}
	}
	n := len(l.nodes)
	l.adj = make([][]int, n)
	l.reach = make([][]bool, n)
	for i := range l.reach {
		l.reach[i] = make([]bool, n)
	}
	for _, e := range edges {
		u := l.index[e.From]
		v := l.index[e.To]
		l.adj[u] = append(l.adj[u], v)
		l.reach[u][v] = true
	}
	if hasCycle(l.reach) {
		return nil, errors.New("derivation graph contains a cycle")
	}
	for k := 0; k < n; k++ {
		for i := 0; i < n; i++ {
			if !l.reach[i][k] {
				continue
			}
			for j := 0; j < n; j++ {
				if l.reach[k][j] {
					l.reach[i][j] = true
				}
			}
		}
	}
	return l, nil
}

// reaches 判断 from 是否等于 to，或可沿派生边到达 to。
func (l *lineage) reaches(from, to Instance) bool {
	if from == to {
		return true
	}
	u, ok1 := l.index[from]
	v, ok2 := l.index[to]
	if !ok1 || !ok2 {
		return false
	}
	return l.reach[u][v]
}

// path 返回从 from 到 to 的一条派生路径（含两端），相等时返回单元素。
// 不可达时返回 nil，用于在证据中展示令牌实际流过的派生链。
func (l *lineage) path(from, to Instance) []Instance {
	if from == to {
		return []Instance{from}
	}
	u, ok1 := l.index[from]
	v, ok2 := l.index[to]
	if !ok1 || !ok2 || !l.reach[u][v] {
		return nil
	}
	parent := make([]int, len(l.nodes))
	for i := range parent {
		parent[i] = -1
	}
	queue := []int{u}
	parent[u] = u
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		if cur == v {
			break
		}
		for _, nxt := range l.adj[cur] {
			if parent[nxt] == -1 {
				parent[nxt] = cur
				queue = append(queue, nxt)
			}
		}
	}
	if parent[v] == -1 {
		return nil
	}
	var rev []Instance
	for x := v; x != u; x = parent[x] {
		rev = append(rev, l.nodes[x])
	}
	rev = append(rev, l.nodes[u])
	out := make([]Instance, 0, len(rev))
	for i := len(rev) - 1; i >= 0; i-- {
		out = append(out, rev[i])
	}
	return out
}
