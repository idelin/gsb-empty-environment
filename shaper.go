package tbsim

import (
	"container/heap"
	"math"
	"math/big"
)

type eventKind int

const (
	evControl eventKind = iota // controls apply before arrivals at the same tick
	evArrival
	evCheck // readiness re-evaluation (candidate emit)
)

type event struct {
	time int64
	kind eventKind
	seq  int
	gen  int
	pkt  Packet
	op   ControlOp
}

type eventHeap []event

func (h eventHeap) Len() int { return len(h) }
func (h eventHeap) Less(i, j int) bool {
	a, b := h[i], h[j]
	if a.time != b.time {
		return a.time < b.time
	}
	if a.kind != b.kind {
		return a.kind < b.kind
	}
	return a.seq < b.seq
}
func (h eventHeap) Swap(i, j int) { h[i], h[j] = h[j], h[i] }
func (h *eventHeap) Push(x any)   { *h = append(*h, x.(event)) }
func (h *eventHeap) Pop() any     { old := *h; n := len(old); e := old[n-1]; *h = old[:n-1]; return e }

// Shaper is a waiting-style traffic shaper running on a discrete-event
// model: all packets, queues and the single link live on integer ticks, no
// real network traffic is involved. It hops exactly (rational arithmetic)
// from event to event instead of iterating ticks.
type Shaper struct {
	flows    []*Flow
	byName   map[string]*Flow
	buckets  map[string]*Bucket
	now      int64
	lastEmit int64 // one emit per tick (single link)
	evs      eventHeap
	seq      int
	gen      int
	pending  bool
	pendTime int64

	Emitted []EmitRecord
	Dropped []DropRecord
}

func NewShaper(flows []*Flow) *Shaper {
	s := &Shaper{
		byName:   map[string]*Flow{},
		buckets:  map[string]*Bucket{},
		lastEmit: -1,
	}
	s.flows = flows
	for _, f := range flows {
		s.byName[f.Name] = f
		for _, b := range f.Path() {
			s.buckets[b.Name] = b
		}
	}
	return s
}

// Bucket exposes a bucket by name (for audits and hot rate swaps).
func (s *Shaper) Bucket(name string) *Bucket { return s.buckets[name] }

// Flow exposes a flow by name.
func (s *Shaper) Flow(name string) *Flow { return s.byName[name] }

func (s *Shaper) AddArrival(a Arrival) {
	s.seq++
	heap.Push(&s.evs, event{time: a.Time, kind: evArrival, seq: s.seq, pkt: a.Pkt})
}

func (s *Shaper) AddControl(op ControlOp) {
	s.seq++
	heap.Push(&s.evs, event{time: op.Time, kind: evControl, seq: s.seq, op: op})
}

// Run processes every event in time order and returns when the model drains.
func (s *Shaper) Run() {
	for len(s.evs) > 0 {
		ev := heap.Pop(&s.evs).(event)
		s.now = ev.time
		switch ev.kind {
		case evControl:
			s.applyControl(ev.op)
		case evArrival:
			s.arrive(ev.pkt)
		case evCheck:
			s.check(ev.gen)
		}
	}
}

func (s *Shaper) arrive(p Packet) {
	f := s.byName[p.Flow]
	if reason := admit(f, p.Size); reason != "" {
		s.Dropped = append(s.Dropped, DropRecord{Pkt: p, Time: s.now, Reason: reason})
		return
	}
	f.push(p, s.now)
	s.schedule()
}

func (s *Shaper) applyControl(op ControlOp) {
	switch op.Kind {
	case "setrate":
		// Hot rate swap: bucket settles at now first, so no double refill.
		s.buckets[op.Bucket].SetRate(s.now, big.NewRat(op.RateN, op.RateD))
		s.schedule()
	case "setweight":
		// Hot weight swap: touches scheduling only, never token accounting.
		s.byName[op.Flow].Weight = op.Weight
	case "cancel":
		if s.byName[op.Flow].cancel(op.PktID) {
			s.schedule()
		}
	}
}

// readyTime computes, for the head packet of f, the exact earliest tick it
// could pass ALL ancestor buckets, plus the per-bucket wait decomposition.
// The combined constraint is the max over ancestors: no bucket is skipped.
func readyTime(now int64, f *Flow, size int64) (int64, map[string]int64, bool) {
	waits := make(map[string]int64, len(f.Path()))
	var maxw int64
	for _, b := range f.Path() {
		w := b.WaitTicks(now, size)
		if w < 0 {
			return math.MaxInt64, waits, false // zero-rate bucket can never pass
		}
		waits[b.Name] = w
		if w > maxw {
			maxw = w
		}
	}
	return now + maxw, waits, true
}

// schedule evaluates every flow head and arms a check event at the exact
// earliest tick any head packet could pass, but never more than one emit per
// tick (single link).
func (s *Shaper) schedule() {
	best := int64(math.MaxInt64)
	for _, f := range s.flows {
		if len(f.queue) == 0 {
			continue
		}
		head := &f.queue[0]
		r, waits, ok := readyTime(s.now, f, head.pkt.Size)
		head.decision = s.now
		head.waits = waits
		head.readyAt = r
		if ok && r < best {
			best = r
		}
	}
	if best == math.MaxInt64 {
		return
	}
	if t := s.lastEmit + 1; best < t {
		best = t
	}
	if s.pending && s.pendTime <= best {
		return // an earlier or equal check is already armed
	}
	s.pending = true
	s.pendTime = best
	s.gen++
	s.seq++
	heap.Push(&s.evs, event{time: best, kind: evCheck, seq: s.seq, gen: s.gen})
}

// check re-evaluates readiness exactly at the candidate tick: it re-checks
// every ancestor bucket of every head packet (never trusting a stale local
// wait), picks by priority + smooth WRR, consumes and emits.
func (s *Shaper) check(gen int) {
	if gen != s.gen {
		return // stale, superseded by an earlier re-evaluation
	}
	s.pending = false
	var cands []*Flow
	for _, f := range s.flows {
		if len(f.queue) == 0 {
			continue
		}
		ready := true
		for _, b := range f.Path() {
			if !b.Has(s.now, f.queue[0].pkt.Size) {
				ready = false
				break
			}
		}
		if ready {
			cands = append(cands, f)
		}
	}
	if len(cands) == 0 {
		s.schedule()
		return
	}
	f := pick(cands)
	head := f.pop()
	for _, b := range f.Path() {
		b.Consume(s.now, head.pkt.Size)
	}
	s.Emitted = append(s.Emitted, EmitRecord{
		Pkt:      head.pkt,
		Arrival:  head.arrival,
		Emit:     s.now,
		Decision: head.decision,
		ReadyAt:  head.readyAt,
		Waits:    head.waits,
	})
	s.lastEmit = s.now
	s.schedule()
}
