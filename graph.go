package netsec

import (
	"errors"
	"sync"
)

// ErrNotFound 表示注册或撤销时引用的对象标识在图中不存在。
var ErrNotFound = errors.New("netsec: sensitive object not found")

// ErrRevoked 表示对象已被撤销：其依赖闭包已标记不可用或已擦除，
// 此时无法再获取新的使用句柄，也不能在其下注册新副本。
var ErrRevoked = errors.New("netsec: sensitive object revoked")

// WipeFunc 由缓存宿主提供，负责在依赖闭包完成擦除阶段时清理宿主侧资源
// （例如从读缓存、索引缓存中摘除条目）。字节切片由图负责清零，回调在
// 图锁之外执行，宿主可安全执行耗时操作。
type WipeFunc func(n *Node)

// Node 是依赖图中的一个敏感对象节点：来源明文、读缓存副本、索引缓存副本
// 或派生片段均建模为节点。节点字段全部由 Graph 的互斥锁保护。
type Node struct {
	ID         string
	Generation int64
	Secret     []byte

	// deps 是该节点依赖的来源节点（派生边：节点 -> 来源）。
	deps map[*Node]struct{}
	// dependents 是依赖该节点的派生节点（反向边）。
	dependents map[*Node]struct{}

	// available 为 false 表示节点已进入撤销流程，拒绝新的 Acquire/注册。
	available bool
	// wiped 为 true 表示所有使用者已退出，密钥材料已清零，擦除完成。
	wiped bool
	// users 是当前持有的使用句柄数（未关闭的 Handle）。
	users int

	wipe  WipeFunc
	graph *Graph
}

// Available 报告节点是否仍可获取。已撤销但尚在等待使用者退出的节点
// 返回 false。访问器在图锁下读取，保证与撤销流程并发安全。
func (n *Node) Available() bool {
	n.graph.mu.Lock()
	defer n.graph.mu.Unlock()
	return n.available
}

// Wiped 报告节点是否已完成擦除（密钥材料已清零）。
func (n *Node) Wiped() bool {
	n.graph.mu.Lock()
	defer n.graph.mu.Unlock()
	return n.wiped
}

// Users 返回节点当前未退出的使用者数量，主要供测试与可观测性使用。
func (n *Node) Users() int {
	n.graph.mu.Lock()
	defer n.graph.mu.Unlock()
	return n.users
}

// Handle 是一次活跃使用的凭证。持有期间对应的节点不会被擦除；必须通过
// Close 退出。Close 后 Secret 返回的切片不再有效。
type Handle struct {
	graph *Graph
	node  *Node
	once  sync.Once
}

// Secret 返回敏感字节切片。只要句柄仍处于打开状态，即使节点已进入撤销
// 排空阶段（标记不可用、等待使用者退出），持有者依然能读到完整材料；
// 节点擦除完成或句柄关闭后返回 nil，保证不会暴露已清零的内存。
func (h *Handle) Secret() []byte {
	h.graph.mu.Lock()
	defer h.graph.mu.Unlock()
	if h.node.wiped {
		return nil
	}
	return h.node.Secret
}

// NodeID 返回句柄绑定节点的标识。
func (h *Handle) NodeID() string { return h.node.ID }

// Close 退出使用，递减使用者计数；若它是撤销闭包中最后一个使用者，
// 唤醒等待中的撤销流程完成擦除。重复关闭是安全的。
func (h *Handle) Close() {
	h.once.Do(func() {
		h.graph.release(h.node)
	})
}

// Graph 是供缓存宿主调用的敏感对象依赖图。
//
// 注册时每个副本绑定来源对象与密钥代；撤销某个对象（或某一密钥代）时，
// 沿派生边计算依赖闭包，闭包内全部节点先原子标记不可用（拒绝新使用者），
// 再等待存量使用者全部退出，最后清零密钥材料并调用宿主擦除回调。
// 共享派生物声明多个来源，任一来源撤销都会使其进入闭包而失效。
type Graph struct {
	mu    sync.Mutex
	nodes map[string]*Node

	// userExit 在每次有使用者退出时广播，供撤销流程等待闭包排空。
	userExit *sync.Cond
}

