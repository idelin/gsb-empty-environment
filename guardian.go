// Package clockguard 提供"双时钟源偏差检测与租约熔断守护器"。
//
// 守护器持续比对本地单调时钟（Monotonic）与墙钟（Wall-Clock）的推进步调。
// 一旦检测到人为修改系统时间、NTP 大幅阶跃或虚拟机时钟挂起造成的严重撕裂，
// 立即紧急熔断：强制注销所有活跃租约，并将自身降级为安全模式（拒绝发放新租约），
// 防止时钟混乱引发逻辑灾难。
package clockguard

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"
)

// ErrSafeMode 表示守护器已熔断并降级为安全模式，不再发放任何租约。
var ErrSafeMode = errors.New("clockguard: guardian tripped, safe mode engaged, leasing disabled")

// ErrLeaseNotFound 表示要释放的租约不存在（可能已被熔断强制注销）。
var ErrLeaseNotFound = errors.New("clockguard: lease not found")

// Lease 是一份由守护器发放的租约。
type Lease struct {
	ID           string
	Holder       string
	AcquiredAt   time.Time
	Revoked      bool
	RevokeReason string
}

// TripEvent 描述一次熔断事件。
type TripEvent struct {
	At            time.Time     // 检测到撕裂时的墙钟时间
	Skew          time.Duration // 两个时钟源的推进偏差（绝对值）
	WallDelta     time.Duration // 上次采样以来墙钟推进量
	MonoDelta     time.Duration // 上次采样以来单调时钟推进量
	Reason        string        // 故障分类说明
	RevokedLeases int           // 被强制注销的租约数量
}

// Guardian 是双时钟源偏差检测与租约熔断守护器。
type Guardian struct {
	clock     ClockSource
	threshold time.Duration // 允许的最大时钟撕裂
	interval  time.Duration // Run 循环的采样间隔

	onTrip   func(TripEvent)
	onRevoke func(Lease)

	mu          sync.Mutex
	leases      map[string]*Lease
	seq         int
	tripped     bool
	trip        *TripEvent
	initialized bool
	lastWall    time.Time
	lastMono    time.Duration
}

// Option 用于定制 Guardian。
type Option func(*Guardian)

// WithThreshold 设置允许的最大时钟撕裂，默认 5 秒。
func WithThreshold(d time.Duration) Option { return func(g *Guardian) { g.threshold = d } }

// WithInterval 设置 Run 循环的采样间隔，默认 1 秒。
func WithInterval(d time.Duration) Option { return func(g *Guardian) { g.interval = d } }

// OnTrip 注册熔断回调（在锁外调用，可安全做耗时操作）。
func OnTrip(fn func(TripEvent)) Option { return func(g *Guardian) { g.onTrip = fn } }

// OnRevoke 注册租约被强制注销时的回调（在锁外调用）。
func OnRevoke(fn func(Lease)) Option { return func(g *Guardian) { g.onRevoke = fn } }

func NewGuardian(clock ClockSource, opts ...Option) *Guardian {
	g := &Guardian{
		clock:     clock,
		threshold: 5 * time.Second,
		interval:  time.Second,
		leases:    make(map[string]*Lease),
	}
	for _, opt := range opts {
		opt(g)
	}
	return g
}

// Check 执行一次双时钟比对。检测到撕裂时立即熔断：
// 强制注销全部活跃租约并进入安全模式。返回当前是否处于熔断状态。
//
// 首次调用仅建立基线，不做判定。
func (g *Guardian) Check() bool {
	wall := g.clock.Wall()
	mono := g.clock.Monotonic()

	g.mu.Lock()
	if !g.initialized {
		g.initialized = true
		g.lastWall, g.lastMono = wall, mono
		g.mu.Unlock()
		return false
	}
	wallDelta := wall.Sub(g.lastWall)
	monoDelta := mono - g.lastMono
	g.lastWall, g.lastMono = wall, mono

	if g.tripped {
		g.mu.Unlock()
		return true
	}

	skew := wallDelta - monoDelta
	if skew < 0 {
		skew = -skew
	}
	if skew <= g.threshold {
		g.mu.Unlock()
		return false
	}

	event := TripEvent{
		At:        wall,
		Skew:      skew,
		WallDelta: wallDelta,
		MonoDelta: monoDelta,
		Reason:    classify(wallDelta, monoDelta),
	}
	g.tripped = true
	g.trip = &event

	revoked := make([]Lease, 0, len(g.leases))
	for _, l := range g.leases {
		l.Revoked = true
		l.RevokeReason = event.Reason
		revoked = append(revoked, *l)
	}
	event.RevokedLeases = len(revoked)
	g.leases = make(map[string]*Lease)

	onTrip, onRevoke := g.onTrip, g.onRevoke
	g.mu.Unlock()

	for _, l := range revoked {
		if onRevoke != nil {
			onRevoke(l)
		}
	}
	if onTrip != nil {
		onTrip(event)
	}
	return true
}

// classify 根据两个时钟域的推进方向对故障进行分类。
func classify(wallDelta, monoDelta time.Duration) string {
	switch {
	case wallDelta < 0:
		return "wall clock moved backwards (manual time change or NTP step)"
	case monoDelta <= 0:
		return "monotonic clock stalled (possible VM/clock suspend)"
	default:
		return "wall clock jumped forward (NTP step or clock adjustment)"
	}
}

// Run 启动持续监视循环，按采样间隔执行 Check，直到 ctx 取消。
// 熔断后守护器保持安全模式，循环仍继续采样以便观察时钟状态。
func (g *Guardian) Run(ctx context.Context) {
	g.Check() // 建立基线
	ticker := time.NewTicker(g.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			g.Check()
		}
	}
}

// Acquire 申请一份租约。守护器处于安全模式时返回 ErrSafeMode。
func (g *Guardian) Acquire(holder string) (*Lease, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.tripped {
		return nil, ErrSafeMode
	}
	g.seq++
	l := &Lease{
		ID:         fmt.Sprintf("lease-%d", g.seq),
		Holder:     holder,
		AcquiredAt: g.clock.Wall(),
	}
	g.leases[l.ID] = l
	return l, nil
}

// Release 主动释放一份租约。
func (g *Guardian) Release(id string) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if _, ok := g.leases[id]; !ok {
		return ErrLeaseNotFound
	}
	delete(g.leases, id)
	return nil
}

// Tripped 报告守护器是否已熔断（安全模式）。
func (g *Guardian) Tripped() bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.tripped
}

// TripEvent 返回熔断事件详情；未熔断时返回 nil。
func (g *Guardian) TripEvent() *TripEvent {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.trip
}

// LeaseCount 返回当前活跃租约数量。
func (g *Guardian) LeaseCount() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return len(g.leases)
}
