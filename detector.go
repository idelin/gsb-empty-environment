// Package phiaccrual implements an adaptive heartbeat health detector
// modeled after the Phi Accrual Failure Detector (Cassandra / Akka style).
//
// Instead of a fixed timeout, the detector keeps a sliding window of
// historical heartbeat inter-arrival intervals, fits a normal distribution
// over them, and computes a continuously increasing suspicion level phi:
//
//	phi(t) = -log10(1 - F(t - lastHeartbeat))
//
// where F is the CDF of the observed inter-arrival distribution. phi grows
// smoothly as silence stretches beyond what the historical variance deems
// plausible, so short network jitter inside the normal variance does not
// raise a false alarm, while a genuine partition quickly pushes phi past
// the lease-reclamation threshold.
package phiaccrual

import (
	"errors"
	"math"
	"sync"
	"time"
)

// Config tunes a Detector.
type Config struct {
	// WindowSize is the number of recent inter-arrival intervals kept for
	// estimating the latency distribution.
	WindowSize int
	// Threshold is the phi level at which the node is considered failed and
	// its lease reclaimed. phi=8 means roughly 1 in 10^8 chance the node is
	// still alive given the observed distribution.
	Threshold float64
	// MinStdDeviation floors the estimated standard deviation so a very
	// regular heartbeat stream does not make the detector hypersensitive.
	MinStdDeviation time.Duration
	// AcceptableHeartbeatPause is added to the estimated mean so the
	// detector tolerates occasional environmental pauses (GC, scheduling).
	AcceptableHeartbeatPause time.Duration
	// FirstHeartbeatMean and FirstHeartbeatStdDev seed the distribution
	// before enough samples have been collected.
	FirstHeartbeatMean   time.Duration
	FirstHeartbeatStdDev time.Duration
}

// DefaultConfig returns a configuration suitable for a node heartbeating
// roughly once per second over a mildly jittery network.
func DefaultConfig() Config {
	return Config{
		WindowSize:               100,
		Threshold:                8.0,
		MinStdDeviation:          10 * time.Millisecond,
		AcceptableHeartbeatPause: 0,
		FirstHeartbeatMean:       500 * time.Millisecond,
		FirstHeartbeatStdDev:     200 * time.Millisecond,
	}
}

// ErrNoHeartbeat is returned by Phi before the first heartbeat arrives.
var ErrNoHeartbeat = errors.New("phiaccrual: no heartbeat received yet")

// Detector tracks heartbeats from a single remote node and computes its
// accrual suspicion level. It is safe for concurrent use.
type Detector struct {
	mu     sync.Mutex
	cfg    Config
	window []float64 // recent inter-arrival intervals, in milliseconds
	last   time.Time // wall clock of the most recent heartbeat
	seen   bool
}

// NewDetector creates a Detector with the given configuration, filling in
// sane defaults for zero-valued fields.
func NewDetector(cfg Config) *Detector {
	if cfg.WindowSize <= 0 {
		cfg.WindowSize = 100
	}
	if cfg.Threshold <= 0 {
		cfg.Threshold = 8.0
	}
	if cfg.MinStdDeviation <= 0 {
		cfg.MinStdDeviation = 10 * time.Millisecond
	}
	if cfg.FirstHeartbeatMean <= 0 {
		cfg.FirstHeartbeatMean = 500 * time.Millisecond
	}
	if cfg.FirstHeartbeatStdDev <= 0 {
		cfg.FirstHeartbeatStdDev = 200 * time.Millisecond
	}
	return &Detector{cfg: cfg}
}

// Report records a heartbeat arriving at time now.
func (d *Detector) Report(now time.Time) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.seen {
		interval := float64(now.Sub(d.last)) / float64(time.Millisecond)
		if interval < 0 {
			interval = 0
		}
		d.window = append(d.window, interval)
		if len(d.window) > d.cfg.WindowSize {
			d.window = d.window[len(d.window)-d.cfg.WindowSize:]
		}
	}
	d.last = now
	d.seen = true
}

