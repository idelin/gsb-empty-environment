// Package connpool 实现一个支持配置分代的连接池。
//
// 核心语义：
//   - 空闲连接复用：归还的健康连接进入当前代次的空闲栈，借出时优先复用。
//   - 配置换代：UpdateConfig 递增代次，旧代次空闲连接立即排空关闭；
//     借出中的旧代次连接允许完成当前请求，归还时关闭且不得进入新池。
//   - 借出中的连接不会被重复关闭（sync.Once 保证）。
//   - 健康失效：借出时校验、归还时标记不健康，都会触发关闭而非复用。
package connpool

import (
	"context"
	"errors"
	"sync"
)

// Conn 是池化连接的最小接口。
type Conn interface {
	Close() error
}

// DialFunc 创建一个新连接。
type DialFunc func(ctx context.Context) (Conn, error)

// ValidateFunc 在借出复用前校验空闲连接是否仍然健康。
// 返回 false 时连接被关闭并重新获取。可为 nil（总是视为健康）。
type ValidateFunc func(c Conn) bool

var (
	ErrPoolClosed  = errors.New("connpool: pool is closed")
	ErrWaitTimeout = errors.New("connpool: wait for connection timed out")
)

// Options 配置连接池。
type Options struct {
	MaxOpen  int          // 同时打开的最大连接数（含借出与空闲），<=0 表示不限
	MaxIdle  int          // 空闲池最大容量，超出的归还连接直接关闭
	Validate ValidateFunc // 借出复用前的健康检查
}

type pooledConn struct {
	conn Conn
	gen  uint64

	closeOnce sync.Once
	closeErr  error
}

// close 保证同一物理连接只被关闭一次。
func (pc *pooledConn) close() error {
	pc.closeOnce.Do(func() {
		pc.closeErr = pc.conn.Close()
	})
	return pc.closeErr
}

// Handle 是借出的连接句柄。内嵌的 Conn 为真实连接，
// Close 将连接归还池中（而非物理关闭），Invalidate 标记失效并关闭。
// 两者都是一次性的，重复调用安全。
type Handle struct {
	Conn

	pool *Pool
	pc   *pooledConn
	once sync.Once
}

// Close 归还连接到池中。若连接属于旧代次、池已关闭或空闲池已满，
// 连接将被物理关闭而不是复用。
func (h *Handle) Close() error {
	h.once.Do(func() {
		_ = h.pool.putBack(h.pc, true)
	})
	return nil
}

// Invalidate 标记连接不健康并归还，连接将被物理关闭而不是复用。
func (h *Handle) Invalidate() error {
	h.once.Do(func() {
		_ = h.pool.putBack(h.pc, false)
	})
	return nil
}

type Pool struct {
	dial     DialFunc
	maxOpen  int
	maxIdle  int
	validate ValidateFunc

	mu      sync.Mutex
	gen     uint64
	open    int           // 当前物理打开的连接总数（所有代次）
	idle    []*pooledConn // 仅存放当前代次的空闲连接
	waiters []chan struct{}
	closed  bool
}

func New(dial DialFunc, opts Options) *Pool {
	maxOpen := opts.MaxOpen
	if maxOpen <= 0 {
		maxOpen = 1<<31 - 1
	}
	return &Pool{
		dial:     dial,
		maxOpen:  maxOpen,
		maxIdle:  opts.MaxIdle,
		validate: opts.Validate,
	}
}

// Borrow 借出一个连接：优先复用当前代次空闲连接，否则新建；
// 达到 MaxOpen 时阻塞等待，直到有可复用连接、可新建或 ctx 超时。
func (p *Pool) Borrow(ctx context.Context) (*Handle, error) {
	for {
		p.mu.Lock()
		if p.closed {
			p.mu.Unlock()
			return nil, ErrPoolClosed
		}
		// 复用当前代次的空闲连接。
		if n := len(p.idle); n > 0 {
			pc := p.idle[n-1]
			p.idle = p.idle[:n-1]
			if p.validate == nil || p.validate(pc.conn) {
				p.mu.Unlock()
				return &Handle{Conn: pc.conn, pool: p, pc: pc}, nil
			}
			// 健康失效：关闭后继续尝试。
			p.open--
			p.notifyLocked()
			p.mu.Unlock()
			_ = pc.close()
			continue
		}
		// 新建连接。代次在拨号前捕获：拨号期间换代则视为旧代次。
		if p.open < p.maxOpen {
			gen := p.gen
			p.open++
			p.mu.Unlock()
			c, err := p.dial(ctx)
			if err != nil {
				p.mu.Lock()
				p.open--
				p.notifyLocked()
				p.mu.Unlock()
				return nil, err
			}
			pc := &pooledConn{conn: c, gen: gen}
			return &Handle{Conn: c, pool: p, pc: pc}, nil
		}
		// 等待空位。
		waiter := make(chan struct{}, 1)
		p.waiters = append(p.waiters, waiter)
		p.mu.Unlock()
		select {
		case <-waiter:
		case <-ctx.Done():
			p.mu.Lock()
			p.removeWaiterLocked(waiter)
			p.mu.Unlock()
			return nil, ErrWaitTimeout
		}
	}
}

// putBack 归还连接。healthy=false 表示连接已失效，直接关闭。
// 旧代次连接归还时不进入新池，直接关闭（排空语义）。
func (p *Pool) putBack(pc *pooledConn, healthy bool) error {
	p.mu.Lock()
	if p.closed || !healthy || pc.gen != p.gen || len(p.idle) >= p.maxIdle {
		p.open--
		p.notifyLocked()
		p.mu.Unlock()
		return pc.close()
	}
	p.idle = append(p.idle, pc)
	p.notifyLocked()
	p.mu.Unlock()
	return nil
}

// UpdateConfig 配置换代：递增代次，立即排空并关闭旧代次空闲连接；
// 借出中的旧代次连接继续完成请求，归还时被关闭且不进入新池。
func (p *Pool) UpdateConfig() {
	p.mu.Lock()
	p.gen++
	stale := p.idle
	p.idle = nil
	p.open -= len(stale)
	p.notifyAllLocked()
	p.mu.Unlock()
	for _, pc := range stale {
		_ = pc.close()
	}
}

// Close 关闭池：排空空闲连接，拒绝新的借出；
// 借出中的连接归还时被关闭（不会重复关闭）。
func (p *Pool) Close() error {
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return nil
	}
	p.closed = true
	stale := p.idle
	p.idle = nil
	p.open -= len(stale)
	p.notifyAllLocked()
	p.mu.Unlock()
	var firstErr error
	for _, pc := range stale {
		if err := pc.close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

// Stats 返回当前物理打开连接数与空闲连接数（用于测试与监控）。
func (p *Pool) Stats() (open, idle int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.open, len(p.idle)
}

func (p *Pool) notifyLocked() {
	if len(p.waiters) > 0 {
		w := p.waiters[0]
		p.waiters = p.waiters[1:]
		w <- struct{}{}
	}
}

func (p *Pool) notifyAllLocked() {
	for _, w := range p.waiters {
		w <- struct{}{}
	}
	p.waiters = nil
}

func (p *Pool) removeWaiterLocked(target chan struct{}) {
	for i, w := range p.waiters {
		if w == target {
			p.waiters = append(p.waiters[:i], p.waiters[i+1:]...)
			return
		}
	}
}
