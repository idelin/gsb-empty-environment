// Package gencache 提供基于代际（generation）控制的并发安全缓存。
//
// 核心语义：
//   - 每次加载在开始时记录当前有效代际（全局代际 + 键代际）。
//   - Invalidate 提升单个键的代际，Clear 提升全局代际。
//   - 加载完成时若代际已失效，结果只返回给本次加载的等待者，绝不写入缓存，
//     因此旧加载无法“复活”旧值。
//   - 不同键的加载完全独立，互不阻塞；同键同代的并发 Get 共享一次加载。
//   - 失效发生后，新加载可以立即启动，无需等待旧加载结束。
//
// 数据源、时钟与失败回调均为可注入函数，不依赖任何远程缓存。
package gencache

import (
	"sync"
	"time"
)

// Source 按键加载值，可注入任意数据源（数据库、文件、内存映射等）。
type Source[K comparable, V any] func(key K) (V, error)

// Options 构造 Cache 的可注入依赖。
type Options[K comparable, V any] struct {
	// Source 必填，数据加载函数。
	Source Source[K, V]
	// Now 时钟，nil 时使用 time.Now。
	Now func() time.Time
	// TTL 缓存存活时间，<= 0 表示不过期。
	TTL time.Duration
	// OnError 加载失败回调（在锁外调用），可为 nil。
	OnError func(key K, err error)
}

// generation 标识一次写入/加载所属的代际。
type generation struct {
	global uint64
	key    uint64
}

// call 表示一次进行中的加载，等待者通过 done 通道获取结果。
type call[V any] struct {
	done chan struct{}
	val  V
	err  error
	gen  generation
}

type entry[V any] struct {
	keyGen    uint64
	val       V
	hasVal    bool
	expiresAt time.Time
	inflight  *call[V]
}

// Cache 是并发安全的代际缓存。
type Cache[K comparable, V any] struct {
	mu        sync.Mutex
	entries   map[K]*entry[V]
	globalGen uint64
	source    Source[K, V]
	now       func() time.Time
	ttl       time.Duration
	onError   func(key K, err error)
}

// New 创建缓存。
func New[K comparable, V any](opts Options[K, V]) *Cache[K, V] {
	now := opts.Now
	if now == nil {
		now = time.Now
	}
	return &Cache[K, V]{
		entries: make(map[K]*entry[V]),
		source:  opts.Source,
		now:     now,
		ttl:     opts.TTL,
		onError: opts.OnError,
	}
}

// Get 返回键对应的值。
//
// 等待者语义：若已有同代加载在进行，调用者等待该次加载并直接获得其结果
// （值或错误），即使该结果因中途失效而最终未写入缓存。
func (c *Cache[K, V]) Get(key K) (V, error) {
	c.mu.Lock()
	e := c.entries[key]
	if e == nil {
		e = &entry[V]{}
		c.entries[key] = e
	}
	gen := generation{global: c.globalGen, key: e.keyGen}
	if e.hasVal && !c.expiredLocked(e) {
		val := e.val
		c.mu.Unlock()
		return val, nil
	}
	if in := e.inflight; in != nil && in.gen == gen {
		done := in.done
		c.mu.Unlock()
		<-done
		return in.val, in.err
	}
	cl := &call[V]{done: make(chan struct{}), gen: gen}
	e.inflight = cl
	c.mu.Unlock()

	val, err := c.source(key)

	c.mu.Lock()
	if e.inflight == cl {
		e.inflight = nil
	}
	// 仅当代际仍然有效时才写入缓存，防止旧加载复活旧值。
	if err == nil && c.generationLocked(e) == gen {
		e.val = val
		e.hasVal = true
		if c.ttl > 0 {
			e.expiresAt = c.now().Add(c.ttl)
		} else {
			e.expiresAt = time.Time{}
		}
	}
	c.mu.Unlock()

	if err != nil && c.onError != nil {
		c.onError(key, err)
	}
	cl.val, cl.err = val, err
	close(cl.done)
	return val, err
}

// Invalidate 使单个键失效：提升键代际并丢弃已缓存值。
// 进行中的旧加载完成后不会写入缓存；新的 Get 会立即启动新加载。
func (c *Cache[K, V]) Invalidate(key K) {
	c.mu.Lock()
	if e := c.entries[key]; e != nil {
		e.keyGen++
		e.hasVal = false
		var zero V
		e.val = zero
		e.expiresAt = time.Time{}
	}
	c.mu.Unlock()
}

// Clear 使全部键失效：提升全局代际并清空缓存。
// 进行中的旧加载完成后不会写入缓存；新的 Get 会立即启动新加载。
func (c *Cache[K, V]) Clear() {
	c.mu.Lock()
	c.globalGen++
	c.entries = make(map[K]*entry[V])
	c.mu.Unlock()
}

func (c *Cache[K, V]) generationLocked(e *entry[V]) generation {
	return generation{global: c.globalGen, key: e.keyGen}
}

func (c *Cache[K, V]) expiredLocked(e *entry[V]) bool {
	return c.ttl > 0 && !e.expiresAt.IsZero() && !c.now().Before(e.expiresAt)
}

// inflightCount 返回进行中的加载数量，用于测试与资源释放验证。
func (c *Cache[K, V]) inflightCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	n := 0
	for _, e := range c.entries {
		if e.inflight != nil {
			n++
		}
	}
	return n
}
