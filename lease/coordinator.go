package lease

import (
	"sync"
	"time"
)

// Coordinator 租约协调器：周期性巡检配额池，强制回收（Revoke）超期未归还的租约。
type Coordinator struct {
	pool     *Pool
	interval time.Duration

	mu      sync.Mutex
	stop    chan struct{}
	stopped bool
	wg      sync.WaitGroup
}

// NewCoordinator 创建协调器，interval 为巡检周期。
func NewCoordinator(pool *Pool, interval time.Duration) *Coordinator {
	return &Coordinator{pool: pool, interval: interval, stop: make(chan struct{})}
}

// Start 启动后台巡检 goroutine。
func (c *Coordinator) Start() {
	c.wg.Add(1)
	go func() {
		defer c.wg.Done()
		ticker := time.NewTicker(c.interval)
		defer ticker.Stop()
		for {
			select {
			case <-c.stop:
				return
			case <-ticker.C:
				c.pool.RevokeExpired()
			}
		}
	}()
}

// SweepOnce 立即执行一次强制回收（同步语义，便于测试与手动触发）。
func (c *Coordinator) SweepOnce() []*Lease {
	return c.pool.RevokeExpired()
}

// Stop 停止协调器并等待后台 goroutine 退出。
func (c *Coordinator) Stop() {
	c.mu.Lock()
	if !c.stopped {
		c.stopped = true
		close(c.stop)
	}
	c.mu.Unlock()
	c.wg.Wait()
}
