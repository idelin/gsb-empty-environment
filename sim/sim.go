// Package sim provides a deterministic single-process event simulator:
// a virtual clock, an event queue, and a controllable in-memory network.
// Communication delays, message loss/duplication/reordering and node
// crashes are all expressed as scheduled events, so every scenario is
// fully reproducible from a seed.
package sim

import (
	"container/heap"
	"fmt"
	"math/rand"
)

// Event is a scheduled callback at a virtual time.
type Event struct {
	at    int64
	seq   int
	label string
	fn    func()
}

type eventHeap []Event

func (h eventHeap) Len() int { return len(h) }
func (h eventHeap) Less(i, j int) bool {
	if h[i].at != h[j].at {
		return h[i].at < h[j].at
	}
	return h[i].seq < h[j].seq
}
func (h eventHeap) Swap(i, j int)       { h[i], h[j] = h[j], h[i] }
func (h *eventHeap) Push(x interface{}) { *h = append(*h, x.(Event)) }
func (h *eventHeap) Pop() interface{} {
	old := *h
	n := len(old)
	e := old[n-1]
	*h = old[:n-1]
	return e
}

// Engine is the discrete-event simulator core.
type Engine struct {
	now    int64
	seq    int
	h      eventHeap
	Trace  bool
	TraceL []string
	// OnStep runs after every executed event; used by invariant checkers.
	OnStep func()
}

func NewEngine() *Engine { return &Engine{} }

// Now returns the current virtual time.
func (e *Engine) Now() int64 { return e.now }

// After schedules fn to run d ticks after Now.
func (e *Engine) After(d int64, label string, fn func()) {
	heap.Push(&e.h, Event{at: e.now + d, seq: e.seq, label: label, fn: fn})
	e.seq++
}

// Step executes the next event. Returns false when the queue is empty.
func (e *Engine) Step() bool {
	if len(e.h) == 0 {
		return false
	}
	ev := heap.Pop(&e.h).(Event)
	e.now = ev.at
	if e.Trace {
		e.TraceL = append(e.TraceL, fmt.Sprintf("t=%04d %s", ev.at, ev.label))
	}
	ev.fn()
	if e.OnStep != nil {
		e.OnStep()
	}
	return true
}

// Run drains the event queue.
func (e *Engine) Run() {
	for e.Step() {
	}
}

// Pending reports how many events remain.
func (e *Engine) Pending() int { return len(e.h) }

// Net is an in-memory network with controllable delay, loss and
// duplication. All randomness comes from a seeded RNG: deterministic.
type Net struct {
	eng      *Engine
	rng      *rand.Rand
	handlers map[string]func(interface{})

	MinDelay, MaxDelay int64
	DropProb, DupProb  float64
	// Filter lets a scenario inspect/transform every message.
	// Return (msg, true) to drop, (replacement, false) to deliver.
	Filter func(from, to string, msg interface{}) (interface{}, bool)

	Sent, Delivered, Dropped, Duplicated int
}

func NewNet(eng *Engine, seed int64) *Net {
	return &Net{
		eng:      eng,
		rng:      rand.New(rand.NewSource(seed)),
		handlers: map[string]func(interface{}){},
		MinDelay: 1,
		MaxDelay: 5,
	}
}

// Register attaches a delivery handler to an address.
func (n *Net) Register(addr string, h func(interface{})) { n.handlers[addr] = h }

// Send enqueues a message for delivery with random delay/loss/dup.
func (n *Net) Send(from, to, label string, msg interface{}) {
	n.Sent++
	if n.Filter != nil {
		repl, drop := n.Filter(from, to, msg)
		if drop {
			n.Dropped++
			return
		}
		msg = repl
	}
	if n.rng.Float64() < n.DropProb {
		n.Dropped++
		return
	}
	span := n.MaxDelay - n.MinDelay + 1
	d := n.MinDelay + n.rng.Int63n(span)
	n.eng.After(d, "net:deliver "+label+" "+from+"->"+to, func() {
		n.Delivered++
		n.handlers[to](msg)
	})
	if n.rng.Float64() < n.DupProb {
		n.Duplicated++
		d2 := d + 1 + n.rng.Int63n(span)
		n.eng.After(d2, "net:deliver(dup) "+label+" "+from+"->"+to, func() {
			n.Delivered++
			n.handlers[to](msg)
		})
	}
}
