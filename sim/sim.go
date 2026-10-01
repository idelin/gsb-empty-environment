// Package sim is a discrete-event network simulator with integer-tick time.
// Packets, queues and the shared link exist only as in-memory events; no
// real network traffic is ever generated.
//
// Multiple RED queues share one link, served by a deficit-round-robin (DRR)
// scheduler for fairness. Randomness for RED decisions is keyed by packet
// (event) identity, so replaying a run or processing it in chunks yields
// bit-identical sampling and an identical trajectory digest.
package sim

import (
	"container/heap"
	"fmt"
	"hash"
	"hash/fnv"

	"redsim/red"
)

// Packet is one simulated packet.
type Packet struct {
	ID      uint64
	Flow    int
	Size    int // bytes
	Arrival uint64
}

const (
	evArrival = iota
	evDeparture
)

type event struct {
	tick    uint64
	seq     uint64
	kind    int
	flow    int
	pktID   uint64
	size    int
	queue   int
	arrival uint64
}

type eventHeap []event

func (h eventHeap) Len() int { return len(h) }
func (h eventHeap) Less(i, j int) bool {
	if h[i].tick != h[j].tick {
		return h[i].tick < h[j].tick
	}
	return h[i].seq < h[j].seq
}
func (h eventHeap) Swap(i, j int)       { h[i], h[j] = h[j], h[i] }
func (h *eventHeap) Push(x interface{}) { *h = append(*h, x.(event)) }
func (h *eventHeap) Pop() interface{} {
	old := *h
	n := len(old)
	e := old[n-1]
	*h = old[:n-1]
	return e
}

// Queue is a FIFO byte queue guarded by one RED instance.
type Queue struct {
	Name  string
	RED   *red.RED
	buf   []Packet
	bytes int
}

// Len returns the number of buffered packets.
func (q *Queue) Len() int { return len(q.buf) }

// Bytes returns the buffered bytes.
func (q *Queue) Bytes() int { return q.bytes }

// Head returns the oldest buffered packet.
func (q *Queue) Head() (Packet, bool) {
	if len(q.buf) == 0 {
		return Packet{}, false
	}
	return q.buf[0], true
}

func (q *Queue) push(p Packet) {
	q.buf = append(q.buf, p)
	q.bytes += p.Size
}

func (q *Queue) pop(now uint64) Packet {
	p := q.buf[0]
	q.buf = q.buf[1:]
	q.bytes -= p.Size
	if len(q.buf) == 0 {
		q.RED.NoteQueueEmpty(now)
	}
	return p
}

// FlowStats are per-flow counters. MaxDelay is reported next to the mean so
// that long-term starvation cannot hide behind a low average delay.
type FlowStats struct {
	Arrived        uint64
	Completed      uint64
	Dropped        uint64
	BytesArrived   uint64
	BytesCompleted uint64
	BytesDropped   uint64
	TotalDelay     uint64
	MaxDelay       uint64
}

// MeanDelay returns the mean queueing+service delay of completed packets.
func (s FlowStats) MeanDelay() float64 {
	if s.Completed == 0 {
		return 0
	}
	return float64(s.TotalDelay) / float64(s.Completed)
}

// DropRecord is the logged basis of one dropped packet.
type DropRecord struct {
	Tick     uint64
	Flow     int
	Packet   uint64
	Size     int
	Decision red.Decision
}

// Config configures the shared link and the fair scheduler.
type Config struct {
	LinkRateBytesPerTick int // link service rate
	QuantumBytes         int // DRR quantum per queue per round
}

// Network is a set of RED queues sharing one link via DRR scheduling.
type Network struct {
	cfg    Config
	src    red.KeyedSource
	queues []*Queue
	stats  []FlowStats

	now uint64
	seq uint64
	ev  eventHeap

	linkBusy bool
	deficit  []int
	rrNext   int
	fresh    bool // true when rrNext has not been charged quantum this round

	pktSeq uint64

	// Drops logs the basis of every dropped packet, in drop order.
	Drops  []DropRecord
	digest hash.Hash64
}

// New builds a network with one RED queue per entry of params.
func New(cfg Config, params []red.Params, seed uint64) (*Network, error) {
	if cfg.LinkRateBytesPerTick <= 0 {
		return nil, fmt.Errorf("sim: LinkRateBytesPerTick must be > 0")
	}
	if cfg.QuantumBytes <= 0 {
		return nil, fmt.Errorf("sim: QuantumBytes must be > 0")
	}
	if len(params) == 0 {
		return nil, fmt.Errorf("sim: at least one queue is required")
	}
	n := &Network{cfg: cfg, src: red.KeyedSource{Seed: seed}, digest: fnv.New64a()}
	for i, p := range params {
		r, err := red.New(p)
		if err != nil {
			return nil, fmt.Errorf("queue %d: %w", i, err)
		}
		n.queues = append(n.queues, &Queue{Name: fmt.Sprintf("q%d", i), RED: r})
	}
	n.stats = make([]FlowStats, len(params))
	n.deficit = make([]int, len(params))
	n.fresh = true
	return n, nil
}

// Now returns the current simulation tick.
func (n *Network) Now() uint64 { return n.now }

// Pending reports whether events remain in the queue.
func (n *Network) Pending() bool { return len(n.ev) > 0 }

// Digest returns a hash of every drop and completion so far; identical
// seeds produce identical digests.
func (n *Network) Digest() uint64 { return n.digest.Sum64() }

