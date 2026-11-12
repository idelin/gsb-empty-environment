package netsec

import (
	"errors"
	"sort"
	"sync"
	"time"
)

// Kind 描述依赖图中节点的角色。
type Kind int

const (
	// KindSource 是期限的原始来源（例如一条安全日志）。
	KindSource Kind = iota
	// KindCopy 通过复制某个已有节点派生。
	KindCopy
	// KindMerge 通过合并多个已有节点派生。
	KindMerge
	// KindAnonymize 通过匿名化派生；携带有效证明标签时可采用独立期限。
	KindAnonymize
	// KindRebuild 是定期重建产生的快照，语义上等同普通派生。
	KindRebuild
)

// String 返回节点角色的可读名称。
func (k Kind) String() string {
	switch k {
	case KindSource:
		return "source"
	case KindCopy:
		return "copy"
	case KindMerge:
		return "merge"
	case KindAnonymize:
		return "anonymize"
	case KindRebuild:
		return "rebuild"
	default:
		return "unknown"
	}
}

// 期限传播过程中可能返回的错误。
var (
	// ErrNodeNotFound 表示引用的节点不存在。
	ErrNodeNotFound = errors.New("netsec: node not found")
	// ErrNodeExists 表示节点 ID 已被占用。
	ErrNodeExists = errors.New("netsec: node already exists")
	// ErrAnonymizeRequired 表示只有匿名化节点才能申请独立期限。
	ErrAnonymizeRequired = errors.New("netsec: only anonymize nodes may use an independent deadline")
	// ErrMissingSource 表示新增派生节点时引用了尚不存在的来源节点。
	// 迟到来源场景应先 AddSource 登记节点，再建立依赖。
	ErrMissingSource = errors.New("netsec: source node must be registered before deriving from it")
)

// Node 是依赖图中的一个数据节点。
//
// 期限语义：
//   - 来源节点的期限由宿主直接设定；
//   - 普通派生（复制、合并、重建）取所有上游中的最早期限；
//   - 匿名化节点默认同样取最早期限，只有当它携带仍然有效的证明标签
//     （标签存在于 Graph 的有效标签集合中）且申请了独立期限时，才可
//     改用自己的独立期限；此时它成为传播屏障，不再受上游最早期限约束。
type Node struct {
	ID       string
	Kind     Kind
	Deadline time.Time

	// 以下字段仅匿名化节点使用。
	Label          string
	Independent    time.Time
	hasIndependent bool
	// hasAttestationTag 由 Graph 根据标签是否有效维护。
	hasAttestationTag bool
}

// IsIndependent 报告节点当前是否以独立期限运行（匿名化 + 有效证明 + 已申请独立期限）。
func (n *Node) IsIndependent() bool {
	return n.Kind == KindAnonymize && n.hasAttestationTag && n.hasIndependent
}

// NodeInfo 是节点的只读快照，供宿主检查图状态。
type NodeInfo struct {
	ID                string
	Kind              Kind
	Deadline          time.Time
	Label             string
	HasAttestationTag bool
	Independent       time.Time
	HasIndependent    bool
	IsIndependent     bool
	Sources           []string
}

// Graph 是支持增量更新的保留期限依赖图。
//
// Graph 的所有方法均可被多个 goroutine 并发调用。
type Graph struct {
	mu     sync.RWMutex
	nodes  map[string]*Node
	out    map[string]map[string]struct{} // node -> 它依赖的上游
	in     map[string]map[string]struct{} // node -> 依赖它的下游
	active map[string]struct{}            // 仍然有效的证明标签
}

// NewGraph 创建一个空的依赖图。
func NewGraph() *Graph {
	return &Graph{
		nodes:  make(map[string]*Node),
		out:    make(map[string]map[string]struct{}),
		in:     make(map[string]map[string]struct{}),
		active: make(map[string]struct{}),
	}
}

// AddSource 登记一个原始来源并设定其保留期限。
// deadline 的零值表示“暂未设限”，可随后用 SetSourceDeadline 补齐（迟到来源）。
func (g *Graph) AddSource(id string, deadline time.Time) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if _, ok := g.nodes[id]; ok {
		return ErrNodeExists
	}
	g.addNodeLocked(&Node{ID: id, Kind: KindSource, Deadline: deadline})
	g.recomputeLocked(g.descendantsLocked(id))
	return nil
}

