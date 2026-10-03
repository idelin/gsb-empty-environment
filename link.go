package cdcprobe

import (
	"sync"
	"time"
)

// Link simulates the replication path between source and target:
// network transport plus target-side apply. A base latency is always
// applied; extra latency models transient network congestion.
type Link struct {
	mu    sync.Mutex
	base  time.Duration
	extra time.Duration // congestion penalty
	apply time.Duration // target apply cost
	in    <-chan Heartbeat
	out   chan Heartbeat
	done  chan struct{}
}

// NewLink creates a replication link reading heartbeats from in.
// base is the normal one-way network latency, apply the target apply time.
func NewLink(in <-chan Heartbeat, base, apply time.Duration) *Link {
	return &Link{
		base:  base,
		apply: apply,
		in:    in,
		out:   make(chan Heartbeat, 1024),
		done:  make(chan struct{}),
	}
}

// Out returns the channel of heartbeats delivered to the target.
func (l *Link) Out() <-chan Heartbeat { return l.out }

// SetCongestion adds (or clears) extra transport latency, simulating
// transient network congestion.
func (l *Link) SetCongestion(d time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.extra = d
}

// Run pumps heartbeats through the link until the input closes.
// Each heartbeat is transported concurrently (the network is
// pipelined, not store-and-forward), so one slow packet does not
// block those behind it.
func (l *Link) Run() {
	defer close(l.done)
	var wg sync.WaitGroup
	for hb := range l.in {
		l.mu.Lock()
		delay := l.base + l.extra + l.apply
		l.mu.Unlock()
		wg.Add(1)
		go func(hb Heartbeat, delay time.Duration) {
			defer wg.Done()
			timer := time.NewTimer(delay)
			<-timer.C
			l.out <- hb
		}(hb, delay)
	}
	wg.Wait()
	close(l.out)
}

// Done closes when the link has fully drained.
func (l *Link) Done() <-chan struct{} { return l.done }
