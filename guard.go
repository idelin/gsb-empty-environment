package clockguard

import (
	"fmt"
	"sync"
	"time"
)

// TripEvent 描述一次时钟撕裂熔断事件。
type TripEvent struct {
	Reason    string
	WallDelta time.Duration // 两次采样间墙钟推进量
	MonoDelta time.Duration // 两次采样间单调时钟推进量
	Skew      time.Duration // 两者之差（撕裂量）
	At        time.Time
}

// Guardian 双时钟源一致性守护器。
// 周期性比对墙钟与单调时钟的推进步调；一旦发现撕裂量超过阈值
// （人为改时间、NTP 大幅阶跃、虚拟机时钟挂起等），立即熔断所有租约。
type Guardian struct {
	clock     Clock
	manager   *LeaseManager
	threshold time.Duration
	interval  time.Duration

	mu       sync.Mutex
	lastWall time.Time
	lastMono time.Duration
	primed   bool
	tripped  bool
	lastEv   TripEvent
	onTrip   func(TripEvent)

	running bool
	stopCh  chan struct{}
	doneCh  chan struct{}
}

// NewGuardian 创建守护器。threshold 为允许的最大时钟撕裂量，interval 为采样周期。
func NewGuardian(clock Clock, manager *LeaseManager, threshold, interval time.Duration) *Guardian {
	return &Guardian{clock: clock, manager: manager, threshold: threshold, interval: interval}
}

// OnTrip 注册熔断回调（在锁外调用）。
func (g *Guardian) OnTrip(fn func(TripEvent)) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.onTrip = fn
}

// CheckOnce 执行一次双时钟比对。检测到严重撕裂时立即熔断并返回 true。
func (g *Guardian) CheckOnce() bool {
	g.mu.Lock()
	wall := g.clock.Wall()
	mono := g.clock.Monotonic()
	if !g.primed {
		g.lastWall, g.lastMono, g.primed = wall, mono, true
		g.mu.Unlock()
		return false
	}
	if g.tripped {
		g.mu.Unlock()
		return true
	}
	wallDelta := wall.Sub(g.lastWall)
	monoDelta := mono - g.lastMono
	skew := wallDelta - monoDelta
	g.lastWall, g.lastMono = wall, mono
	if abs(skew) <= g.threshold {
		g.mu.Unlock()
		return false
	}
	g.tripped = true
	ev := TripEvent{
		Reason: fmt.Sprintf("时钟撕裂：墙钟推进 %v，单调时钟推进 %v，撕裂量 %v 超过阈值 %v",
			wallDelta, monoDelta, skew, g.threshold),
		WallDelta: wallDelta,
		MonoDelta: monoDelta,
		Skew:      skew,
		At:        wall,
	}
	g.lastEv = ev
	cb := g.onTrip
	g.mu.Unlock()

	// 紧急熔断：强制注销所有租约并降级为安全模式。
	g.manager.RevokeAll(ev.Reason)
	if cb != nil {
		cb(ev)
	}
	return true
}

// Tripped 报告守护器是否已熔断。
func (g *Guardian) Tripped() bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.tripped
}

// LastEvent 返回最近一次熔断事件。
func (g *Guardian) LastEvent() TripEvent {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.lastEv
}

// Start 启动后台周期监视。熔断后监视循环自动退出（守护器已降级，无需继续采样）。
func (g *Guardian) Start() {
	g.mu.Lock()
	if g.running {
		g.mu.Unlock()
		return
	}
	g.running = true
	g.stopCh = make(chan struct{})
	g.doneCh = make(chan struct{})
	stopCh, doneCh := g.stopCh, g.doneCh
	g.mu.Unlock()

	go func() {
		defer close(doneCh)
		t := time.NewTicker(g.interval)
		defer t.Stop()
		for {
			select {
			case <-stopCh:
				return
			case <-t.C:
				if g.CheckOnce() {
					return
				}
			}
		}
	}()
}

// Stop 停止后台监视并等待退出。
func (g *Guardian) Stop() {
	g.mu.Lock()
	if !g.running {
		g.mu.Unlock()
		return
	}
	g.running = false
	close(g.stopCh)
	doneCh := g.doneCh
	g.mu.Unlock()
	<-doneCh
}

func abs(d time.Duration) time.Duration {
	if d < 0 {
		return -d
	}
	return d
}
