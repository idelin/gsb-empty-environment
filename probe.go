// Package cdcprobe implements a CDC end-to-end performance monitoring probe.
//
// The source side periodically injects heartbeat probe rows carrying a
// high-precision nanosecond timestamp into the data stream. The consumer
// side reads the heartbeat rows after they are replicated and applied on
// the target, and computes the end-to-end replication lag. A monitor
// records lag samples, renders a dynamic latency curve, raises an alert
// when the lag breaks the threshold (default 1000ms), and dynamically
// throttles the source injection rate until the lag recovers.
package cdcprobe

import (
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// DefaultLagThreshold is the default end-to-end lag alert threshold.
const DefaultLagThreshold = 1000 * time.Millisecond

// Heartbeat is a probe row injected into the source data stream.
type Heartbeat struct {
	Seq         uint64 // monotonic probe sequence number
	SrcUnixNano int64  // high-precision source-side write timestamp (ns)
}

// Lag returns the end-to-end replication lag of a heartbeat applied at
// applyUnixNano on the target side.
func (h Heartbeat) Lag(applyUnixNano int64) time.Duration {
	return time.Duration(applyUnixNano - h.SrcUnixNano)
}

// Store is an append-only in-memory row stream simulating a database
// table whose changes are captured by CDC.
type Store struct {
	mu   sync.Mutex
	rows []Heartbeat
	subs []chan Heartbeat
}

// NewStore creates an empty Store.
func NewStore() *Store { return &Store{} }

// Append appends a row and fans it out to all subscribers.
func (s *Store) Append(h Heartbeat) {
	s.mu.Lock()
	s.rows = append(s.rows, h)
	subs := append([]chan Heartbeat(nil), s.subs...)
	s.mu.Unlock()
	for _, ch := range subs {
		ch <- h
	}
}

// Subscribe returns a buffered channel receiving every appended row.
func (s *Store) Subscribe() <-chan Heartbeat {
	ch := make(chan Heartbeat, 4096)
	s.mu.Lock()
	s.subs = append(s.subs, ch)
	s.mu.Unlock()
	return ch
}

// Len returns the number of rows appended so far.
func (s *Store) Len() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.rows)
}

// RateLimiter is a token-bucket rate limiter used for dynamic throttling.
type RateLimiter struct {
	mu       sync.Mutex
	rate     float64 // tokens per second
	tokens   float64
	max      float64
	lastFill time.Time
}

// NewRateLimiter creates a limiter allowing rate events per second.
func NewRateLimiter(rate float64) *RateLimiter {
	return &RateLimiter{rate: rate, tokens: rate, max: rate, lastFill: time.Now()}
}

// Allow reports whether one event may proceed at the current rate.
func (r *RateLimiter) Allow() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	now := time.Now()
	r.tokens += now.Sub(r.lastFill).Seconds() * r.rate
	if r.tokens > r.max {
		r.tokens = r.max
	}
	r.lastFill = now
	if r.tokens >= 1 {
		r.tokens--
		return true
	}
	return false
}

// SetRate dynamically changes the limit (events per second).
func (r *RateLimiter) SetRate(rate float64) {
	r.mu.Lock()
	r.rate = rate
	r.max = rate
	if r.tokens > r.max {
		r.tokens = r.max
	}
	r.mu.Unlock()
}

// Rate returns the current limit in events per second.
func (r *RateLimiter) Rate() float64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.rate
}

// Injector periodically injects heartbeat probe rows into the source.
type Injector struct {
	src      *Store
	limiter  *RateLimiter
	interval time.Duration
	seq      uint64
	stop     chan struct{}
	done     chan struct{}
}

// NewInjector creates an Injector ticking every interval.
func NewInjector(src *Store, limiter *RateLimiter, interval time.Duration) *Injector {
	return &Injector{
		src:      src,
		limiter:  limiter,
		interval: interval,
		stop:     make(chan struct{}),
		done:     make(chan struct{}),
	}
}

