// Package cdcprobe implements a CDC (Change Data Capture) pipeline
// performance probe: the source periodically injects heartbeat rows
// carrying high-precision nanosecond timestamps into the data stream,
// the consumer reads them and computes end-to-end replication lag.
package cdcprobe

import (
	"sync/atomic"
	"time"
)

// Heartbeat is a probe row injected by the source into the CDC stream.
// SourceTime is captured with nanosecond precision at the moment the
// row is written to the source database.
type Heartbeat struct {
	ID         int64
	SourceTime time.Time
}

// Lag returns the end-to-end replication lag of this heartbeat,
// measured from source write until target apply completes.
func (h Heartbeat) Lag(now time.Time) time.Duration {
	return now.Sub(h.SourceTime)
}

// Source periodically injects heartbeat probe rows into the stream.
// Its injection pace is governed by a dynamic RateLimiter so the
// monitor can throttle it when lag exceeds the alert threshold.
type Source struct {
	interval time.Duration
	limiter  *RateLimiter
	out      chan<- Heartbeat

	seq      atomic.Int64
	injected atomic.Int64
	stop     chan struct{}
	done     chan struct{}
}

// NewSource creates a heartbeat source emitting at most one heartbeat
// per interval, additionally paced by limiter.
func NewSource(interval time.Duration, limiter *RateLimiter, out chan<- Heartbeat) *Source {
	return &Source{
		interval: interval,
		limiter:  limiter,
		out:      out,
		stop:     make(chan struct{}),
		done:     make(chan struct{}),
	}
}

// Run starts injecting heartbeats until Stop is called.
func (s *Source) Run() {
	defer close(s.done)
	ticker := time.NewTicker(s.interval)
	defer ticker.Stop()
	for {
		select {
		case <-s.stop:
			return
		case <-ticker.C:
			if s.limiter != nil {
				if !s.limiter.Allow() {
					continue // throttled: skip this injection slot
				}
			}
			hb := Heartbeat{
				ID:         s.seq.Add(1),
				SourceTime: time.Now(),
			}
			select {
			case s.out <- hb:
				s.injected.Add(1)
			case <-s.stop:
				return
			}
		}
	}
}

// Stop halts injection and waits for the source goroutine to exit.
func (s *Source) Stop() {
	close(s.stop)
	<-s.done
}

// Injected reports how many heartbeat rows have been written so far.
func (s *Source) Injected() int64 { return s.injected.Load() }
