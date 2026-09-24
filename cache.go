// Package gencache 提供基于代际（generation）控制的并发缓存。
//
// 核心语义：
//   - 每次加载在启动时记录当前 (keyGen, globalGen) 代际快照；
//     加载完成时仅当两代际均未变化才把结果写入缓存，
//     因此 Invalidate/Clear 之后完成的旧加载不会复活旧值。
//   - 不同键的加载完全独立，互不阻塞；同一键的并发 Get 共享同一次加载。
//   - Invalidate/Clear 会使在途加载与缓存脱钩：旧加载继续运行直至结束，
//     但其结果不再写入缓存；新的 Get 会立即启动新加载，无需等待旧加载结束。
//   - 等待者语义：Get 的调用者（等待者）总是收到它所加入的那一次加载的
//     结果（值与错误），无论该结果最终是否被缓存。
//   - 数据源 (Loader)、时钟 (Clock) 与失败处理 (OnError) 均为可注入函数，
//     不依赖任何远程缓存。
package gencache

import (
	"context"
	"errors"
	"sync"
	"time"
)

// ErrClosed 在缓存已关闭后调用 Get 时返回。
var ErrClosed = errors.New("gencache: cache is closed")

// Loader 是数据源函数，按键加载值。可注入，便于测试与替换实现。
type Loader func(ctx context.Context, key string) (any, error)

// Clock 返回当前时间，用于 TTL 过期判定。可注入，便于测试。
type Clock func() time.Time

// ErrorHandler 在加载失败时被调用（在锁外执行）。
type ErrorHandler func(key string, err error)

// Options 配置 Cache。
type Options struct {
	// Loader 必填，数据源函数。
	Loader Loader
	// Clock 可选，默认 time.Now。
	Clock Clock
	// OnError 可选，加载失败回调。
	OnError ErrorHandler
	// TTL 可选，缓存条目存活时间；<= 0 表示永不过期。
	TTL time.Duration
}

type entry struct {
	value     any
	expiresAt time.Time // 零值表示不过期
}

// load 表示一次在途加载。done 关闭后 value/err 可读。
type load struct {
	done      chan struct{}
	value     any
	err       error
	keyGen    uint64
	globalGen uint64
}

// Cache 是代际控制的并发缓存。零值不可用，请使用 New。
type Cache struct {
	loader  Loader
	clock   Clock
	onError ErrorHandler
	ttl     time.Duration

	mu        sync.Mutex
	entries   map[string]entry
	keyGen    map[string]uint64
	globalGen uint64
	inflight  map[string]*load
	closed    bool

	wg sync.WaitGroup // 跟踪在途加载，供 Close 等待资源释放
}

// New 创建 Cache。Options.Loader 必填。
func New(opts Options) *Cache {
	clock := opts.Clock
	if clock == nil {
		clock = time.Now
	}
	return &Cache{
		loader:   opts.Loader,
		clock:    clock,
		onError:  opts.OnError,
		ttl:      opts.TTL,
		entries:  make(map[string]entry),
		keyGen:   make(map[string]uint64),
		inflight: make(map[string]*load),
	}
}

// Get 返回键对应的值：命中缓存直接返回；未命中则加入在途加载或启动新加载。
// 调用者总是收到其所加入的那一次加载的结果，无论该结果是否被缓存。
func (c *Cache) Get(ctx context.Context, key string) (any, error) {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil, ErrClosed
	}
	if e, ok := c.entries[key]; ok {
		if e.expiresAt.IsZero() || c.clock().Before(e.expiresAt) {
			c.mu.Unlock()
			return e.value, nil
		}
		delete(c.entries, key) // 已过期
	}
	l, ok := c.inflight[key]
	if !ok {
		l = &load{
			done:      make(chan struct{}),
			keyGen:    c.keyGen[key],
			globalGen: c.globalGen,
		}
		c.inflight[key] = l
		c.wg.Add(1)
		go c.run(ctx, key, l)
	}
	c.mu.Unlock()

	select {
	case <-l.done:
		return l.value, l.err
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// run 执行加载并在完成后按代际快照决定是否写入缓存。
func (c *Cache) run(ctx context.Context, key string, l *load) {
	defer c.wg.Done()
	v, err := c.loader(ctx, key)

	c.mu.Lock()
	// 仅当代际快照仍然有效时才写入缓存：
	// Invalidate 会推进 keyGen，Clear 会推进 globalGen，
	// 任一变化都意味着本次加载的结果已过期，不得复活旧值。
	if err == nil && l.keyGen == c.keyGen[key] && l.globalGen == c.globalGen {
		e := entry{value: v}
		if c.ttl > 0 {
			e.expiresAt = c.clock().Add(c.ttl)
		}
		c.entries[key] = e
	}
	// 仅当在途记录仍指向本次加载时才移除（可能已被 Invalidate/Clear 脱钩）。
	if cur, ok := c.inflight[key]; ok && cur == l {
		delete(c.inflight, key)
	}
	l.value, l.err = v, err
	close(l.done)
	c.mu.Unlock()

	if err != nil && c.onError != nil {
		c.onError(key, err)
	}
}

// Invalidate 使指定键的缓存失效，并推进该键的代际。
// 在途旧加载继续运行但其结果不会写入缓存；
// 之后的 Get 会立即启动新加载，无需等待旧加载结束。
func (c *Cache) Invalidate(key string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.entries, key)
	c.keyGen[key]++
	delete(c.inflight, key) // 与在途旧加载脱钩，允许新加载立即启动
}

// Clear 清空全部缓存并推进全局代际。
// 所有在途加载的结果都不会写入缓存；之后的 Get 会启动新加载。
func (c *Cache) Clear() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries = make(map[string]entry)
	c.globalGen++
	c.inflight = make(map[string]*load) // 与全部在途加载脱钩
}

// Close 关闭缓存：之后的 Get 返回 ErrClosed；
// 并等待所有在途加载结束，确保数据源连接、goroutine 等资源全部释放。
func (c *Cache) Close() {
	c.mu.Lock()
	c.closed = true
	c.mu.Unlock()
	c.wg.Wait()
}