// Start begins injecting heartbeats in the background.
func (in *Injector) Start() {
	go func() {
		defer close(in.done)
		ticker := time.NewTicker(in.interval)
		defer ticker.Stop()
		for {
			select {
			case <-in.stop:
				return
			case <-ticker.C:
				if !in.limiter.Allow() {
					continue // throttled: skip this tick
				}
				seq := atomic.AddUint64(&in.seq, 1)
				in.src.Append(Heartbeat{Seq: seq, SrcUnixNano: time.Now().UnixNano()})
			}
		}
	}()
}

// Stop halts injection and waits for the goroutine to exit.
func (in *Injector) Stop() {
	close(in.stop)
	<-in.done
}

// Injected returns the number of heartbeats injected so far.
func (in *Injector) Injected() uint64 { return atomic.LoadUint64(&in.seq) }

// Replicator moves rows from the source store to the target store,
// applying a network delay model to each row.
type Replicator struct {
	src     *Store
	dst     *Store
	latency func() time.Duration
	stop    chan struct{}
	done    chan struct{}
	applied uint64
}

// NewReplicator creates a Replicator; latency is consulted per row and
// models the network delay (including congestion) of the replication link.
func NewReplicator(src, dst *Store, latency func() time.Duration) *Replicator {
	return &Replicator{
		src:     src,
		dst:     dst,
		latency: latency,
		stop:    make(chan struct{}),
		done:    make(chan struct{}),
	}
}

// Start begins replicating rows in the background.
func (r *Replicator) Start() {
	ch := r.src.Subscribe()
	go func() {
		defer close(r.done)
		for {
			select {
			case <-r.stop:
				return
			case h := <-ch:
				if d := r.latency(); d > 0 {
					select {
					case <-time.After(d):
					case <-r.stop:
						return
					}
				}
				r.dst.Append(h)
				atomic.AddUint64(&r.applied, 1)
			}
		}
	}()
}

// Stop halts replication and waits for the goroutine to exit.
func (r *Replicator) Stop() {
	close(r.stop)
	<-r.done
}

// Applied returns the number of rows applied on the target so far.
func (r *Replicator) Applied() uint64 { return atomic.LoadUint64(&r.applied) }

// Sample is one end-to-end lag measurement taken on the target side.
type Sample struct {
	Seq uint64
	At  time.Time
	Lag time.Duration
}

// Alert is raised when the lag breaks the configured threshold.
type Alert struct {
	At  time.Time
	Lag time.Duration
	Msg string
}

// Monitor consumes applied heartbeats on the target, measures end-to-end
// lag, raises alerts and dynamically throttles the source injector.
type Monitor struct {
	dst        *Store
	limiter    *RateLimiter
	threshold  time.Duration
	baseRate   float64
	throttleTo float64
	onAlert    func(Alert)
	onRecover  func()
	mu         sync.Mutex
	samples    []Sample
	alerts     []Alert
	throttled  bool
	stop       chan struct{}
	done       chan struct{}
}

// NewMonitor creates a Monitor. When lag exceeds threshold the limiter is
// throttled to throttleTo events/s; when lag falls back below half the
// threshold the limiter is restored to baseRate events/s.
func NewMonitor(dst *Store, limiter *RateLimiter, threshold time.Duration, baseRate, throttleTo float64) *Monitor {
	return &Monitor{
		dst:        dst,
		limiter:    limiter,
		threshold:  threshold,
		baseRate:   baseRate,
		throttleTo: throttleTo,
		stop:       make(chan struct{}),
		done:       make(chan struct{}),
	}
}

// OnAlert registers a callback invoked once per congestion episode.
func (m *Monitor) OnAlert(fn func(Alert)) { m.onAlert = fn }

// OnRecover registers a callback invoked when the lag recovers.
func (m *Monitor) OnRecover(fn func()) { m.onRecover = fn }

// Start begins consuming applied heartbeats in the background.
func (m *Monitor) Start() {
	ch := m.dst.Subscribe()
	go func() {
		defer close(m.done)
		for {
			select {
			case <-m.stop:
				return
			case h := <-ch:
				now := time.Now()
				m.Record(Sample{Seq: h.Seq, At: now, Lag: h.Lag(now.UnixNano())})
			}
		}
	}()
}

