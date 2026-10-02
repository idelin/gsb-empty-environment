// Package reactive 实现一个带事务批处理（Batch）的响应式系统。
//
// 核心概念：
//   - Signal：可写的响应式数据源。
//   - Computed：由一个或多个 Signal/Computed 派生的只读计算值，惰性求值并缓存。
//   - Effect：依赖变化时自动重新执行的副作用函数。
//   - Batch：事务批处理。块内对 Signal 的所有写入只会让下游
//     Computed/Effect 静默挂起，直到 batch 正常退出时才一次性级联刷新。
package reactive

import "reflect"

// ---------------------------------------------------------------------------
// 全局调度状态
// ---------------------------------------------------------------------------

var (
	// batchDepth > 0 表示当前处于 Batch 块内。
	batchDepth int
	// pendingEffects 是 batch 期间被挂起的 Effect 队列（去重）。
	pendingEffects []*Effect
	pendingSet     = map[*Effect]struct{}{}
	// activeObserver 用于依赖自动收集：正在执行的 Computed/Effect。
	activeObserver observer
)

// source 是可被订阅的响应式节点（Signal / Computed）。
type source interface {
	removeSub(o observer)
}

// observer 是依赖变化的接收方（Computed / Effect）。
type observer interface {
	onSourceChanged()
	addDep(src source)
}

// ---------------------------------------------------------------------------
// Batch：事务批处理
// ---------------------------------------------------------------------------

// Batch 在事务块内执行 fn。块内所有 Signal 写入引起的下游更新都会被
// 静默挂起；fn 正常返回后，所有挂起的 Effect 被去重并各执行一次，
// 此时它们读取到的全部是 batch 结束后的最新值。
//
// 支持嵌套：只有最外层 Batch 退出时才触发刷新。
// 若 fn 发生 panic，batch 深度会被正确恢复，且挂起的更新不会触发。
func Batch(fn func()) {
	batchDepth++
	defer func() { batchDepth-- }()
	fn()
	if batchDepth == 1 {
		flush()
	}
}

// flush 循环执行所有挂起的 Effect。Effect 执行期间若又产生新的
// 挂起更新（例如 Effect 内写 Signal），会继续循环直至队列清空。
func flush() {
	for len(pendingEffects) > 0 {
		effects := pendingEffects
		pendingEffects = nil
		pendingSet = map[*Effect]struct{}{}
		for _, e := range effects {
			e.run()
		}
	}
}

// schedule 调度一个 Effect：入队去重；不在任何通知/批处理边界内时立即刷新。
// 每次 Signal.Set 的通知本身就是一个隐式批处理边界，因此沿多条路径
// （直接依赖 + 经由 Computed 间接依赖）到达同一 Effect 的通知只会执行一次。
func schedule(e *Effect) {
	if _, ok := pendingSet[e]; !ok {
		pendingSet[e] = struct{}{}
		pendingEffects = append(pendingEffects, e)
	}
	if batchDepth == 0 {
		flush()
	}
}

// ---------------------------------------------------------------------------
// Signal
// ---------------------------------------------------------------------------

// Signal 是可写的响应式数据源。
type Signal struct {
	value interface{}
	subs  map[observer]struct{}
}

// NewSignal 创建一个初始值为 v 的 Signal。
func NewSignal(v interface{}) *Signal {
	return &Signal{value: v}
}

// Get 读取当前值，并向正在执行的 Computed/Effect 注册依赖。
func (s *Signal) Get() interface{} {
	subscribe(s, &s.subs)
	return s.value
}

// Set 写入新值。值未变化时不做任何通知；否则通知所有订阅者。
// 通知过程构成一个隐式批处理边界：Computed 只标脏不重算，
// Effect 去重入队，待本次级联全部标记完成后统一执行，
// 保证无中间态（glitch-free）且每个 Effect 至多执行一次。
func (s *Signal) Set(v interface{}) {
	if reflect.DeepEqual(s.value, v) {
		return
	}
	s.value = v
	batchDepth++
	for o := range s.subs {
		o.onSourceChanged()
	}
	batchDepth--
	if batchDepth == 0 {
		flush()
	}
}

func (s *Signal) removeSub(o observer) { delete(s.subs, o) }

// subscribe 将当前活动的 observer 注册为 src 的订阅者。
func subscribe(src source, subs *map[observer]struct{}) {
	if activeObserver == nil {
		return
	}
	if *subs == nil {
		*subs = map[observer]struct{}{}
	}
	(*subs)[activeObserver] = struct{}{}
	activeObserver.addDep(src)
}

// ---------------------------------------------------------------------------
// Computed
// ---------------------------------------------------------------------------

// Computed 是派生的只读响应式值：依赖变化时标记为脏，
// 下次读取时惰性重算并缓存。
type Computed struct {
	fn    func() interface{}
	value interface{}
	dirty bool
	subs  map[observer]struct{}
	deps  []source
}

// NewComputed 创建 Computed 并立即完成首次求值。
func NewComputed(fn func() interface{}) *Computed {
	c := &Computed{fn: fn, dirty: true}
	c.recompute()
	return c
}

// Get 读取计算值（必要时惰性重算），并注册依赖。
func (c *Computed) Get() interface{} {
	subscribe(c, &c.subs)
	if c.dirty {
		c.recompute()
	}
	return c.value
}

// recompute 重新执行计算函数，并重新收集依赖。
func (c *Computed) recompute() {
	for _, d := range c.deps {
		d.removeSub(c)
	}
	c.deps = c.deps[:0]
	prev := activeObserver
	activeObserver = c
	c.value = c.fn()
	activeObserver = prev
	c.dirty = false
}

// onSourceChanged 上游变化：标记自身为脏并把脏标记向下游传播。
// 注意这里不立即重算，保证 batch 期间完全静默。
func (c *Computed) onSourceChanged() {
	if c.dirty {
		return
	}
	c.dirty = true
	for o := range c.subs {
		o.onSourceChanged()
	}
}

func (c *Computed) removeSub(o observer) { delete(c.subs, o) }
func (c *Computed) addDep(src source)    { c.deps = append(c.deps, src) }

// ---------------------------------------------------------------------------
// Effect
// ---------------------------------------------------------------------------

// Effect 是依赖变化时自动重跑的副作用。
type Effect struct {
	fn   func()
	deps []source
}

// NewEffect 创建 Effect 并立即执行一次以收集依赖。
func NewEffect(fn func()) *Effect {
	e := &Effect{fn: fn}
	e.run()
	return e
}

// run 重新执行副作用函数，并重新收集依赖。
func (e *Effect) run() {
	for _, d := range e.deps {
		d.removeSub(e)
	}
	e.deps = e.deps[:0]
	prev := activeObserver
	activeObserver = e
	e.fn()
	activeObserver = prev
}

// onSourceChanged 上游变化：交给调度器（batch 内挂起，否则立即执行）。
func (e *Effect) onSourceChanged() { schedule(e) }

func (e *Effect) addDep(src source) { e.deps = append(e.deps, src) }
