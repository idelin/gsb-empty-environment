package leaseaudit

import (
	"sync"
	"time"
)

// Tracker 租约争抢事件的可重放审计追踪器（黑匣子）。
// 追加式日志，并发安全；每条事件被打上 Lamport 时钟与向量时钟。
type Tracker struct {
	mu      sync.Mutex
	events  []Event
	lamport uint64
	vectors map[string]VectorClock // 每个事件主体（节点/仲裁器）的向量时钟
	now     func() time.Time
}

// NewTracker 创建追踪器。now 可注入假时钟用于测试；传 nil 使用真实时钟。
func NewTracker(now func() time.Time) *Tracker {
	if now == nil {
		now = time.Now
	}
	return &Tracker{
		vectors: make(map[string]VectorClock),
		now:     now,
	}
}

// Record 追加一条事件。调用方填充事件主体字段（NodeID/LeaseID/Type/...），
// 追踪器负责分配 Seq、Lamport、向量时钟快照与物理时间。
func (t *Tracker) Record(ev Event) Event {
	t.mu.Lock()
	defer t.mu.Unlock()

	t.lamport++
	vc, ok := t.vectors[ev.NodeID]
	if !ok {
		vc = make(VectorClock)
		t.vectors[ev.NodeID] = vc
	}
	vc.Tick(ev.NodeID)

	ev.Seq = uint64(len(t.events)) + 1
	ev.Lamport = t.lamport
	ev.Vector = vc.Copy()
	ev.WallTime = t.now()
	t.events = append(t.events, ev)
	return ev
}

// Merge 表示 nodeID 收到了来自对端的因果上下文（如仲裁器收到节点请求），
// 用于在向量时钟上建立跨节点的 happens-before 边。
func (t *Tracker) Merge(nodeID string, other VectorClock) {
	t.mu.Lock()
	defer t.mu.Unlock()
	vc, ok := t.vectors[nodeID]
	if !ok {
		vc = make(VectorClock)
		t.vectors[nodeID] = vc
	}
	vc.Merge(other)
}

// Events 返回全部事件的拷贝，按 Seq（到达次序）排列。
func (t *Tracker) Events() []Event {
	t.mu.Lock()
	defer t.mu.Unlock()
	out := make([]Event, len(t.events))
	copy(out, t.events)
	return out
}

// EventsFor 返回指定租约的全部事件。
func (t *Tracker) EventsFor(leaseID string) []Event {
	t.mu.Lock()
	defer t.mu.Unlock()
	var out []Event
	for _, e := range t.events {
		if e.LeaseID == leaseID {
			out = append(out, e)
		}
	}
	return out
}

// Get 按 Seq 取事件。
func (t *Tracker) Get(seq uint64) (Event, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if seq == 0 || seq > uint64(len(t.events)) {
		return Event{}, false
	}
	return t.events[seq-1], true
}

// HappensBefore 用向量时钟判定 seqA 是否因果先于 seqB。
func (t *Tracker) HappensBefore(seqA, seqB uint64) bool {
	a, okA := t.Get(seqA)
	b, okB := t.Get(seqB)
	if !okA || !okB {
		return false
	}
	return HappensBefore(a.Vector, b.Vector)
}

// Concurrent 判定两个事件是否因果并发（竞态判据）。
func (t *Tracker) Concurrent(seqA, seqB uint64) bool {
	a, okA := t.Get(seqA)
	b, okB := t.Get(seqB)
	if !okA || !okB {
		return false
	}
	return Concurrent(a.Vector, b.Vector)
}

// CausalChain 返回某事件的完整因果证据链：
// 沿 CausedBy 指针做传递闭包，按 Seq 升序输出全部祖先事件。
func (t *Tracker) CausalChain(seq uint64) []Event {
	t.mu.Lock()
	defer t.mu.Unlock()

	seen := make(map[uint64]bool)
	var chain []Event
	var visit func(s uint64)
	visit = func(s uint64) {
		if s == 0 || s > uint64(len(t.events)) || seen[s] {
			return
		}
		seen[s] = true
		e := t.events[s-1]
		for _, parent := range e.CausedBy {
			visit(parent)
		}
		chain = append(chain, e)
	}
	visit(seq)

	// 按 Seq 升序（即时间轴顺序）输出
	for i := 1; i < len(chain); i++ {
		for j := i; j > 0 && chain[j].Seq < chain[j-1].Seq; j-- {
			chain[j], chain[j-1] = chain[j-1], chain[j]
		}
	}
	return chain
}
