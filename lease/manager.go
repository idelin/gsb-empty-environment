// Package lease 实现单机模拟的分布式租约协调内核：
// 租约由主节点持有并具有明确 TTL；协调端与持有端均按
// 最大时钟漂移（maxDrift）预留安全余量，主节点在安全期
// （TTL - maxDrift）结束后主动放弃特权；检测到墙上时钟
// 异常前拨时触发紧急提前降级，杜绝双主脑裂。
package lease

import (
	"errors"
	"fmt"
	"sync"
	"time"
)

const (
	DefaultTTL      = 10 * time.Second
	DefaultMaxDrift = 500 * time.Millisecond
)

var (
	ErrNotPrimary = errors.New("lease: not primary, write rejected")
	ErrLeaseHeld  = errors.New("lease: still held (within drift safety window)")
)

// Coordinator 租约协调端（单机模拟的协调内核）。
// 只有在旧租约完全过期（TTL + maxDrift 安全窗口）之后，
// 才允许把租约授予新节点，保证任意时钟偏移下都不会双主。
type Coordinator struct {
	clock    Clock
	ttl      time.Duration
	maxDrift time.Duration

	mu           sync.Mutex
	holder       string
	fencing      uint64
	acquiredMono time.Duration
}

func NewCoordinator(clock Clock, ttl, maxDrift time.Duration) *Coordinator {
	return &Coordinator{clock: clock, ttl: ttl, maxDrift: maxDrift}
}

// Acquire 尝试为 nodeID 授予租约，成功时返回单调递增的 fencing token。
func (c *Coordinator) Acquire(nodeID string) (uint64, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.holder != "" {
		// 旧租约须完整经过 TTL + maxDrift 才视为彻底失效，
		// 覆盖旧主可能仍认为自己合法的整个区间。
		if c.clock.Mono() < c.acquiredMono+c.ttl+c.maxDrift {
			return 0, fmt.Errorf("%w: holder=%s", ErrLeaseHeld, c.holder)
		}
	}
	c.fencing++
	c.holder = nodeID
	c.acquiredMono = c.clock.Mono()
	return c.fencing, nil
}

// Fencing 返回当前最新 fencing token（只读）。
func (c *Coordinator) Fencing() uint64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.fencing
}

// Manager 节点侧租约管理器。
type Manager struct {
	nodeID   string
	coord    *Coordinator
	clock    Clock
	ttl      time.Duration
	maxDrift time.Duration

	mu           sync.Mutex
	primary      bool
	fencing      uint64
	acquiredMono time.Duration
	acquiredWall time.Time
	demoteReason string

	stopCh chan struct{}
	doneCh chan struct{}
}

func NewManager(nodeID string, coord *Coordinator, clock Clock, ttl, maxDrift time.Duration) *Manager {
	return &Manager{
		nodeID:   nodeID,
		coord:    coord,
		clock:    clock,
		ttl:      ttl,
		maxDrift: maxDrift,
		stopCh:   make(chan struct{}),
		doneCh:   make(chan struct{}),
	}
}

// SafePeriod 主节点可安全以主身份下发写操作的时长：TTL - maxDrift。
func (m *Manager) SafePeriod() time.Duration { return m.ttl - m.maxDrift }

// Campaign 竞选主节点。
func (m *Manager) Campaign() error {
	fencing, err := m.coord.Acquire(m.nodeID)
	if err != nil {
		return err
	}
	m.mu.Lock()
	m.primary = true
	m.fencing = fencing
	m.acquiredMono = m.clock.Mono()
	m.acquiredWall = m.clock.Wall()
	m.demoteReason = ""
	m.mu.Unlock()
	return nil
}

// StartWatchdog 启动后台安全自检协程，周期性执行 SafetyCheck。
func (m *Manager) StartWatchdog(interval time.Duration) {
	go func() {
		defer close(m.doneCh)
		t := time.NewTicker(interval)
		defer t.Stop()
		for {
			select {
			case <-m.stopCh:
				return
			case <-t.C:
				m.SafetyCheck()
			}
		}
	}()
}

// Close 停止后台自检。
func (m *Manager) Close() {
	select {
	case <-m.stopCh:
	default:
		close(m.stopCh)
	}
	<-m.doneCh
}

// SafetyCheck 安全自检（幂等，可由看门狗或写路径调用）：
//  1. 时钟突变检测：墙上时间 elapsed 与单调时间 elapsed 之差超过
//     maxDrift，说明发生了时钟前拨等异常，立即紧急提前降级；
//  2. 安全期检测：单调时间已过 TTL - maxDrift，主动放弃特权。
func (m *Manager) SafetyCheck() {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.primary {
		return
	}
	monoElapsed := m.clock.Mono() - m.acquiredMono
	wallElapsed := m.clock.Wall().Sub(m.acquiredWall)
	if skew := wallElapsed - monoElapsed; skew > m.maxDrift {
		m.demoteLocked(fmt.Sprintf("emergency: wall clock jumped forward by %s (>%s max drift)", skew, m.maxDrift))
		return
	}
	if monoElapsed >= m.ttl-m.maxDrift {
		m.demoteLocked(fmt.Sprintf("safe period %s elapsed (ttl=%s, maxDrift=%s)", m.ttl-m.maxDrift, m.ttl, m.maxDrift))
	}
}

func (m *Manager) demoteLocked(reason string) {
	m.primary = false
	m.demoteReason = reason
}

// IsPrimary 报告当前是否仍持有主特权（调用前先做安全自检）。
func (m *Manager) IsPrimary() bool {
	m.SafetyCheck()
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.primary
}

// DemoteReason 返回降级原因（未降级为空串）。
func (m *Manager) DemoteReason() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.demoteReason
}

// Fencing 返回本节点持有租约的 fencing token。
func (m *Manager) Fencing() uint64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.fencing
}

// Write 以主身份下发写操作。每次写前强制执行安全自检，
// 一旦越过安全期或检测到时钟突变，拒绝写并返回 ErrNotPrimary。
func (m *Manager) Write(op string) (uint64, error) {
	m.SafetyCheck()
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.primary {
		return 0, fmt.Errorf("%w (node=%s, reason=%s)", ErrNotPrimary, m.nodeID, m.demoteReason)
	}
	// 模拟把 (fencing, op) 提交到底层存储。
	return m.fencing, nil
}