// distribution returns the estimated mean and standard deviation (ms) of
// inter-arrival intervals.
func (d *Detector) distribution() (mean, stdDev float64) {
	n := len(d.window)
	if n == 0 {
		return float64(d.cfg.FirstHeartbeatMean) / float64(time.Millisecond),
			float64(d.cfg.FirstHeartbeatStdDev) / float64(time.Millisecond)
	}
	var sum float64
	for _, v := range d.window {
		sum += v
	}
	mean = sum / float64(n)
	var sq float64
	for _, v := range d.window {
		diff := v - mean
		sq += diff * diff
	}
	stdDev = math.Sqrt(sq / float64(n))
	minStd := float64(d.cfg.MinStdDeviation) / float64(time.Millisecond)
	if stdDev < minStd {
		stdDev = minStd
	}
	mean += float64(d.cfg.AcceptableHeartbeatPause) / float64(time.Millisecond)
	return mean, stdDev
}

// normalCDF evaluates the CDF of N(mean, stdDev^2) at x via math.Erf.
func normalCDF(x, mean, stdDev float64) float64 {
	y := (x - mean) / (stdDev * math.Sqrt2)
	return 0.5 * (1 + math.Erf(y))
}

// Phi returns the current suspicion level at time now. Higher means more
// likely crashed; compare against Config.Threshold.
func (d *Detector) Phi(now time.Time) (float64, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if !d.seen {
		return 0, ErrNoHeartbeat
	}
	mean, stdDev := d.distribution()
	elapsed := float64(now.Sub(d.last)) / float64(time.Millisecond)
	p := normalCDF(elapsed, mean, stdDev)
	if p >= 1 {
		p = 1 - 1e-15
	}
	return -math.Log10(1 - p), nil
}

// Available reports whether the node is still trusted at time now, i.e.
// phi has not crossed the failure threshold. Before the first heartbeat
// the node is optimistically considered available.
func (d *Detector) Available(now time.Time) bool {
	phi, err := d.Phi(now)
	if err != nil {
		return true
	}
	return phi < d.cfg.Threshold
}

// Threshold returns the configured failure threshold.
func (d *Detector) Threshold() float64 { return d.cfg.Threshold }

// Lease grants the monitored node the right to hold a resource while it is
// believed alive. A Monitor reclaims it once phi crosses the threshold.
type Lease struct {
	Holder string
}

// Monitor periodically evaluates a Detector and reclaims the lease exactly
// once when the suspicion level exceeds the threshold.
type Monitor struct {
	detector *Detector
	lease    Lease
	interval time.Duration

	mu        sync.Mutex
	reclaimed bool
	onReclaim func(Lease, float64)
}

// NewMonitor creates a Monitor checking the detector every interval.
// onReclaim, if non-nil, is invoked with the reclaimed lease and the phi
// value that tripped the threshold.
func NewMonitor(d *Detector, lease Lease, interval time.Duration, onReclaim func(Lease, float64)) *Monitor {
	if interval <= 0 {
		interval = 100 * time.Millisecond
	}
	return &Monitor{detector: d, lease: lease, interval: interval, onReclaim: onReclaim}
}

// Check performs a single evaluation at time now, reclaiming the lease if
// phi has crossed the threshold. It reports whether the lease was reclaimed
// by this call.
func (m *Monitor) Check(now time.Time) bool {
	m.mu.Lock()
	if m.reclaimed {
		m.mu.Unlock()
		return false
	}
	m.mu.Unlock()

	phi, err := m.detector.Phi(now)
	if err != nil {
		return false
	}
	if phi < m.detector.Threshold() {
		return false
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	if m.reclaimed {
		return false
	}
	m.reclaimed = true
	if m.onReclaim != nil {
		m.onReclaim(m.lease, phi)
	}
	return true
}

// Reclaimed reports whether the lease has been reclaimed.
func (m *Monitor) Reclaimed() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.reclaimed
}

// Run evaluates the detector every interval until the lease is reclaimed or
// stop is closed. It blocks; run it in a goroutine.
func (m *Monitor) Run(stop <-chan struct{}) {
	ticker := time.NewTicker(m.interval)
	defer ticker.Stop()
	for {
		select {
		case <-stop:
			return
		case now := <-ticker.C:
			if m.Check(now) {
				return
			}
		}
	}
}
