package adaptive

import (
	"sync"
	"time"
)

// Tuner 后台周期调谐器：定时采样指标并驱动控制器。
// Stop 幂等且会等待后台 goroutine 退出，保证严格无 goroutine 泄漏。
type Tuner struct {
	ctrl     *Controller
	source   func() Metrics
	interval time.Duration

	stopCh  chan struct{}
	doneCh  chan struct{}
	startMu sync.Mutex
	started bool
	once    sync.Once
}

// NewTuner 创建调谐器。interval <= 0 时使用 100ms。
func NewTuner(ctrl *Controller, source func() Metrics, interval time.Duration) *Tuner {
	if interval <= 0 {
		interval = 100 * time.Millisecond
	}
	return &Tuner{
		ctrl:     ctrl,
		source:   source,
		interval: interval,
		stopCh:   make(chan struct{}),
		doneCh:   make(chan struct{}),
	}
}

// Start 启动后台调谐 goroutine；重复调用安全（仅首次生效）。
func (t *Tuner) Start() {
	t.startMu.Lock()
	defer t.startMu.Unlock()
	if t.started {
		return
	}
	t.started = true
	go t.loop()
}

func (t *Tuner) loop() {
	defer close(t.doneCh)
	ticker := time.NewTicker(t.interval)
	defer ticker.Stop()
	for {
		select {
		case <-t.stopCh:
			return
		case <-ticker.C:
			t.ctrl.Update(t.source())
		}
	}
}

// Stop 停止调谐器并等待后台 goroutine 退出；幂等，可重复调用。
func (t *Tuner) Stop() {
	t.once.Do(func() { close(t.stopCh) })
	t.startMu.Lock()
	started := t.started
	t.startMu.Unlock()
	if started {
		<-t.doneCh
	}
}
