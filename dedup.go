package netsec

import (
	"context"
	"crypto/sha256"
	"sync"
	"time"
)

// DefaultTTL 是已完成响应的默认缓存时长。
const DefaultTTL = 30 * time.Second

// Identity 唯一标识一个来自已验证来源的请求。
// 只有地址验证代次、请求摘要与序号三者完全一致时才视为同一请求；
// 任一字段不同都按新请求处理，缓存响应不得跨身份重放。
type Identity struct {
	Epoch  uint64
	Seq    uint64
	Digest [sha256.Size]byte
}

// NewIdentity 由地址验证代次、请求序号与请求正文构造请求身份。
// 来源地址变化后必须重新完成地址验证并推进代次，
// 使旧代次下缓存的响应无法向新来源重放。
func NewIdentity(epoch, seq uint64, body []byte) Identity {
	return Identity{Epoch: epoch, Seq: seq, Digest: sha256.Sum256(body)}
}

// Deduper 合并相同身份的在途请求，并缓存已完成的响应供同一身份重放。
// 所有方法并发安全；请使用 New 构造。
type Deduper struct {
	mu       sync.Mutex
	ttl      time.Duration
	inflight map[Identity]*call
	cache    map[Identity]*entry
}

type call struct {
	done chan struct{}
	refs int
	resp []byte
	err  error
}

type entry struct {
	resp    []byte
	expires time.Time
}

// New 返回一个 Deduper，ttl 为已完成响应的缓存保留时长；
// ttl<=0 时使用 DefaultTTL。
func New(ttl time.Duration) *Deduper {
	if ttl <= 0 {
		ttl = DefaultTTL
	}
	return &Deduper{
		ttl:      ttl,
		inflight: make(map[Identity]*call),
		cache:    make(map[Identity]*entry),
	}
}

// Do 执行 id 对应的请求并返回响应。
// 相同身份的请求正在执行时，本次调用合并等待同一结果，fn 不会重复执行；
// 响应已缓存时直接返回缓存内容。
// ctx 取消只让当前等待者退出，不影响其他等待者，也不中断后台执行；
// 只有成功的响应会被缓存，错误不缓存。
func (d *Deduper) Do(ctx context.Context, id Identity, fn func(context.Context) ([]byte, error)) ([]byte, error) {
	d.mu.Lock()
	if e, ok := d.cache[id]; ok {
		if time.Now().Before(e.expires) {
			resp := e.resp
			d.mu.Unlock()
			return resp, nil
		}
		delete(d.cache, id)
	}
	if c, ok := d.inflight[id]; ok {
		c.refs++
		d.mu.Unlock()
		return wait(ctx, c)
	}
	c := &call{done: make(chan struct{}), refs: 1}
	d.inflight[id] = c
	d.mu.Unlock()

	go d.run(ctx, id, c, fn)
	return wait(ctx, c)
}

func wait(ctx context.Context, c *call) ([]byte, error) {
	select {
	case <-c.done:
		return c.resp, c.err
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (d *Deduper) run(ctx context.Context, id Identity, c *call, fn func(context.Context) ([]byte, error)) {
	resp, err := fn(context.WithoutCancel(ctx))

	d.mu.Lock()
	defer d.mu.Unlock()
	c.resp, c.err = resp, err
	close(c.done)
	delete(d.inflight, id)
	if err == nil {
		d.cache[id] = &entry{resp: resp, expires: time.Now().Add(d.ttl)}
	}
}

// Replay 返回缓存响应中从 off 起最多 max 字节的片段，total 为响应全长。
// 仅当 id 与缓存身份（代次、摘要、序号）完全一致且响应未过期时 ok 为 true，
// 否则不得向请求方发送任何缓存内容。max<=0 表示返回到响应末尾；
// off 越界时返回空片段且 ok 仍为 true。
func (d *Deduper) Replay(id Identity, off, max int) (chunk []byte, total int, ok bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	e, hit := d.cache[id]
	if !hit {
		return nil, 0, false
	}
	if !time.Now().Before(e.expires) {
		delete(d.cache, id)
		return nil, 0, false
	}
	total = len(e.resp)
	if off < 0 {
		off = 0
	}
	if off >= total {
		return nil, total, true
	}
	end := total
	if max > 0 && off+max < end {
		end = off + max
	}
	return e.resp[off:end], total, true
}
