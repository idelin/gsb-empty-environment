// Package statewait 提供状态等待中枢：等待者以任意谓词注册等待条件，
// 更新方只负责提交新状态，无需了解任何等待者的谓词。
//
// 关键不变量：
//  1. 检查谓词与登记等待在同一把锁内完成，更新不会从两者之间穿过，
//     因此不存在"检查后登记"导致的永久休眠。
//  2. 每个等待者只被唤醒一次；取消与状态满足竞争时，Wait 只返回一次，
//     且结果唯一（要么成功事件，要么 ctx 错误）。
//  3. 每次状态提交都追加到事件历史，成功等待返回的 Event 必然对应
//     历史中真实可见的状态。
package statewait

import (
	"context"
	"sync"
)

// Event 是一次已提交的状态迁移。Version 从 0（初始状态）开始单调递增。
type Event struct {
	Version uint64
	State   interface{}
}

// Predicate 判断某个状态是否满足等待条件。
type Predicate func(state interface{}) bool

// waiter 是一次登记中的等待。ch 容量为 1，保证唤醒发送永不阻塞，
// 且每个 waiter 最多收到一个事件。
type waiter struct {
	pred Predicate
	ch   chan Event
}

// Hub 协调状态更新与谓词等待。
type Hub struct {
	mu      sync.Mutex
	state   interface{}
	version uint64
	history []Event
	waiters map[*waiter]struct{}
}

// New 创建以 initial 为初始状态（版本 0）的 Hub。
func New(initial interface{}) *Hub {
	return &Hub{
		state:   initial,
		history: []Event{{Version: 0, State: initial}},
		waiters: make(map[*waiter]struct{}),
	}
}

// Update 提交新状态，追加事件历史，并唤醒所有谓词被满足的等待者。
// 返回新提交的事件。
func (h *Hub) Update(next interface{}) Event {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.updateLocked(next)
}

// UpdateFunc 在同一临界区内由旧状态计算并提交新状态，
// 适合 read-modify-write 场景（如计数器自增），避免丢失更新。
func (h *Hub) UpdateFunc(fn func(old interface{}) interface{}) Event {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.updateLocked(fn(h.state))
}

func (h *Hub) updateLocked(next interface{}) Event {
	h.version++
	h.state = next
	ev := Event{Version: h.version, State: next}
	h.history = append(h.history, ev)

	for w := range h.waiters {
		if w.pred(next) {
			w.ch <- ev // 缓冲通道，且 waiter 随即注销：不会阻塞、不会重发
			delete(h.waiters, w)
		}
	}
	return ev
}

// Wait 阻塞直到 pred 被某个可见状态满足，或 ctx 结束。
// 成功时返回对应的事件（其状态必然已存在于事件历史中）。
func (h *Hub) Wait(ctx context.Context, pred Predicate) (Event, error) {
	w := &waiter{pred: pred, ch: make(chan Event, 1)}

	h.mu.Lock()
	// 检查与登记在同一临界区内：此期间不可能发生 Update，
	// 因此不会错过任何一次满足谓词的状态。
	if pred(h.state) {
		ev := Event{Version: h.version, State: h.state}
		h.mu.Unlock()
		return ev, nil
	}
	h.waiters[w] = struct{}{}
	h.mu.Unlock()

	select {
	case ev := <-w.ch:
		return ev, nil
	case <-ctx.Done():
		h.mu.Lock()
		if _, ok := h.waiters[w]; ok {
			// 取消赢了：注销后 Update 不可能再唤醒它。
			delete(h.waiters, w)
			h.mu.Unlock()
			return Event{}, ctx.Err()
		}
		h.mu.Unlock()
		// 状态满足赢了：waiter 已被 Update 注销，事件已在缓冲通道中，
		// 这里必然立即取到，不会阻塞。
		return <-w.ch, nil
	}
}

// History 返回事件历史的副本，可用于校验等待结果对应真实可见状态。
func (h *Hub) History() []Event {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := make([]Event, len(h.history))
	copy(out, h.history)
	return out
}

// Snapshot 返回当前状态及其版本。
func (h *Hub) Snapshot() Event {
	h.mu.Lock()
	defer h.mu.Unlock()
	return Event{Version: h.version, State: h.state}
}
