// Package netsim is a deterministic discrete-event network simulator built
// around a single bottleneck link with a shallow FIFO queue. It implements
// tail-drop loss and deterministic threshold-based RED/ECN marking, and drives
// one or more bbr.BBR senders through a virtual clock so identical
// configuration always produces identical trajectories.
package netsim

import (
	"container/heap"
	"time"

	"prescreen.local/ps-93493e0dc5/bbr"
)

// LinkConfig describes the shared bottleneck link.
type LinkConfig struct {
	// RateBps is the bottleneck service rate in bytes per second.
	RateBps int64
	// QueuePackets is the shallow FIFO buffer capacity in packets; arrivals
	// beyond it are tail-dropped.
	QueuePackets int
	// ECNThreshold marks every arriving packet CE once the queue occupancy
	// reaches this many packets. Zero disables ECN marking.
	ECNThreshold int
	// PropDelay is the one-way propagation delay.
	PropDelay time.Duration
	// MSS is the packet size in bytes.
	MSS uint32
	// TickInterval bounds the idle wake-up granularity of every sender.
	TickInterval time.Duration
}

// DefaultLinkConfig returns a 10 Mbps bottleneck with a shallow 40-packet
// buffer (the BDP at 20ms one-way / 40ms RTT is ~35 packets, so the buffer is
// deliberately only slightly larger than one BDP), deterministic threshold
// ECN marking (from queue length 30) and 20ms one-way propagation. The marker
// sits above the steady-state BDP queue depth so cruising is unmarked while
// over-eager STARTUP/UP bursts that overfill the buffer are signalled
// promptly, exercising BBRv2's explicit-signal early-exit and bounded
// back-off paths. Stress tests use a smaller QueuePackets for the classic
// shallow-buffer regime.
func DefaultLinkConfig() LinkConfig {
	return LinkConfig{
		RateBps:      1_250_000, // 10 Mbit/s
		QueuePackets: 40,
		ECNThreshold: 30,
		PropDelay:    20 * time.Millisecond,
		MSS:          1448,
		TickInterval: 10 * time.Millisecond,
	}
}

type eventType uint8

const (
	evWake       eventType = iota // sender-side send/tick opportunity
	evArrival                     // packet arrives at the bottleneck queue
	evTxComplete                  // bottleneck finished serving one packet
	evAck                         // ACK reaches the sender
	evRTO                         // retransmission timeout at the sender
)

type event struct {
	at     int64 // nanoseconds on the virtual clock
	seq    int64 // insertion tie-breaker for deterministic ordering
	typ    eventType
	flowID int
	gen    int64
	pkt    *packet
}

type eventHeap []event

func (h eventHeap) Len() int { return len(h) }
func (h eventHeap) Less(i, j int) bool {
	if h[i].at != h[j].at {
		return h[i].at < h[j].at
	}
	return h[i].seq < h[j].seq
}
func (h eventHeap) Swap(i, j int)       { h[i], h[j] = h[j], h[i] }
func (h *eventHeap) Push(x interface{}) { *h = append(*h, x.(event)) }
func (h *eventHeap) Pop() interface{} {
	old := *h
	n := len(old)
	it := old[n-1]
	*h = old[:n-1]
	return it
}

// Simulator is the deterministic bottleneck engine.
type Simulator struct {
	cfg   LinkConfig
	now   int64
	flows []*Flow

	events eventHeap
	evSeq  int64

	// bottleneck server state
	queue     []*packet
	busyUntil int64

	totalDrops int
	totalECN   int
}

// New creates an engine with the given link configuration.
func New(cfg LinkConfig) *Simulator {
	return &Simulator{cfg: cfg, events: make(eventHeap, 0)}
}

// Now returns the current virtual time in nanoseconds.
func (s *Simulator) Now() int64 { return s.now }

// AddFlow registers a sender. appRateBps limits the application offer rate in
// bytes/second; a non-positive value means an always-backlogged application.
// bbrCfg may be nil to select the BBRv2 defaults.
func (s *Simulator) AddFlow(id int, appRateBps int64, bbrCfg *bbr.Config) *Flow {
	f := &Flow{
		id:          id,
		sim:         s,
		bbr:         bbr.New(bbrCfg),
		appRateBps:  appRateBps,
		outstanding: make(map[int]*packet),
		rcvSeqs:     make(map[int]bool),
		nextSeq:     1,
		rcvNext:     1,
		sndUna:      1,
		mss:         int(s.cfg.MSS),
	}
	// Initial burst credit so STARTUP can fill the initial window immediately.
	f.pacingTokens = float64(f.bbr.Cwnd()) * float64(f.mss)
	f.appTokens = f.pacingTokens
	s.flows = append(s.flows, f)
	s.scheduleWake(f, 0)
	return f
}