// AddCopy 新增一个复制节点，从 src 派生。
func (g *Graph) AddCopy(id, src string) error {
	return g.addDerived(id, KindCopy, []string{src}, "", time.Time{}, false)
}

// AddMerge 新增一个合并节点，从所有给定来源派生；来源列表会去重。
func (g *Graph) AddMerge(id string, sources ...string) error {
	return g.addDerived(id, KindMerge, sources, "", time.Time{}, false)
}

// AddRebuild 新增一个定期重建节点，从 src 派生；语义与复制相同，
// 区别仅在于角色标记，便于宿主区分定期重建产物。
func (g *Graph) AddRebuild(id, src string) error {
	return g.addDerived(id, KindRebuild, []string{src}, "", time.Time{}, false)
}

// AddAnonymize 新增一个匿名化节点。
//
// label 是它申请独立期限所需的证明标签；independent 是期望采用的独立期限。
// 只有当该标签当前有效（见 IssueAttestation）时，独立期限才会生效，
// 否则节点先按普通派生处理，待证明签发后自动切换。
// label 为空时 independent 必须也是零值。
func (g *Graph) AddAnonymize(id string, sources []string, label string, independent time.Time) error {
	if label == "" && !independent.IsZero() {
		return ErrAnonymizeRequired
	}
	return g.addDerived(id, KindAnonymize, sources, label, independent, !independent.IsZero())
}

func (g *Graph) addDerived(id string, kind Kind, sources []string, label string, independent time.Time, hasIndependent bool) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if _, ok := g.nodes[id]; ok {
		return ErrNodeExists
	}
	uniq := uniqueSorted(sources)
	for _, src := range uniq {
		if _, ok := g.nodes[src]; !ok {
			return ErrMissingSource
		}
	}
	node := &Node{
		ID:             id,
		Kind:           kind,
		Label:          label,
		Independent:    independent,
		hasIndependent: hasIndependent,
	}
	g.addNodeLocked(node)
	for _, src := range uniq {
		g.addEdgeLocked(src, id)
	}
	g.refreshAttestationLocked(node)
	g.recomputeLocked(g.descendantsLocked(id))
	return nil
}

func (g *Graph) addNodeLocked(node *Node) {
	g.nodes[node.ID] = node
	g.out[node.ID] = make(map[string]struct{})
	g.in[node.ID] = make(map[string]struct{})
}

func (g *Graph) addEdgeLocked(src, dst string) {
	g.out[dst][src] = struct{}{}
	g.in[src][dst] = struct{}{}
}

// AddEdge 增量增加一条“dst 依赖 src”的边，用于迟到来源接入已有派生节点。
// 两个节点都必须已登记；重复添加同一条边是幂等的。
func (g *Graph) AddEdge(src, dst string) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if _, ok := g.nodes[src]; !ok {
		return ErrNodeNotFound
	}
	if _, ok := g.nodes[dst]; !ok {
		return ErrNodeNotFound
	}
	if _, exists := g.out[dst][src]; exists {
		return nil
	}
	g.addEdgeLocked(src, dst)
	// 新增边只会收紧期限，受影响的是 dst 及其全部下游。
	g.recomputeLocked(g.descendantsLocked(dst))
	return nil
}

// SetSourceDeadline 更新来源节点的保留期限，并增量传播到全部下游。
// 这同时用于“迟到来源”：先 AddSource（零期限）建好依赖，再补设期限。
func (g *Graph) SetSourceDeadline(id string, deadline time.Time) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	node, ok := g.nodes[id]
	if !ok {
		return ErrNodeNotFound
	}
	if node.Kind != KindSource {
		return errors.New("netsec: only source deadlines can be set directly")
	}
	node.Deadline = deadline
	g.recomputeLocked(g.descendantsLocked(id))
	return nil
}

// IssueAttestation 签发（或恢复）一个证明标签。
//
// 所有携带该标签、且已申请独立期限的匿名化节点会立即切换为独立期限，
// 并从其下游的最早期限计算中移除自己的上游约束。
func (g *Graph) IssueAttestation(label string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if _, ok := g.active[label]; ok {
		return
	}
	g.active[label] = struct{}{}
	affected := newStringSet()
	for _, node := range g.sortedNodesLocked() {
		if node.Kind == KindAnonymize && node.Label == label {
			g.refreshAttestationLocked(node)
			affected.add(node.ID)
			affected.addAll(g.descendantsLocked(node.ID))
		}
	}
	if affected.len() > 0 {
		g.recomputeLocked(affected)
	}
}

