// Package phidetector implements an adaptive heartbeat health detector
// inspired by the φ-Accrual Failure Detector (Cassandra / Akka).
//
// Instead of a fixed timeout, the detector keeps a sliding window of
// historical heartbeat inter-arrival intervals, estimates their mean and
// variance, and models the arrival time of the next heartbeat with a
// normal distribution. The suspicion level φ at time t is:
//
//	φ(t) = -log10(P_later(t - t_last))
//
// where P_later is the survival function (1 - CDF) of the estimated
// normal distribution. φ grows smoothly with elapsed time: occasional
// network jitter that falls within the observed variance keeps φ low,
// while a genuine partition pushes φ past the threshold decisively.
package phidetector

import (
	"errors"
	"math"
	"sync"
	"time"
)

// Default tuning parameters.
const (
	DefaultWindowSize      = 200
	DefaultMinSamples      = 10
	DefaultMinStdDev       = 10 * time.Millisecond
	DefaultPhiThreshold    = 8.0
	DefaultAcceptablePause = 0 * time.Millisecond
)

// ErrNoHeartbeat is returned by Phi when no heartbeat has been reported yet.
var ErrNoHeartbeat = errors.New("phidetector: no heartbeat reported yet")

// intervalWindow is a fixed-capacity sliding window of heartbeat
// inter-arrival intervals with running mean/variance.
type intervalWindow struct {
	buf   []float64 // seconds
	head  int
	count int
	mean  float64
	m2    float64 // sum of squared deviations
}

func newIntervalWindow(capacity int) *intervalWindow {
	return &intervalWindow{buf: make([]float64, capacity)}
}

func (w *intervalWindow) add(v float64) {
	if w.count < len(w.buf) {
		w.buf[(w.head+w.count)%len(w.buf)] = v
		w.count++
	} else {
		w.buf[w.head] = v
		w.head = (w.head + 1) % len(w.buf)
	}
	w.recompute()
}

// recompute refreshes mean and variance from scratch. With a bounded
// window (default 200) this is cheap and avoids numerical drift.
func (w *intervalWindow) recompute() {
	if w.count == 0 {
		w.mean, w.m2 = 0, 0
		return
	}
	var sum float64
	for i := 0; i < w.count; i++ {
		sum += w.buf[i]
	}
	w.mean = sum / float64(w.count)
	var m2 float64
	for i := 0; i < w.count; i++ {
		d := w.buf[i] - w.mean
		m2 += d * d
	}
	w.m2 = m2
}

func (w *intervalWindow) variance() float64 {
	if w.count < 2 {
		return 0
	}
	return w.m2 / float64(w.count-1)
}

// Stats is a snapshot of the detector's current estimate.
type Stats struct {
	Samples   int
	Mean      time.Duration
	StdDev    time.Duration
	LastSeen  time.Time
	Phi       float64
	Suspected bool
}

// Detector tracks heartbeats from a single remote node.
// It is safe for concurrent use.
type Detector struct {
	mu          sync.Mutex
	window      *intervalWindow
	minSamples  int
	minStdDev   float64 // seconds
	threshold   float64
	acceptPause float64 // seconds of grace added before suspicion grows
	lastSeen    time.Time
	hasSeen     bool
}

// Option customizes a Detector.
type Option func(*Detector)

// WithWindowSize sets the sliding-window capacity (number of intervals kept).
func WithWindowSize(n int) Option {
	return func(d *Detector) {
		if n < 2 {
			n = 2
		}
		d.window = newIntervalWindow(n)
	}
}

// WithMinSamples sets how many intervals are required before φ is
// considered meaningful (below that, Phi returns 0).
func WithMinSamples(n int) Option {
	return func(d *Detector) {
		if n < 1 {
			n = 1
		}
		d.minSamples = n
	}
}

// WithMinStdDev floors the estimated standard deviation so that an
// unnaturally stable link does not make the detector hair-trigger.
func WithMinStdDev(sd time.Duration) Option {
	return func(d *Detector) { d.minStdDev = sd.Seconds() }
}