// Flows returns all registered senders.
func (s *Simulator) Flows() []*Flow { return s.flows }

// FlowByID looks up a sender.
func (s *Simulator) FlowByID(id int) *Flow {
	for _, f := range s.flows {
		if f.id == id {
			return f
		}
	}
	return nil
}

// Run advances the virtual clock until no events remain or time reaches untilNs.
func (s *Simulator) Run(untilNs int64) {
	heap.Init(&s.events)
	for s.events.Len() > 0 {
		ev := heap.Pop(&s.events).(event)
		if ev.at > untilNs {
			heap.Push(&s.events, ev)
			break
		}
		s.now = ev.at
		switch ev.typ {
		case evWake:
			if f := s.flow(ev.flowID); f != nil && ev.gen == f.wakeGen {
				f.wake(ev.at)
			}
		case evArrival:
			s.enqueue(ev.pkt)
		case evTxComplete:
			s.onTxComplete(ev)
		case evAck:
			if f := s.flow(ev.flowID); f != nil {
				f.onAck(ev.pkt, ev.at)
			}
		case evRTO:
			if f := s.flow(ev.flowID); f != nil && ev.gen == f.rtoGen {
				f.onRTO(ev.at)
			}
		}
	}
	s.now = untilNs
}

func (s *Simulator) flow(id int) *Flow {
	for _, f := range s.flows {
		if f.id == id {
			return f
		}
	}
	return nil
}

func (s *Simulator) push(ev event) {
	s.evSeq++
	ev.seq = s.evSeq
	heap.Push(&s.events, ev)
}

func (s *Simulator) scheduleWake(f *Flow, at int64) {
	f.wakeGen++
	s.push(event{at: at, typ: evWake, flowID: f.id, gen: f.wakeGen})
}

// enqueue models arrival at the bottleneck: deterministic CE marking once the
// queue passes the ECN threshold, tail-drop beyond capacity.
func (s *Simulator) enqueue(p *packet) {
	if len(s.queue) >= s.cfg.QueuePackets {
		p.dropped = true
		s.totalDrops++
		// Dropped packets stay in the sender's outstanding map and are
		// declared lost via the duplicate-ACK process at the sender.
		return
	}
	if s.cfg.ECNThreshold > 0 && len(s.queue) >= s.cfg.ECNThreshold {
		p.ecn = true
		s.totalECN++
		p.flow().stats.ECN++
	}
	s.queue = append(s.queue, p)
	if s.busyUntil <= s.now {
		s.scheduleHead()
	}
}

func (s *Simulator) scheduleHead() {
	head := s.queue[0]
	start := s.now
	if s.busyUntil > start {
		start = s.busyUntil
	}
	service := int64(head.size) * int64(time.Second) / s.cfg.RateBps
	s.busyUntil = start + service
	s.push(event{at: s.busyUntil, typ: evTxComplete, flowID: head.flowID, pkt: head})
}

func (s *Simulator) onTxComplete(ev event) {
	head := ev.pkt
	if len(s.queue) == 0 || s.queue[0] != head {
		// Defensive: stale event, ignore.
		return
	}
	s.queue = s.queue[1:]

	// Receiver arrival after one-way propagation delay. Every arriving data
	// packet triggers an ACK carrying the current cumulative receive pointer.
	f := head.flow()
	f.onReceive(head.seq)
	f.stats.Delivered++
	head.ackNum = f.rcvNext
	s.push(event{
		at:     ev.at + s.cfg.PropDelay.Nanoseconds(),
		typ:    evAck,
		flowID: f.id,
		pkt:    head,
	})

	if len(s.queue) > 0 {
		s.scheduleHead()
	}
}

// FlowStats summarizes one sender's observed traffic.
type FlowStats struct {
	Sent      int
	Delivered int
	Lost      int
	ECN       int
}

// SimStats is the end-of-run summary.
type SimStats struct {
	Elapsed    time.Duration
	QueueDrops int
	ECNMarks   int
	Flows      map[int]FlowStats
}

// Stats snapshots engine and per-flow counters.
func (s *Simulator) Stats() SimStats {
	out := SimStats{
		Elapsed:    time.Duration(s.now),
		QueueDrops: s.totalDrops,
		ECNMarks:   s.totalECN,
		Flows:      make(map[int]FlowStats),
	}
	for _, f := range s.flows {
		out.Flows[f.id] = f.stats
	}
	return out
}
