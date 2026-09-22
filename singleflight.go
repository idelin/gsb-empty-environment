// Package singleflight 提供同键并发调用的去重（共享一次计算），
// 并支持每个等待者独立取消。
//
// 语义：
//   - 同一 key 的并发 Do 调用共享同一次 fn 执行，所有等待者拿到相同结果。
//   - 每个等待者可通过自己的 context 独立取消，不影响其他等待者。
//   - 当所有等待者都取消后，计算的 context 被取消（终止计算），
//     该 key 的调用记录被移除，后续调用会重新执行 fn。
//   - fn 完成（无论成功或失败）后，调用记录先从映射中移除再唤醒等待者，
//     因此完成与新调用的竞态不会复用已关闭的失败状态。
//   - fn 发生 panic 时转换为 *PanicError 并释放所有等待者。
package singleflight

import (
	"context"
	"fmt"
	"sync"
)

// PanicError 是 fn panic 时返回给所有等待者的错误。
type PanicError struct {
	Value any
}

func (e *PanicError) Error() string {
	return fmt.Sprintf("singleflight: shared call panicked: %v", e.Value)
}

// call 表示一次进行中的共享计算。
type call struct {
	done chan struct{} // 计算结束时关闭
	val  any
	err  error

	refs   int                // 当前等待者数量（含发起者），受 mu 保护
	cancel context.CancelFunc // 取消计算 context
}

// Group 是单键去重的调用组，零值即可使用。
type Group struct {
	mu sync.Mutex
	m  map[string]*call
}

// Do 以 key 去重执行 fn。fn 收到的 context 在所有等待者取消时被取消。
// 调用者自己的 ctx 取消时返回 ctx.Err()，不影响其他等待者。
func (g *Group) Do(ctx context.Context, key string, fn func(context.Context) (any, error)) (any, error) {
	g.mu.Lock()
	if g.m == nil {
		g.m = make(map[string]*call)
	}
	if c, ok := g.m[key]; ok {
		c.refs++
		g.mu.Unlock()
		return g.wait(ctx, key, c)
	}
	callCtx, cancel := context.WithCancel(context.Background())
	c := &call{done: make(chan struct{}), refs: 1, cancel: cancel}
	g.m[key] = c
	g.mu.Unlock()

	go g.run(key, c, callCtx, fn)
	return g.wait(ctx, key, c)
}

// run 执行 fn，把 panic 转成错误，然后先从映射移除再关闭 done，
// 保证新调用不会附着到已关闭（尤其已失败）的调用上。
func (g *Group) run(key string, c *call, ctx context.Context, fn func(context.Context) (any, error)) {
	defer func() {
		if r := recover(); r != nil {
			c.val = nil
			c.err = &PanicError{Value: r}
		}
		g.mu.Lock()
		if g.m[key] == c {
			delete(g.m, key)
		}
		g.mu.Unlock()
		close(c.done)
		c.cancel()
	}()
	c.val, c.err = fn(ctx)
}

// wait 等待共享结果或调用者自身取消。
func (g *Group) wait(ctx context.Context, key string, c *call) (any, error) {
	select {
	case <-c.done:
		return c.val, c.err
	case <-ctx.Done():
		g.mu.Lock()
		c.refs--
		if c.refs == 0 {
			// 最后一个等待者离开：终止计算并移除记录，
			// 后续同 key 调用会重新执行。
			if g.m[key] == c {
				delete(g.m, key)
			}
			c.cancel()
		}
		g.mu.Unlock()
		return nil, ctx.Err()
	}
}
