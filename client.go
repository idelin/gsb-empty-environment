package distlock

import (
	"context"
	"sync"
	"time"
)

// Client 是一个锁客户端节点，通过 LockService 协调。
type Client struct {
	svc    *LockService
	nodeID string
}

// NewClient 创建一个节点 ID 为 nodeID 的客户端。
func NewClient(svc *LockService, nodeID string) *Client {
	return &Client{svc: svc, nodeID: nodeID}
}

// Lock 表示客户端持有的一把锁，附带 FenceToken 与后台心跳协程。
type Lock struct {
	svc   *LockService
	name  string
	owner string
	token uint64
	ttl   time.Duration

	mu     sync.Mutex
	frozen bool // 模拟节点假死：为 true 时心跳协程跳过续期
	lost   bool // 心跳续期失败（锁已被回收）后置位
	stopCh chan struct{}
	doneCh chan struct{}
}

// Acquire 为节点获取锁，绑定租约 TTL，并立即启动后台心跳协程自动续期。
func (c *Client) Acquire(ctx context.Context, name string, ttl time.Duration) (*Lock, error) {
	token, err := c.svc.Acquire(ctx, name, c.nodeID, ttl)
	if err != nil {
		return nil, err
	}
	l := &Lock{
		svc:    c.svc,
		name:   name,
		owner:  c.nodeID,
		token:  token,
		ttl:    ttl,
		stopCh: make(chan struct{}),
		doneCh: make(chan struct{}),
	}
	go l.heartbeatLoop(ttl / 3)
	return l, nil
}

// Token 返回该锁的 FenceToken。
func (l *Lock) Token() uint64 { return l.token }

// Lost 报告心跳是否已发现锁丢失（租约被回收）。
func (l *Lock) Lost() bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.lost
}

// Freeze 模拟节点假死/挂起：心跳协程停止发送续期（协程仍在，但不工作）。
func (l *Lock) Freeze() {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.frozen = true
}

// Unfreeze 模拟节点从假死中恢复。
func (l *Lock) Unfreeze() {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.frozen = false
}

// Write 携带 FenceToken 写受保护资源；token 过期会被服务端拒绝。
func (l *Lock) Write(value string) error {
	return l.svc.Write(l.name, l.token, value)
}

// Unlock 停止心跳并显式释放锁。锁已丢失时静默成功。
func (l *Lock) Unlock() {
	close(l.stopCh)
	<-l.doneCh
	_ = l.svc.Release(l.name, l.owner, l.token)
}

func (l *Lock) heartbeatLoop(interval time.Duration) {
	defer close(l.doneCh)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-l.stopCh:
			return
		case <-ticker.C:
			l.mu.Lock()
			frozen := l.frozen
			l.mu.Unlock()
			if frozen {
				continue // 假死：不发送心跳
			}
			if err := l.svc.KeepAlive(l.name, l.owner, l.token, l.ttl); err != nil {
				l.mu.Lock()
				l.lost = true
				l.mu.Unlock()
				return
			}
		}
	}
}