// NewGraph 创建空的敏感对象依赖图。
func NewGraph() *Graph {
	g := &Graph{nodes: make(map[string]*Node)}
	g.userExit = sync.NewCond(&g.mu)
	return g
}

// RegisterSource 注册一份来源敏感对象（例如解密后的明文），绑定密钥代。
// 重复注册同一标识会返回错误。
func (g *Graph) RegisterSource(id string, generation int64, secret []byte, wipe WipeFunc) (*Node, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if _, ok := g.nodes[id]; ok {
		return nil, errors.New("netsec: duplicate object id: " + id)
	}
	n := &Node{
		ID:         id,
		Generation: generation,
		Secret:     secret,
		deps:       make(map[*Node]struct{}),
		dependents: make(map[*Node]struct{}),
		available:  true,
		wipe:       wipe,
		graph:      g,
	}
	g.nodes[id] = n
	return n, nil
}

// RegisterReplica 注册来源对象的一份副本（读缓存条目、索引缓存条目等）。
// 副本与来源绑定同一密钥代；来源撤销时副本经依赖闭包一并失效擦除。
func (g *Graph) RegisterReplica(id, sourceID string, secret []byte, wipe WipeFunc) (*Node, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	source, ok := g.nodes[sourceID]
	if !ok {
		return nil, ErrNotFound
	}
	if !source.available {
		return nil, ErrRevoked
	}
	return g.registerLocked(id, source.Generation, secret, wipe, []*Node{source})
}

// RegisterDerived 注册一个派生片段，可声明一个或多个来源对象。派生物绑定
// 来源的密钥代（要求所有来源同代；跨代派生应由宿主先重加密为同代）。
// 任一来源被撤销，该派生物都会随依赖闭包失效。
func (g *Graph) RegisterDerived(id string, sourceIDs []string, secret []byte, wipe WipeFunc) (*Node, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if len(sourceIDs) == 0 {
		return nil, errors.New("netsec: derived object requires at least one source")
	}
	sources := make([]*Node, 0, len(sourceIDs))
	var generation int64
	for i, sid := range sourceIDs {
		source, ok := g.nodes[sid]
		if !ok {
			return nil, ErrNotFound
		}
		if !source.available {
			return nil, ErrRevoked
		}
		if i == 0 {
			generation = source.Generation
		} else if source.Generation != generation {
			return nil, errors.New("netsec: derived object spans mixed key generations")
		}
		sources = append(sources, source)
	}
	return g.registerLocked(id, generation, secret, wipe, sources)
}
func (g *Graph) registerLocked(id string, generation int64, secret []byte, wipe WipeFunc, sources []*Node) (*Node, error) {
	if _, ok := g.nodes[id]; ok {
		return nil, errors.New("netsec: duplicate object id: " + id)
	}
	n := &Node{
		ID:         id,
		Generation: generation,
		Secret:     secret,
		deps:       make(map[*Node]struct{}),
		dependents: make(map[*Node]struct{}),
		available:  true,
		wipe:       wipe,
		graph:      g,
	}
	for _, s := range sources {
		n.deps[s] = struct{}{}
		s.dependents[n] = struct{}{}
	}
	g.nodes[id] = n
	return n, nil
}

// Lookup 按标识返回节点，不存在返回 ErrNotFound。
func (g *Graph) Lookup(id string) (*Node, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	n, ok := g.nodes[id]
	if !ok {
		return nil, ErrNotFound
	}
	return n, nil
}

// Len 返回图中节点总数（含已撤销、已擦除的节点）。
func (g *Graph) Len() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return len(g.nodes)
}