// WithPhiThreshold sets the φ value at which the node is declared dead
// and its lease reclaimed. φ=8 corresponds to roughly a 1-in-10^8
// chance that a live node would be this late.
func WithPhiThreshold(phi float64) Option {
	return func(d *Detector) { d.threshold = phi }
}

// WithAcceptablePause adds a flat grace period (e.g. for known GC
// pauses) before suspicion starts to accrue.
func WithAcceptablePause(p time.Duration) Option {
	return func(d *Detector) { d.acceptPause = p.Seconds() }
}

// New creates a Detector with the given options.
func New(opts ...Option) *Detector {
	d := &Detector{
		window:      newIntervalWindow(DefaultWindowSize),
		minSamples:  DefaultMinSamples,
		minStdDev:   DefaultMinStdDev.Seconds(),
		threshold:   DefaultPhiThreshold,
		acceptPause: DefaultAcceptablePause.Seconds(),
	}
	for _, o := range opts {
		o(d)
	}
	return d
}

// Report records a heartbeat arriving at time now.
func (d *Detector) Report(now time.Time) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.hasSeen {
		interval := now.Sub(d.lastSeen).Seconds()
		if interval > 0 {
			d.window.add(interval)
		}
	}
	d.lastSeen = now
	d.hasSeen = true
}

// phiLocked computes φ; caller must hold d.mu.
func (d *Detector) phiLocked(now time.Time) (float64, error) {
	if !d.hasSeen {
		return 0, ErrNoHeartbeat
	}
	if d.window.count < d.minSamples {
		return 0, nil
	}
	elapsed := now.Sub(d.lastSeen).Seconds() - d.acceptPause
	if elapsed <= 0 {
		return 0, nil
	}
	stdDev := math.Sqrt(d.window.variance())
	if stdDev < d.minStdDev {
		stdDev = d.minStdDev
	}
	// P_later(elapsed): probability a heartbeat arrives later than this.
	// Computed via Erfc (not 1-CDF) to keep precision in the far tail.
	pLater := survival(elapsed, d.window.mean, stdDev)
	if pLater <= 0 {
		// Beyond float64 resolution: effectively certain the node is dead.
		return math.MaxFloat64, nil
	}
	return -math.Log10(pLater), nil
}

// Phi returns the current suspicion level φ at time now.
// φ is 0 while fewer than minSamples intervals have been observed.
func (d *Detector) Phi(now time.Time) (float64, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.phiLocked(now)
}

// IsSuspected reports whether φ(now) exceeds the configured threshold,
// i.e. whether the node's lease should be reclaimed.
func (d *Detector) IsSuspected(now time.Time) bool {
	phi, err := d.Phi(now)
	if err != nil {
		return false
	}
	return phi >= d.threshold
}

// Threshold returns the configured φ threshold.
func (d *Detector) Threshold() float64 {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.threshold
}

// Snapshot returns a consistent view of the detector state at time now.
func (d *Detector) Snapshot(now time.Time) Stats {
	d.mu.Lock()
	defer d.mu.Unlock()
	s := Stats{Samples: d.window.count, LastSeen: d.lastSeen}
	if d.window.count > 0 {
		s.Mean = duration(d.window.mean)
		s.StdDev = duration(math.Sqrt(d.window.variance()))
	}
	if phi, err := d.phiLocked(now); err == nil {
		s.Phi = phi
		s.Suspected = phi >= d.threshold
	}
	return s
}

func duration(seconds float64) time.Duration {
	return time.Duration(seconds * float64(time.Second))
}

// survival returns P(X > x) for X ~ N(mean, stdDev^2), accurate far
// into the tail where 1-CDF would underflow to zero.
func survival(x, mean, stdDev float64) float64 {
	return 0.5 * math.Erfc((x-mean)/(stdDev*math.Sqrt2))
}
