package clockguard

import (
	"errors"
	"sync"
	"time"
)

// ErrSafeMode 表示守护器已降级为安全模式，拒绝发放任何新租约。
var ErrSafeMode = errors.New("clockguard: 守护器已进入安全模式，拒绝新租约")

// Lease 一个活跃租约。
type Lease struct {
	ID        string
	ExpiresAt time.Time
}

// LeaseManager 管理活跃租约，并支持紧急熔断与安全模式降级。
type LeaseManager struct {
	mu       sync.Mutex
	clock    Clock
	leases   map[string]Lease
	safeMode bool
	reason   string
}

func NewLeaseManager(clock Clock) *LeaseManager {
	return &LeaseManager{clock: clock, leases: make(map[string]Lease)}
}

// Acquire 申请租约；安全模式下返回 ErrSafeMode。
func (m *LeaseManager) Acquire(id string, ttl time.Duration) (Lease, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.safeMode {
		return Lease{}, ErrSafeMode
	}
	l := Lease{ID: id, ExpiresAt: m.clock.Wall().Add(ttl)}
	m.leases[id] = l
	return l, nil
}

// Active 返回当前活跃租约快照。
func (m *LeaseManager) Active() []Lease {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]Lease, 0, len(m.leases))
	for _, l := range m.leases {
		out = append(out, l)
	}
	return out
}

// RevokeAll 紧急熔断：立即强制注销全部活跃租约，并将自身降级为安全模式。
// 返回被注销的租约数量。幂等，可重复调用。
func (m *LeaseManager) RevokeAll(reason string) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	n := len(m.leases)
	m.leases = make(map[string]Lease)
	m.safeMode = true
	m.reason = reason
	return n
}

// SafeMode 报告是否已降级为安全模式。
func (m *LeaseManager) SafeMode() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.safeMode
}

// TripReason 返回触发熔断的原因。
func (m *LeaseManager) TripReason() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.reason
}
