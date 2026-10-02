// Package reactive 实现细粒度响应式核心：Signal / Computed / Effect。
//
// 当依赖图出现钻石结构（A -> B, A -> C, (B,C) -> D）时，通过
// 拓扑染色（clean/check/dirty）与两阶段更新保证：
//   1. D 每次源变更只计算一次；
//   2. D 读到的 B 与 C 均为最新版本，不会看到中间瞬态脏值（glitch-free）。
//
// 两阶段更新：
//   阶段一（染色）：从变更的 Signal 出发向下游传播标记——直接订阅者
//     标为 dirty（直接依赖变了），更远的下游标为 check（可能脏）。
//   阶段二（结算）：按拓扑层级（level）从小到大处理队列，computed 优先于
//     effect。处理 check 节点时先让其依赖递归结算，再比对依赖版本号，
//     仅当依赖真的变化时才重算。因此 D 被处理时 B、C 已结算完毕，
//     D 只重算一次且读到一致快照。
package reactive

import "reflect"

// 拓扑染色状态
const (
	stateClean = iota // 最新
	stateCheck        // 可能过期：某个间接依赖变化了，需要核对版本
	stateDirty        // 确定过期：某个直接依赖变化了
)

// source 是可被计算节点依赖的值的来源（Signal 或 Computed）。
type source interface {
	getVersion() uint64
	getLevel() int
	subscribe(c *computed)
	unsubscribe(c *computed)
}

// observerStack 用于动态依赖追踪：计算执行期间读到的 source 会被记录。
var observerStack []*computed

func track(s source) {
	n := len(observerStack)
	if n == 0 {
		return
	}
	observerStack[n-1].addDep(s)
}

// ---------- Signal ----------

// Signal 是响应式图的源节点，持有可写的值。
type Signal struct {
	val     interface{}
	version uint64
	subs    map[*computed]struct{}
}

// NewSignal 创建一个持有初始值的 Signal。
func NewSignal(v interface{}) *Signal {
	return &Signal{val: v, subs: make(map[*computed]struct{})}
}

// Get 读取当前值；若在 Computed/Effect 计算过程中调用，则建立依赖边。
func (s *Signal) Get() interface{} {
	track(s)
	return s.val
}

// Set 写入新值并触发两阶段更新。值未变化（DeepEqual）时不做任何事。
func (s *Signal) Set(v interface{}) {
	if reflect.DeepEqual(s.val, v) {
		return
	}
	s.val = v
	s.version++
	// 阶段一：拓扑染色
	for sub := range s.subs {
		sub.markDirty()
	}
	// 阶段二：按拓扑序结算
	flush()
}

func (s *Signal) getVersion() uint64      { return s.version }
func (s *Signal) getLevel() int           { return 0 }
func (s *Signal) subscribe(c *computed)   { s.subs[c] = struct{}{} }
func (s *Signal) unsubscribe(c *computed) { delete(s.subs, c) }

// ---------- 内部计算节点（Computed 与 Effect 共用） ----------

type computed struct {
	fn      func() interface{}
	effect  bool
	val     interface{}
	version uint64
	level   int // 拓扑层级 = max(依赖层级)+1，Signal 为 0
	state   int
	queued  bool

	deps     []source
	depVers  []uint64 // 上次计算时各依赖的版本号
	building map[source]struct{}
	subs     map[*computed]struct{}
}

func newComputed(fn func() interface{}, effect bool) *computed {
	c := &computed{fn: fn, effect: effect, subs: make(map[*computed]struct{})}
	c.recompute()
	return c
}

func (c *computed) addDep(s source) {
	if c.building == nil {
		return
	}
	if _, ok := c.building[s]; ok {
		return
	}
	c.building[s] = struct{}{}
	c.deps = append(c.deps, s)
}

// recompute 重新执行计算函数，重建依赖边、记录依赖版本、更新拓扑层级。
func (c *computed) recompute() {
	for _, d := range c.deps {
		d.unsubscribe(c)
	}
	c.deps = c.deps[:0]
	c.depVers = c.depVers[:0]
	c.building = make(map[source]struct{})

	observerStack = append(observerStack, c)
	v := c.fn()
	observerStack = observerStack[:len(observerStack)-1]
	c.building = nil

	maxLevel := 0
	for _, d := range c.deps {
		d.subscribe(c)
		c.depVers = append(c.depVers, d.getVersion())
		if l := d.getLevel(); l > maxLevel {
			maxLevel = l
		}
	}
	// 值真正变化才提升版本号，下游 check 节点因此可避免无谓重算。
	if c.version == 0 || !reflect.DeepEqual(c.val, v) {
		c.val = v
		c.version++
	}
	c.state = stateClean
	if lvl := maxLevel + 1; lvl != c.level {
		c.level = lvl
		for sub := range c.subs {
			sub.updateLevel()
		}
	}
}

