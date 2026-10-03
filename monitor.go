package cdcprobe

import (
	"fmt"
	"io"
	"strings"
	"sync"
	"time"
)

// Alert is emitted when end-to-end lag crosses the alert threshold.
type Alert struct {
	Time      time.Time
	Lag       time.Duration
	Threshold time.Duration
	Message   string
}

// Sample is one lag measurement point.
type Sample struct {
	Time time.Time
	Lag  time.Duration
}

// Monitor consumes heartbeats delivered to the target, computes
// end-to-end replication lag for each, raises alerts when lag exceeds
// the threshold, and dynamically throttles the source limiter.
type Monitor struct {
	threshold  time.Duration
	limiter    *RateLimiter
	window     int // max samples retained for the chart
	recoverFor int // consecutive OK samples before restoring rate

	mu       sync.Mutex
	samples  []Sample
	alerts   []Alert
	okStreak int

	alertCh chan Alert
	done    chan struct{}
}

// NewMonitor creates a lag monitor. threshold is the alert boundary
// (e.g. 1000ms); limiter may be nil to disable dynamic throttling.
func NewMonitor(threshold time.Duration, limiter *RateLimiter, window int) *Monitor {
	return &Monitor{
		threshold:  threshold,
		limiter:    limiter,
		window:     window,
		recoverFor: 5,
		alertCh:    make(chan Alert, 64),
		done:       make(chan struct{}),
	}
}

// Alerts returns the channel on which lag-breach alerts are published.
func (m *Monitor) Alerts() <-chan Alert { return m.alertCh }

// Run consumes heartbeats until in is closed.
func (m *Monitor) Run(in <-chan Heartbeat) {
	defer close(m.done)
	defer close(m.alertCh)
	for hb := range in {
		m.Observe(hb)
	}
}

// Done closes when the monitor has finished consuming.
func (m *Monitor) Done() <-chan struct{} { return m.done }

// Observe records one heartbeat, computes its end-to-end lag, and
// applies the alert / dynamic-throttling policy.
func (m *Monitor) Observe(hb Heartbeat) Sample {
	now := time.Now()
	lag := hb.Lag(now)

	m.mu.Lock()
	m.samples = append(m.samples, Sample{Time: now, Lag: lag})
	if len(m.samples) > m.window {
		m.samples = m.samples[len(m.samples)-m.window:]
	}
	breach := lag > m.threshold
	if breach {
		m.okStreak = 0
	} else {
		m.okStreak++
	}
	recover := !breach && m.okStreak >= m.recoverFor
	m.mu.Unlock()

	if breach {
		alert := Alert{
			Time:      now,
			Lag:       lag,
			Threshold: m.threshold,
			Message:   fmt.Sprintf("ALERT: end-to-end lag %v exceeds threshold %v", lag, m.threshold),
		}
		m.mu.Lock()
		m.alerts = append(m.alerts, alert)
		m.mu.Unlock()
		if m.limiter != nil {
			m.limiter.Throttle(0.5) // dynamic throttling: halve source rate
		}
		select {
		case m.alertCh <- alert:
		default:
		}
	} else if recover && m.limiter != nil && m.limiter.Throttled() {
		m.limiter.Restore()
	}
	return Sample{Time: now, Lag: lag}
}

// Samples returns a snapshot of the retained lag samples.
func (m *Monitor) Samples() []Sample {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]Sample, len(m.samples))
	copy(out, m.samples)
	return out
}

// AlertLog returns a snapshot of all raised alerts.
func (m *Monitor) AlertLog() []Alert {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]Alert, len(m.alerts))
	copy(out, m.alerts)
	return out
}

// RenderChart draws the retained lag samples as an ASCII line chart,
// with a dashed marker row at the alert threshold.
func (m *Monitor) RenderChart(w io.Writer, width, height int) {
	samples := m.Samples()
	fmt.Fprintf(w, "End-to-End Replication Lag (threshold %v)\n", m.threshold)
	if len(samples) == 0 {
		fmt.Fprintln(w, "(no samples)")
		return
	}
	if len(samples) > width {
		samples = samples[len(samples)-width:]
	}
	maxLag := m.threshold * 2
	for _, s := range samples {
		if s.Lag > maxLag {
			maxLag = s.Lag
		}
	}
	if maxLag <= 0 {
		maxLag = time.Millisecond
	}

	grid := make([][]rune, height)
	for r := range grid {
		grid[r] = make([]rune, len(samples))
		for c := range grid[r] {
			grid[r][c] = ' '
		}
	}
	// threshold marker row
	thRow := height - 1 - int(float64(m.threshold)/float64(maxLag)*float64(height-1))
	if thRow >= 0 && thRow < height {
		for c := range grid[thRow] {
			grid[thRow][c] = '-'
		}
	}
	for c, s := range samples {
		row := height - 1 - int(float64(s.Lag)/float64(maxLag)*float64(height-1))
		if row < 0 {
			row = 0
		}
		if row >= height {
			row = height - 1
		}
		mark := '*'
		if s.Lag > m.threshold {
			mark = '!'
		}
		grid[row][c] = mark
	}
	for r := 0; r < height; r++ {
		var lagLabel time.Duration
		switch r {
		case 0:
			lagLabel = maxLag
		case height - 1:
			lagLabel = 0
		default:
			lagLabel = time.Duration(float64(maxLag) * float64(height-1-r) / float64(height-1))
		}
		fmt.Fprintf(w, "%9s |%s|\n", lagLabel.Round(time.Millisecond), string(grid[r]))
	}
	fmt.Fprintf(w, "%9s +%s\n", "", strings.Repeat("-", len(samples)))
	fmt.Fprintf(w, " legend: '*' ok  '!' lag>%v  '-' threshold line\n", m.threshold)
}
