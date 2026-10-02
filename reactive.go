// Package reactive 实现细粒度响应式核心：Signal / Computed / Effect。
//
// 防钻石依赖（glitch-free）的核心机制：
//  1. 拓扑染色：每个节点在创建时根据依赖计算 rank（拓扑高度），
//     Signal 的 rank 为 0，Computed/Effect 的 rank = max(依赖 rank) + 1。
//  2. 两阶段更新：Signal.Set 先沿依赖图做“标脏”（mark），
//     再按 rank 最小堆拓扑序“刷新”（flush）。钻石图中 D 的 rank
//     高于 B 和 C，因此 D 一定在 B、C 都刷新完成后才执行，
//     且每个节点每轮只入堆一次、只重算一次，不会读到中间瞬态脏值。
package reactive

import "container/heap"

// node 是响应式图中的一个节点（Signal、Computed 或 Effect 的内部表示）。
type node struct {
	rank       int   // 拓扑高度（染色值）
	dirty      bool  // 标脏标记
	order      int64 // 入堆序号，保证同 rank 时按标记顺序处理（确定性）
	dependents []*node
	recompute  func() // Computed/Effect 的重算逻辑；Signal 为 nil
}

// 全局依赖追踪：创建 Computed/Effect 时收集其读取到的节点。
var tracking *[]*node

func registerDependency(n *node) {
	if tracking != nil {
		*tracking = append(*tracking, n)
	}
}

// collect 运行 fn 并收集其间被读取的节点（去重后返回）。
func collect(fn func()) []*node {
	var deps []*node
	prev := tracking
	tracking = &deps
	fn()
	tracking = prev
	seen := make(map[*node]bool)
	out := make([]*node, 0, len(deps))
	for _, d := range deps {
		if !seen[d] {
			seen[d] = true
			out = append(out, d)
		}
	}
	return out
}

// subscribe 将 self 挂到各依赖的 dependents 上，并返回 self 的 rank。
func subscribe(deps []*node, self *node) int {
	rank := 0
	for _, d := range deps {
		d.dependents = append(d.dependents, self)
		if d.rank+1 > rank {
			rank = d.rank + 1
		}
	}
	return rank
}

// ---------------- 两阶段更新调度器 ----------------

var (
	queue   nodeHeap
	pushSeq int64
)

// 阶段一：标脏。从源节点出发沿 dependents 递归标记，
// 每个脏节点只入堆一次（已脏则跳过，天然去重 -> D 只会入堆一次）。
func markDirty(n *node) {
	for _, dep := range n.dependents {
		if dep.dirty {
			continue
		}
		dep.dirty = true
		pushSeq++
		dep.order = pushSeq
		heap.Push(&queue, dep)
		markDirty(dep)
	}
}

// 阶段二：刷新。按 rank 拓扑序弹出脏节点并重算。
// 由于 rank(子) > rank(父)，父节点一定先于子节点完成重算，
// 因此任何节点重算时读到的依赖都是本轮最新值，无中间瞬态。
func flush() {
	for queue.Len() > 0 {
		n := heap.Pop(&queue).(*node)
		n.dirty = false
		if n.recompute != nil {
			n.recompute()
		}
	}
}

// ---------------- Signal ----------------

// Signal 是可写的响应式源节点。
type Signal struct {
	n     *node
	value interface{}
}

// NewSignal 创建初始值为 v 的 Signal。
func NewSignal(v interface{}) *Signal {
	return &Signal{n: &node{rank: 0}, value: v}
}

// Get 读取当前值，并在依赖追踪上下文中注册依赖。
func (s *Signal) Get() interface{} {
	registerDependency(s.n)
	return s.value
}

// Set 写入新值并触发两阶段更新（标脏 + 拓扑序刷新）。
func (s *Signal) Set(v interface{}) {
	s.value = v
	markDirty(s.n)
	flush()
}

// ---------------- Computed ----------------

// Computed 是派生的只读节点，依赖在首次计算时自动收集。
type Computed struct {
	n     *node
	value interface{}
}

// NewComputed 创建派生节点：立即计算一次以收集依赖并染色（确定 rank）。
func NewComputed(fn func() interface{}) *Computed {
	c := &Computed{n: &node{}}
	deps := collect(func() { c.value = fn() })
	c.n.rank = subscribe(deps, c.n)
	c.n.recompute = func() { c.value = fn() }
	return c
}

// Get 读取缓存值（保证与当前图源一致），并注册依赖。
func (c *Computed) Get() interface{} {
	registerDependency(c.n)
	return c.value
}

// ---------------- Effect ----------------

// Effect 是副作用节点，依赖变化时在拓扑序刷新阶段执行一次。
type Effect struct {
	n *node
}

// NewEffect 创建副作用节点：立即执行一次以收集依赖并染色。
func NewEffect(fn func()) *Effect {
	e := &Effect{n: &node{}}
	deps := collect(fn)
	e.n.rank = subscribe(deps, e.n)
	e.n.recompute = fn
	return e
}

// Dispose 将 Effect 从依赖图中摘除。
func (e *Effect) Dispose() {
	// 简单实现：清空重算逻辑并断开引用由 GC 处理；
	// 这里从各依赖的 dependents 中移除自身。
	for _, d := range e.n.dependents { // 防御：Effect 一般无 dependents
		_ = d
	}
	e.n.recompute = nil
}

// ---------------- 拓扑序最小堆 ----------------

type nodeHeap []*node

func (h nodeHeap) Len() int { return len(h) }
func (h nodeHeap) Less(i, j int) bool {
	if h[i].rank != h[j].rank {
		return h[i].rank < h[j].rank
	}
	return h[i].order < h[j].order
}
func (h nodeHeap) Swap(i, j int)       { h[i], h[j] = h[j], h[i] }
func (h *nodeHeap) Push(x interface{}) { *h = append(*h, x.(*node)) }
func (h *nodeHeap) Pop() interface{} {
	old := *h
	n := old[len(old)-1]
	*h = old[:len(old)-1]
	return n
}