// Stats returns a copy of the per-flow statistics.
func (n *Network) Stats() []FlowStats {
	cp := make([]FlowStats, len(n.stats))
	copy(cp, n.stats)
	return cp
}

// QueueBytes returns the current buffered bytes per queue.
func (n *Network) QueueBytes() []int {
	out := make([]int, len(n.queues))
	for i, q := range n.queues {
		out[i] = q.bytes
	}
	return out
}

// AddArrival schedules a packet arrival at an integer tick.
func (n *Network) AddArrival(tick uint64, flow, size int) error {
	if flow < 0 || flow >= len(n.queues) {
		return fmt.Errorf("sim: bad flow %d", flow)
	}
	if size <= 0 {
		return fmt.Errorf("sim: size must be > 0")
	}
	if tick < n.now {
		return fmt.Errorf("sim: arrival tick %d is in the past (now %d)", tick, n.now)
	}
	id := n.pktSeq
	n.pktSeq++
	n.pushEvent(event{tick: tick, kind: evArrival, flow: flow, pktID: id, size: size, queue: flow})
	return nil
}

func (n *Network) pushEvent(e event) {
	e.seq = n.seq
	n.seq++
	heap.Push(&n.ev, e)
}

// Run advances simulation time up to and including tick until. Calling Run
// in chunks yields exactly the same trajectory as one long Run, because all
// randomness is keyed by event identity rather than PRNG position.
func (n *Network) Run(until uint64) {
	if until < n.now {
		until = n.now
	}
	for len(n.ev) > 0 {
		if n.ev[0].tick > until {
			break
		}
		e := heap.Pop(&n.ev).(event)
		n.now = e.tick
		n.process(e)
	}
	n.now = until
}

func (n *Network) process(e event) {
	if e.kind == evArrival {
		n.arrive(e)
	} else {
		n.depart(e)
	}
}

func (n *Network) arrive(e event) {
	q := n.queues[e.queue]
	st := &n.stats[e.flow]
	st.Arrived++
	st.BytesArrived += uint64(e.size)

	dec := q.RED.Decide(e.tick, q.bytes, e.size, n.src, e.pktID)
	if dec.Verdict == red.Accept {
		if q.bytes+e.size > q.RED.Params().CapacityBytes {
			dec = red.Decision{
				Verdict: red.DropOverflow, Avg: dec.Avg,
				BaseProb: dec.BaseProb, Prob: dec.Prob, Draw: dec.Draw,
				Reason: fmt.Sprintf("tail drop: queue %dB + packet %dB exceeds capacity %dB",
					q.bytes, e.size, q.RED.Params().CapacityBytes),
			}
		} else {
			q.push(Packet{ID: e.pktID, Flow: e.flow, Size: e.size, Arrival: e.tick})
			n.schedule()
			return
		}
	}
	st.Dropped++
	st.BytesDropped += uint64(e.size)
	n.Drops = append(n.Drops, DropRecord{
		Tick: e.tick, Flow: e.flow, Packet: e.pktID, Size: e.size, Decision: dec,
	})
	fmt.Fprintf(n.digest, "D tick=%d flow=%d pkt=%d size=%d verdict=%s\n",
		e.tick, e.flow, e.pktID, e.size, dec.Verdict)
}

func (n *Network) depart(e event) {
	n.linkBusy = false
	st := &n.stats[e.flow]
	st.Completed++
	st.BytesCompleted += uint64(e.size)
	delay := e.tick - e.arrival
	st.TotalDelay += delay
	if delay > st.MaxDelay {
		st.MaxDelay = delay
	}
	fmt.Fprintf(n.digest, "C tick=%d flow=%d pkt=%d delay=%d\n",
		e.tick, e.flow, e.pktID, delay)
	n.schedule()
}

// schedule starts link service on the next packet chosen by deficit round
// robin: a queue accrues QuantumBytes of deficit once per round, when the
// round-robin pointer moves to it, and may keep sending while its head
// packet fits the remaining deficit. If a full sweep finds only packets
// larger than the remaining deficits, a new round begins and backlogged
// queues accrue another quantum, so even packets larger than one quantum
// are eventually served.
func (n *Network) schedule() {
	if n.linkBusy {
		return
	}
	nq := len(n.queues)
	for {
		backlogged := false
		for i := 0; i < nq; i++ {
			idx := n.rrNext
			q := n.queues[idx]
			head, ok := q.Head()
			if !ok {
				n.deficit[idx] = 0
				n.rrNext = (idx + 1) % nq
				n.fresh = true
				continue
			}
			backlogged = true
			if n.fresh {
				n.deficit[idx] += n.cfg.QuantumBytes
				n.fresh = false
			}
			if head.Size > n.deficit[idx] {
				n.rrNext = (idx + 1) % nq
				n.fresh = true
				continue
			}
			n.deficit[idx] -= head.Size
			pkt := q.pop(n.now)
			n.linkBusy = true
			service := uint64((pkt.Size + n.cfg.LinkRateBytesPerTick - 1) / n.cfg.LinkRateBytesPerTick)
			n.pushEvent(event{
				tick: n.now + service, kind: evDeparture, flow: pkt.Flow,
				pktID: pkt.ID, size: pkt.Size, queue: idx, arrival: pkt.Arrival,
			})
			return
		}
		if !backlogged {
			return
		}
	}
}
