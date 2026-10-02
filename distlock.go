// Package distlock 实现带租约（Lease）隔离与 Fencing Token 的分布式互斥锁。
//
// 核心机制：
//   - 获取锁时绑定租约 TTL，到期未续期则自动释放，防止持有者崩溃导致死锁。
//   - 客户端后台协程按 TTL/3 周期发送 KeepAlive 心跳续期。
//   - 每次授予锁都会分配单调递增的 FenceToken，受保护资源拒绝携带
//     过期 token 的写操作（防止“假死恢复”的节点脑裂写入）。
package distlock

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"
)

var (
	// ErrLockLost 表示锁已被租约超时回收或持有者身份不匹配。
	ErrLockLost = errors.New("distlock: lock lost")
	// ErrLeaseExpired 表示租约已过期，续期被拒绝。
	ErrLeaseExpired = errors.New("distlock: lease expired")
	// ErrFencing 表示 FenceToken 已过期，写操作被拒绝。
	ErrFencing = errors.New("distlock: fencing token stale, write rejected")
)

type lockState struct {
	owner  string
	token  uint64
	expiry time.Time
	notify chan struct{} // 状态变化时关闭以唤醒等待者
}

// LockService 是锁的协调方（可理解为 etcd/ZooKeeper 的锁模块）。
// 本实现为单进程内模拟，便于测试；语义与分布式锁服务一致。
type LockService struct {
	mu       sync.Mutex
	locks    map[string]*lockState
	fence    uint64            // 全局单调递增的 fencing token 计数器
	maxFence map[string]uint64 // 每个资源已见过的最大 token
	data     map[string]string // 模拟受锁保护的资源数据
}

// NewLockService 创建一个锁服务实例。
func NewLockService() *LockService {
	return &LockService{
		locks:    make(map[string]*lockState),
		maxFence: make(map[string]uint64),
		data:     make(map[string]string),
	}
}

// Acquire 阻塞直到获得名为 name 的锁（或 ctx 取消）。
// 成功时返回新分配的 FenceToken，锁绑定租约 TTL，到期未续期自动释放。
func (s *LockService) Acquire(ctx context.Context, name, owner string, ttl time.Duration) (uint64, error) {
	for {
		s.mu.Lock()
		st, ok := s.locks[name]
		now := time.Now()
		if !ok || !now.Before(st.expiry) {
			// 锁空闲，或前任持有者租约已过期 -> 回收并授予。
			if ok {
				close(st.notify)
			}
			s.fence++
			token := s.fence
			s.locks[name] = &lockState{
				owner:  owner,
				token:  token,
				expiry: now.Add(ttl),
				notify: make(chan struct{}),
			}
			if token > s.maxFence[name] {
				s.maxFence[name] = token
			}
			s.mu.Unlock()
			return token, nil
		}
		wait := time.Until(st.expiry)
		notify := st.notify
		s.mu.Unlock()

		timer := time.NewTimer(wait + 5*time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return 0, ctx.Err()
		case <-notify: // 锁被显式释放或易主，立即重试
			timer.Stop()
		case <-timer.C: // 睡到租约到期点再检查
		}
	}
}

// KeepAlive 为持有者续期一个 TTL。持有者身份不符或租约已过期时返回错误。
func (s *LockService) KeepAlive(name, owner string, token uint64, ttl time.Duration) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	st, ok := s.locks[name]
	if !ok || st.owner != owner || st.token != token {
		return ErrLockLost
	}
	if !time.Now().Before(st.expiry) {
		return ErrLeaseExpired
	}
	st.expiry = time.Now().Add(ttl)
	return nil
}

// Release 显式释放锁并唤醒等待者。只有当前持有者可以释放。
func (s *LockService) Release(name, owner string, token uint64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	st, ok := s.locks[name]
	if !ok || st.owner != owner || st.token != token {
		return ErrLockLost
	}
	close(st.notify)
	delete(s.locks, name)
	return nil
}

// Write 模拟对受保护资源的写入，强制校验 Fencing Token：
// 任何携带小于该资源已见最大 token 的写都会被拒绝。
func (s *LockService) Write(name string, token uint64, value string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if token < s.maxFence[name] {
		return fmt.Errorf("%w (token=%d, fence=%d)", ErrFencing, token, s.maxFence[name])
	}
	s.data[name] = value
	return nil
}

// Read 读取受保护资源的当前值（演示用）。
func (s *LockService) Read(name string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.data[name]
}
