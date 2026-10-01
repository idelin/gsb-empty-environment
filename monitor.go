package phidetector

import (
	"context"
	"sync"
	"time"
)

// Monitor periodically evaluates a Detector and triggers lease
// reclamation exactly once when φ crosses the threshold.
type Monitor struct {
	detector *Detector
	interval time.Duration

	mu        sync.Mutex
	reclaimed bool
	reclaimAt time.Time
}

// NewMonitor creates a Monitor that checks the detector every interval.
func NewMonitor(d *Detector, interval time.Duration) *Monitor {
	if interval <= 0 {
		interval = 50 * time.Millisecond
	}
	return &Monitor{detector: d, interval: interval}
}

// Reclaimed reports whether the lease has been reclaimed, and when.
func (m *Monitor) Reclaimed() (bool, time.Time) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.reclaimed, m.reclaimAt
}

// Run evaluates φ until the lease is reclaimed or ctx is cancelled.
// onReclaim (may be nil) is invoked exactly once, at the moment the
// detector crosses the threshold.
func (m *Monitor) Run(ctx context.Context, onReclaim func(phi float64, at time.Time)) {
	ticker := time.NewTicker(m.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			phi, err := m.detector.Phi(now)
			if err != nil {
				continue
			}
			if phi >= m.detector.Threshold() {
				m.mu.Lock()
				m.reclaimed, m.reclaimAt = true, now
				m.mu.Unlock()
				if onReclaim != nil {
					onReclaim(phi, now)
				}
				return
			}
		}
	}
}