// Acquire 获取节点的使用句柄。节点已撤销（含正在等待使用者退出）时
// 返回 ErrRevoked，保证撤销标记之后不会有新使用者进入。
func (g *Graph) Acquire(id string) (*Handle, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	n, ok := g.nodes[id]
	if !ok {
		return nil, ErrNotFound
	}
	if !n.available {
		return nil, ErrRevoked
	}
	n.users++
	return &Handle{graph: g, node: n}, nil
}

func (g *Graph) release(n *Node) {
	g.mu.Lock()
	if n.users > 0 {
		n.users--
	}
	// 唤醒可能正在等待使用者退出的撤销流程。
	g.userExit.Broadcast()
	g.mu.Unlock()
}

// closureLocked 以 roots 为起点沿反向边（dependents）收集依赖闭包：
// 即所有直接或间接派生自 roots 的节点，连同 roots 自身。
func (g *Graph) closureLocked(roots []*Node) map[*Node]struct{} {
	closure := make(map[*Node]struct{})
	stack := append([]*Node(nil), roots...)
	for len(stack) > 0 {
		n := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if _, seen := closure[n]; seen {
			continue
		}
		closure[n] = struct{}{}
		for d := range n.dependents {
			if _, seen := closure[d]; !seen {
				stack = append(stack, d)
			}
		}
	}
	return closure
}

// Revoke 撤销单个对象及其全部依赖闭包（读缓存、索引缓存、共享派生片段等
// 所有副本）。调用阻塞至闭包内每个节点的存量使用者退出并完成擦除。
//
// 语义保证：
//   - 闭包原子标记不可用，标记后任何 Acquire 立即失败（无新使用者）；
//   - 已持有的句柄仍可读取密钥材料，直到句柄关闭，期间绝不提前清零；
//   - 仅当闭包内 users 全部归零后才统一清零并执行宿主擦除回调。
func (g *Graph) Revoke(id string) error {
	g.mu.Lock()
	root, ok := g.nodes[id]
	if !ok {
		g.mu.Unlock()
		return ErrNotFound
	}
	closure := g.closureLocked([]*Node{root})
	g.markAndWipeLocked(closure)
	return nil
}

// RevokeGeneration 撤销属于指定密钥代的全部对象及其依赖闭包。密钥轮换后
// 旧代的明文与所有副本、派生物将被一次性清理。
func (g *Graph) RevokeGeneration(generation int64) error {
	g.mu.Lock()
	var roots []*Node
	for _, n := range g.nodes {
		if n.Generation == generation && n.available {
			roots = append(roots, n)
		}
	}
	if len(roots) == 0 {
		g.mu.Unlock()
		return nil
	}
	closure := g.closureLocked(roots)
	g.markAndWipeLocked(closure)
	return nil
}

// markAndWipeLocked 先将闭包原子标记不可用，再等待闭包内所有使用者退出，
// 然后清零密钥材料，最后在锁外执行宿主擦除回调。调用方进入时持有 g.mu，
// 返回时锁已释放。
func (g *Graph) markAndWipeLocked(closure map[*Node]struct{}) {
	for n := range closure {
		n.available = false
	}
	for {
		busy := false
		for n := range closure {
			if n.users > 0 {
				busy = true
				break
			}
		}
		if !busy {
			break
		}
		g.userExit.Wait()
	}

	type pendingWipe struct {
		fn WipeFunc
		n  *Node
	}
	pending := make([]pendingWipe, 0, len(closure))
	for n := range closure {
		if n.wiped {
			continue
		}
		for i := range n.Secret {
			n.Secret[i] = 0
		}
		n.wiped = true
		if n.wipe != nil {
			pending = append(pending, pendingWipe{fn: n.wipe, n: n})
		}
	}
	g.mu.Unlock()

	// 锁外执行宿主回调，避免回调再次进入图操作时自锁或长时间持锁。
	for _, p := range pending {
		p.fn(p.n)
	}
}
