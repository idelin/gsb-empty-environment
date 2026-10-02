// Package suspense 提供与异步 Promise 结合的响应式资源绑定器。
//
// 核心能力：
//   - 追踪异步请求状态：Pending / Success / Error
//   - 数据就绪前向下游传播 Pending 信标
//   - 并发请求竞争时自动作废（discard）旧请求的迟到结果
package suspense

import (
	"context"
	"sync"
)

// State 是资源状态机的三个状态。
type State int

const (
	StatePending State = iota // 请求已发出，数据未就绪
	StateSuccess              // 数据就绪
	StateError                // 请求失败
)

func (s State) String() string {
	switch s {
	case StatePending:
		return "Pending"
	case StateSuccess:
		return "Success"
	case StateError:
		return "Error"
	default:
		return "Unknown"
	}
}

// Snapshot 是资源某一时刻的不可变快照，向下游传播。
type Snapshot[T any] struct {
	State State
	Value T      // 仅 State == StateSuccess 时有效
	Err   error  // 仅 State == StateError 时有效
	Seq   uint64 // 产生该快照的请求序号，单调递增
}

// Signal 是响应式数据源：值变化时通知所有订阅者。
type Signal[T any] struct {
	mu     sync.Mutex
	val    T
	subs   map[uint64]func(T)
	nextID uint64
}

// NewSignal 以初始值创建信号。
func NewSignal[T any](v T) *Signal[T] {
	return &Signal[T]{val: v, subs: make(map[uint64]func(T))}
}

// Get 读取当前值。
func (s *Signal[T]) Get() T {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.val
}

// Set 写入新值并同步通知所有订阅者。
func (s *Signal[T]) Set(v T) {
	s.mu.Lock()
	s.val = v
	fns := make([]func(T), 0, len(s.subs))
	for _, fn := range s.subs {
		fns = append(fns, fn)
	}
	s.mu.Unlock()
	for _, fn := range fns {
		fn(v)
	}
}

// Subscribe 订阅变化，返回取消订阅函数。
func (s *Signal[T]) Subscribe(fn func(T)) (unsubscribe func()) {
	s.mu.Lock()
	s.nextID++
	id := s.nextID
	s.subs[id] = fn
	s.mu.Unlock()
	return func() {
		s.mu.Lock()
		delete(s.subs, id)
		s.mu.Unlock()
	}
}

// Resource 是响应式资源绑定器。
// P 为查询参数类型，T 为资源数据类型。
type Resource[P, T any] struct {
	fetch func(ctx context.Context, p P) (T, error)

	mu        sync.Mutex
	seq       uint64             // 当前最新请求序号
	snap      Snapshot[T]        // 当前快照
	changed   chan struct{}      // 每次提交快照时关闭并重建，用于 Await 唤醒
	cancel    context.CancelFunc // 当前在飞请求的取消函数
	listeners map[uint64]func(Snapshot[T])
	nextID    uint64
	discarded uint64 // 被作废的迟到结果计数
}

// NewResource 以异步 fetch 函数创建资源。fetch 必须尊重 ctx 取消。
func NewResource[P, T any](fetch func(ctx context.Context, p P) (T, error)) *Resource[P, T] {
	return &Resource[P, T]{
		fetch:     fetch,
		changed:   make(chan struct{}),
		listeners: make(map[uint64]func(Snapshot[T])),
	}
}

// Track 将资源绑定到响应式参数信号：
// 立即以当前参数发起首次加载，之后参数每次变化都会触发重新加载，
// 并自动作废旧请求。
func (r *Resource[P, T]) Track(params *Signal[P]) (unsubscribe func()) {
	r.load(params.Get())
	return params.Subscribe(r.load)
}

// load 发起一次新请求：作废旧请求、广播 Pending 信标、异步执行 fetch。
func (r *Resource[P, T]) load(p P) {
	r.mu.Lock()
	if r.cancel != nil {
		r.cancel() // 取消旧请求（协作式作废，配合序号检查兜底）
	}
	r.seq++
	seq := r.seq
	ctx, cancel := context.WithCancel(context.Background())
	r.cancel = cancel
	r.commitLocked(Snapshot[T]{State: StatePending, Seq: seq})
	r.mu.Unlock()

	go func() {
		v, err := r.fetch(ctx, p)

		r.mu.Lock()
		if seq != r.seq {
			// 竞争检测：已有更新的请求发出，该结果为旧 Promise 的迟到结果，作废。
			r.discarded++
			r.mu.Unlock()
			return
		}
		var snap Snapshot[T]
		if err != nil {
			snap = Snapshot[T]{State: StateError, Err: err, Seq: seq}
		} else {
			snap = Snapshot[T]{State: StateSuccess, Value: v, Seq: seq}
		}
		r.commitLocked(snap)
		r.mu.Unlock()
	}()
}

// commitLocked 在持锁状态下提交快照：更新状态、唤醒 Await、通知下游订阅者。
// 订阅者回调在锁内同步执行，保证状态传播顺序严格单调（Seq 递增）。
func (r *Resource[P, T]) commitLocked(snap Snapshot[T]) {
	r.snap = snap
	close(r.changed)
	r.changed = make(chan struct{})
	for _, fn := range r.listeners {
		fn(snap)
	}
}

// Snapshot 返回当前快照。
func (r *Resource[P, T]) Snapshot() Snapshot[T] {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.snap
}

// Subscribe 订阅状态传播（含 Pending 信标），返回取消订阅函数。
// 回调按 Seq 严格递增的顺序被同步调用。
func (r *Resource[P, T]) Subscribe(fn func(Snapshot[T])) (unsubscribe func()) {
	r.mu.Lock()
	r.nextID++
	id := r.nextID
	r.listeners[id] = fn
	r.mu.Unlock()
	return func() {
		r.mu.Lock()
		delete(r.listeners, id)
		r.mu.Unlock()
	}
}

// Await 阻塞直到资源脱离 Pending（Success 或 Error），或 ctx 到期。
func (r *Resource[P, T]) Await(ctx context.Context) (Snapshot[T], error) {
	for {
		r.mu.Lock()
		if r.snap.State != StatePending {
			snap := r.snap
			r.mu.Unlock()
			return snap, nil
		}
		ch := r.changed
		r.mu.Unlock()

		select {
		case <-ch:
		case <-ctx.Done():
			return Snapshot[T]{}, ctx.Err()
		}
	}
}

// Discarded 返回因竞争被作废的迟到结果数量。
func (r *Resource[P, T]) Discarded() uint64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.discarded
}
