// Package singleflight 提供带“键迁移（升级）”能力的单飞请求管理。
//
// 基本语义与 golang.org/x/sync/singleflight 一致：同一键上的并发调用
// 只执行一次，其余调用作为等待者共享结果。在此之上，运行中的任务可以
// 通过 Promote 把自己从临时键升级到正式键：
//
//   - 升级后旧键立即释放，旧键上的新请求会开启一个明确的新代次，
//     而不会挂到已迁走的航班上；
//   - 若正式键上已有任务，两个航班的等待者被合并到正式键的既有航班，
//     被合并航班的任务仍执行完毕，结果仅返回给它自己的发起者；
//   - 等待者可随时通过 context 取消，取消只摘除自己，不影响其他等待者。
//
// 无死锁迁移协议：所有元数据操作只使用一把组级互斥锁；持锁期间绝不
// 执行用户函数，也绝不进行可能阻塞的通道发送（等待者通道缓冲为 1 且
// 每个等待者至多被投递一次）。因此任意并发迁移（包括交叉迁移）都是
// 可串行化的，不存在锁序问题。
package singleflight

import (
	"context"
	"sync"
)

// Result 是一次 Do 调用的结果。
type Result struct {
	Val    interface{}
	Err    error
	Shared bool   // 为 true 表示结果共享自其他执行（本调用是等待者）
	Gen    uint64 // 产生结果的航班代次
}

// waiter 是一个等待中的调用。ch 缓冲为 1，航班完成时至多投递一次。
type waiter struct {
	ch     chan Result
	flight *flight // 当前所属航班，合并时会在持锁状态下更新
}

// flight 是某一代次的一次执行及其等待者集合。
type flight struct {
	gen     uint64
	key     string // 当前挂载的键；被合并到其他航班后置空
	done    bool
	waiters map[*waiter]struct{}
}

// Group 是单飞请求管理器。
type Group struct {
	mu      sync.Mutex
	flights map[string]*flight
	nextGen uint64
}

// NewGroup 创建一个空的 Group。
func NewGroup() *Group {
	return &Group{flights: make(map[string]*flight)}
}

// Handle 由 Do 传给正在执行的用户函数，代表本次执行所在的航班。
type Handle struct {
	g *Group
	f *flight
}

// Gen 返回本航班的代次。
func (h *Handle) Gen() uint64 { return h.f.gen }

// Key 返回本航班当前挂载的键（迁移后会变化）。
func (h *Handle) Key() string {
	h.g.mu.Lock()
	defer h.g.mu.Unlock()
	return h.f.key
}

// Promote 把本航班从当前键迁移到 newKey，语义同 Group.Promote。
func (h *Handle) Promote(newKey string) bool {
	h.g.mu.Lock()
	defer h.g.mu.Unlock()
	return h.g.promoteLocked(h.f, newKey)
}

// Do 对 key 执行 fn。若 key 上已有进行中的航班，则作为等待者挂到该
// 航班上共享其结果；否则开启新代次并亲自执行 fn。ctx 只影响等待者：
// 等待被取消时仅摘除自己，不影响其他等待者与执行中的任务。
func (g *Group) Do(ctx context.Context, key string, fn func(context.Context, *Handle) (interface{}, error)) Result {
	g.mu.Lock()
	if f, ok := g.flights[key]; ok {
		w := &waiter{ch: make(chan Result, 1), flight: f}
		f.waiters[w] = struct{}{}
		gen := f.gen
		g.mu.Unlock()

		select {
		case r := <-w.ch:
			r.Shared = true
			return r
		case <-ctx.Done():
			g.mu.Lock()
			if _, ok := w.flight.waiters[w]; ok {
				delete(w.flight.waiters, w)
				g.mu.Unlock()
				return Result{Err: ctx.Err(), Gen: gen}
			}
			g.mu.Unlock()
			// 航班已完成，结果已在缓冲通道中。
			r := <-w.ch
			r.Shared = true
			return r
		}
	}

	g.nextGen++
	f := &flight{gen: g.nextGen, key: key, waiters: make(map[*waiter]struct{})}
	g.flights[key] = f
	g.mu.Unlock()

	val, err := fn(ctx, &Handle{g: g, f: f})
	res := Result{Val: val, Err: err, Gen: f.gen}
	g.complete(f, res)
	return res
}

// complete 结束航班：把结果投递给所有等待者，并从键映射中摘除。
func (g *Group) complete(f *flight, res Result) {
	g.mu.Lock()
	f.done = true
	for w := range f.waiters {
		w.ch <- res // 缓冲为 1 且每个等待者只投递一次，绝不阻塞
	}
	f.waiters = nil
	if f.key != "" {
		if cur, ok := g.flights[f.key]; ok && cur == f {
			delete(g.flights, f.key)
		}
	}
	g.mu.Unlock()
}

// Promote 把 oldKey 上正在进行的航班迁移到 newKey。oldKey 上没有进行
// 中的航班、或 oldKey == newKey 时返回 false。
func (g *Group) Promote(oldKey, newKey string) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	f, ok := g.flights[oldKey]
	if !ok {
		return false
	}
	return g.promoteLocked(f, newKey)
}

// promoteLocked 是迁移协议的核心，调用方必须持有 g.mu。
func (g *Group) promoteLocked(f *flight, newKey string) bool {
	oldKey := f.key
	if f.done || oldKey == "" || oldKey == newKey {
		return false
	}
	// 旧键立即释放：此后旧键上的新请求将开启一个明确的新代次。
	delete(g.flights, oldKey)

	if dst, ok := g.flights[newKey]; ok {
		// 正式键已有任务：把本航班的等待者合并进既有航班。
		// 本任务继续执行完毕，结果仅返回给它自己的发起者。
		for w := range f.waiters {
			w.flight = dst
			dst.waiters[w] = struct{}{}
		}
		f.waiters = make(map[*waiter]struct{})
		f.key = ""
		return true
	}

	f.key = newKey
	g.flights[newKey] = f
	return true
}