// RevokeAttestation 撤销一个证明标签。
//
// 携带该标签的匿名化节点立即失去独立期限，重新按普通派生取上游最早期限，
// 变化会增量传播到全部下游；撤销一个不存在或已失效的标签是空操作。
func (g *Graph) RevokeAttestation(label string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if _, ok := g.active[label]; !ok {
		return
	}
	delete(g.active, label)
	affected := newStringSet()
	for _, node := range g.sortedNodesLocked() {
		if node.Kind == KindAnonymize && node.Label == label && node.hasAttestationTag {
			node.hasAttestationTag = false
			affected.add(node.ID)
			affected.addAll(g.descendantsLocked(node.ID))
		}
	}
	if affected.len() > 0 {
		g.recomputeLocked(affected)
	}
}

// SetIndependentDeadline 修改匿名化节点申请的独立期限。
//
// 证明有效时立即生效并向下游传播；证明缺失时仅记录申请，待签发后生效。
// 非匿名化节点返回 ErrAnonymizeRequired。
func (g *Graph) SetIndependentDeadline(id string, independent time.Time) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	node, ok := g.nodes[id]
	if !ok {
		return ErrNodeNotFound
	}
	if node.Kind != KindAnonymize {
		return ErrAnonymizeRequired
	}
	node.Independent = independent
	node.hasIndependent = !independent.IsZero()
	g.refreshAttestationLocked(node)
	g.recomputeLocked(g.descendantsLocked(id))
	return nil
}

func (g *Graph) refreshAttestationLocked(node *Node) {
	if node.Kind != KindAnonymize || node.Label == "" {
		node.hasAttestationTag = false
		return
	}
	_, node.hasAttestationTag = g.active[node.Label]
}

// Deadline 返回节点当前生效的保留期限；零值表示不受约束。节点不存在时返回错误。
func (g *Graph) Deadline(id string) (time.Time, error) {
	g.mu.RLock()
	defer g.mu.RUnlock()
	node, ok := g.nodes[id]
	if !ok {
		return time.Time{}, ErrNodeNotFound
	}
	return node.Deadline, nil
}

// Node 返回节点的只读快照；节点不存在时返回错误。
func (g *Graph) Node(id string) (NodeInfo, error) {
	g.mu.RLock()
	defer g.mu.RUnlock()
	node, ok := g.nodes[id]
	if !ok {
		return NodeInfo{}, ErrNodeNotFound
	}
	return NodeInfo{
		ID:                node.ID,
		Kind:              node.Kind,
		Deadline:          node.Deadline,
		Label:             node.Label,
		HasAttestationTag: node.hasAttestationTag,
		Independent:       node.Independent,
		HasIndependent:    node.hasIndependent,
		IsIndependent:     node.IsIndependent(),
		Sources:           setToSorted(g.out[id]),
	}, nil
}

// Len 返回图中的节点数量。
func (g *Graph) Len() int {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return len(g.nodes)
}

// Rebuild 从零重算整张图。
//
// 它不改变任何节点、边或证明状态，只重新推导所有期限，
// 供宿主在定期维护时调用，以校验增量传播结果并回收内部状态。
// 返回重算的节点数量。
func (g *Graph) Rebuild() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	all := newStringSet()
	for id := range g.nodes {
		all.add(id)
	}
	g.recomputeLocked(all)
	return all.len()
}

// descendantsLocked 返回 seed 及其全部下游（传递闭包），包含 seed 自身。
func (g *Graph) descendantsLocked(seed string) *stringSet {
	seen := newStringSet()
	queue := []string{seed}
	seen.add(seed)
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		for next := range g.in[cur] {
			if seen.add(next) {
				queue = append(queue, next)
			}
		}
	}
	return seen
}