// Stop halts the monitor and waits for the goroutine to exit.
func (m *Monitor) Stop() {
	close(m.stop)
	<-m.done
}

// Record stores one lag sample and applies the alert/throttle logic.
func (m *Monitor) Record(s Sample) {
	m.mu.Lock()
	m.samples = append(m.samples, s)
	m.mu.Unlock()

	if s.Lag > m.threshold {
		m.mu.Lock()
		already := m.throttled
		if !already {
			m.throttled = true
			alert := Alert{
				At:  s.At,
				Lag: s.Lag,
				Msg: fmt.Sprintf("end-to-end lag %v exceeded threshold %v", s.Lag.Round(time.Millisecond), m.threshold),
			}
			m.alerts = append(m.alerts, alert)
			m.mu.Unlock()
			m.limiter.SetRate(m.throttleTo)
			if m.onAlert != nil {
				m.onAlert(alert)
			}
			return
		}
		m.mu.Unlock()
		return
	}

	if s.Lag < m.threshold/2 {
		m.mu.Lock()
		was := m.throttled
		m.throttled = false
		m.mu.Unlock()
		if was {
			m.limiter.SetRate(m.baseRate)
			if m.onRecover != nil {
				m.onRecover()
			}
		}
	}
}

// Samples returns a copy of all recorded lag samples.
func (m *Monitor) Samples() []Sample {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]Sample(nil), m.samples...)
}

// Alerts returns a copy of all raised alerts.
func (m *Monitor) Alerts() []Alert {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]Alert(nil), m.alerts...)
}

// Throttled reports whether the monitor is currently throttling the source.
func (m *Monitor) Throttled() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.throttled
}

// MaxLag returns the largest lag observed so far.
func (m *Monitor) MaxLag() time.Duration {
	m.mu.Lock()
	defer m.mu.Unlock()
	var max time.Duration
	for _, s := range m.samples {
		if s.Lag > max {
			max = s.Lag
		}
	}
	return max
}

// Plot renders lag samples as an ASCII latency curve with the given
// number of columns and rows, marking the alert threshold with '-'.
func Plot(samples []Sample, threshold time.Duration, width, height int) string {
	if len(samples) == 0 || width < 2 || height < 2 {
		return "(no samples)"
	}
	maxLag := threshold
	for _, s := range samples {
		if s.Lag > maxLag {
			maxLag = s.Lag
		}
	}
	if maxLag <= 0 {
		maxLag = time.Millisecond
	}

	cols := make([]time.Duration, width)
	for i, s := range samples {
		c := i * width / len(samples)
		if s.Lag > cols[c] {
			cols[c] = s.Lag
		}
	}
	level := func(d time.Duration) int {
		return int(float64(d) / float64(maxLag) * float64(height-1))
	}
	thresholdRow := level(threshold)

	var b strings.Builder
	fmt.Fprintf(&b, "lag curve (max=%v, threshold=%v, samples=%d)\n",
		maxLag.Round(time.Millisecond), threshold.Round(time.Millisecond), len(samples))
	for row := height - 1; row >= 0; row-- {
		var label string
		switch row {
		case height - 1:
			label = fmt.Sprintf("%8v", maxLag.Round(time.Millisecond))
		case 0:
			label = fmt.Sprintf("%8v", time.Duration(0))
		default:
			label = "        "
		}
		b.WriteString(label)
		b.WriteString(" |")
		for c := 0; c < width; c++ {
			switch {
			case cols[c] > 0 && level(cols[c]) >= row:
				b.WriteByte('#')
			case row == thresholdRow:
				b.WriteByte('-')
			default:
				b.WriteByte(' ')
			}
		}
		b.WriteByte('\n')
	}
	b.WriteString("         +")
	b.WriteString(strings.Repeat("-", width))
	b.WriteString("\n          t0")
	b.WriteString(strings.Repeat(" ", width-4))
	b.WriteString("now\n")
	return b.String()
}