func (c *computed) updateLevel() {
	maxLevel := 0
	for _, d := range c.deps {
		if l := d.getLevel(); l > maxLevel {
			maxLevel = l
		}
	}
	if lvl := maxLevel + 1; lvl != c.level {
		c.level = lvl
		for sub := range c.subs {
			sub.updateLevel()
		}
	}
}

// markDirty：直接依赖已变化。向下游传播 check。
func (c *computed) markDirty() {
	if c.state == stateDirty {
		return
	}
	c.state = stateDirty
	for sub := range c.subs {
		sub.markCheck()
	}
	enqueue(c)
}

// markCheck：某个间接依赖可能变化。向下游继续传播 check。
func (c *computed) markCheck() {
	if c.state != stateClean {
		return
	}
	c.state = stateCheck
	for sub := range c.subs {
		sub.markCheck()
	}
	enqueue(c)
}

// refreshIfStale 按需（惰性）结算：先让依赖结算，再决定自己是否重算。
func (c *computed) refreshIfStale() {
	if c.state == stateClean {
		return
	}
	if c.state == stateCheck {
		for _, d := range c.deps {
			if dc, ok := d.(*computed); ok {
				dc.refreshIfStale()
			}
		}
		if !c.depsChanged() {
			c.state = stateClean
			return
		}
	}
	c.recompute()
}

func (c *computed) depsChanged() bool {
	for i, d := range c.deps {
		if d.getVersion() != c.depVers[i] {
			return true
		}
	}
	return false
}

func (c *computed) getVersion() uint64      { return c.version }
func (c *computed) getLevel() int           { return c.level }
func (c *computed) subscribe(s *computed)   { c.subs[s] = struct{}{} }
func (c *computed) unsubscribe(s *computed) { delete(c.subs, s) }

// ---------- 调度器（阶段二） ----------

var (
	queue    []*computed
	flushing bool
)

func enqueue(c *computed) {
	if c.queued {
		return
	}
	c.queued = true
	queue = append(queue, c)
}

// flush 按拓扑层级从小到大结算队列；computed 一律先于 effect。
// 无任何订阅者的 computed 被跳过，保持脏标记，待 Get 时惰性结算（按需更新）。
func flush() {
	if flushing {
		return
	}
	flushing = true
	defer func() { flushing = false }()
	for len(queue) > 0 {
		c := popNext()
		c.queued = false
		if c.state == stateClean {
			continue
		}
		if !c.effect && len(c.subs) == 0 {
			continue
		}
		c.refreshIfStale()
	}
}

func popNext() *computed {
	best := 0
	for i := 1; i < len(queue); i++ {
		if less(queue[i], queue[best]) {
			best = i
		}
	}
	c := queue[best]
	queue = append(queue[:best], queue[best+1:]...)
	return c
}

func less(a, b *computed) bool {
	if a.effect != b.effect {
		return !a.effect // 非 effect 优先
	}
	return a.level < b.level
}

// ---------- Computed ----------

// Computed 是由其他节点派生的只读节点，带缓存，按需重算。
type Computed struct {
	inner *computed
}

// NewComputed 创建并立即计算一次派生值。
func NewComputed(fn func() interface{}) *Computed {
	return &Computed{inner: newComputed(fn, false)}
}

// Get 读取派生值；过期时先惰性结算；在计算过程中调用会建立依赖边。
func (c *Computed) Get() interface{} {
	c.inner.refreshIfStale()
	track(c.inner)
	return c.inner.val
}

// ---------- Effect ----------

// Effect 是响应式图的汇节点，依赖变化后自动重跑。
type Effect struct {
	inner *computed
}

// NewEffect 创建并立即运行一次副作用。
func NewEffect(fn func()) *Effect {
	return &Effect{inner: newComputed(func() interface{} { fn(); return nil }, true)}
}

// Dispose 解除该 Effect 的所有依赖订阅，之后不再被触发。
func (e *Effect) Dispose() {
	for _, d := range e.inner.deps {
		d.unsubscribe(e.inner)
	}
	e.inner.deps = nil
	e.inner.depVers = nil
}