// recomputeLocked 在 affected 闭包上重新推导期限。
//
// 算法：先在受影响子图上求强连通分量（SCC），SCC 按“上游在前”的
// 拓扑序排列；然后按该顺序对每个 SCC 做不动点迭代。这样：
//   - 无环图每个 SCC 只迭代一次，等价于普通拓扑传播；
//   - 循环中的节点通过不动点收敛到同一最早期限；
//   - 携带有效证明的匿名化节点是屏障，期限锁定为其独立期限，
//     循环无法借由它延长任何来源的保留期限。
func (g *Graph) recomputeLocked(affected *stringSet) {
	if affected.len() == 0 {
		return
	}
	components := g.sccLocked(affected)

	// 受影响节点一律从中性初值重新推导：来源取自身基值，其余取零值（无约束）。
	// 这样既可以收紧、也可以放宽期限——例如来源期限被推迟，或证明签发使某个
	// 匿名化节点成为屏障而解除上游约束。闭包外的上游保持不变，作为外部基值参与计算。
	values := make(map[string]time.Time, affected.len())
	for id := range affected.m {
		node := g.nodes[id]
		if node.Kind == KindSource {
			values[id] = node.Deadline
		} else {
			values[id] = time.Time{}
		}
	}

	for _, component := range components {
		// 屏障节点（有效独立期限的匿名化节点）锁定，不参与 min 传播。
		locked := make(map[string]bool, len(component))
		for _, id := range component {
			node := g.nodes[id]
			if node.IsIndependent() {
				locked[id] = true
				values[id] = node.Independent
			}
		}

		// 至多迭代 len(component) 轮即可保证收敛（每轮至少推进一条依赖边）。
		for iteration := 0; iteration < len(component); iteration++ {
			changed := false
			for _, id := range component {
				if locked[id] {
					continue
				}
				node := g.nodes[id]
				candidate := time.Time{}
				if node.Kind == KindSource {
					candidate = node.Deadline
				}
				for _, upstream := range setToSorted(g.out[id]) {
					var upstreamValue time.Time
					if v, ok := values[upstream]; ok {
						upstreamValue = v
					} else {
						upstreamValue = g.nodes[upstream].Deadline
					}
					candidate = minTime(candidate, upstreamValue)
				}
				if candidate != values[id] {
					values[id] = candidate
					changed = true
				}
			}
			if !changed {
				break
			}
		}
	}

	for id, deadline := range values {
		g.nodes[id].Deadline = deadline
	}
}

// sccLocked 用 Kosaraju 算法求受影响子图的强连通分量。
//
// 返回的分量按“上游在前”的拓扑序排列：若分量 B 依赖分量 A（B 中有边指向 A），
// 则 A 排在 B 前面，保证处理 B 时 A 的值已经确定。分量内部节点按 ID 排序，
// 使推导过程与解释输出保持确定性。
func (g *Graph) sccLocked(affected *stringSet) [][]string {
	visited := make(map[string]bool, affected.len())
	var finishOrder []string

	var dfsForward func(string)
	dfsForward = func(id string) {
		if visited[id] {
			return
		}
		visited[id] = true
		for _, upstream := range setToSorted(g.out[id]) {
			if affected.contains(upstream) {
				dfsForward(upstream)
			}
		}
		finishOrder = append(finishOrder, id)
	}
	for _, id := range affected.sorted() {
		dfsForward(id)
	}

	assigned := make(map[string]bool, affected.len())
	var components [][]string

	var dfsReverse func(string, *[]string)
	dfsReverse = func(id string, component *[]string) {
		assigned[id] = true
		*component = append(*component, id)
		for _, downstream := range setToSorted(g.in[id]) {
			if affected.contains(downstream) && !assigned[downstream] {
				dfsReverse(downstream, component)
			}
		}
	}
	// 逆序处理完成序：Kosaraju 在反向图上得到的分量是“下游在前”，
	// 反转后得到“上游在前”的拓扑序，保证处理某个分量时其上游分量已定稿。
	for i := len(finishOrder) - 1; i >= 0; i-- {
		id := finishOrder[i]
		if assigned[id] {
			continue
		}
		var component []string
		dfsReverse(id, &component)
		sort.Strings(component)
		components = append(components, component)
	}
	for left, right := 0, len(components)-1; left < right; left, right = left+1, right-1 {
		components[left], components[right] = components[right], components[left]
	}
	return components
}

func (g *Graph) sortedNodesLocked() []*Node {
	ids := make([]string, 0, len(g.nodes))
	for id := range g.nodes {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	nodes := make([]*Node, 0, len(ids))
	for _, id := range ids {
		nodes = append(nodes, g.nodes[id])
	}
	return nodes
}
