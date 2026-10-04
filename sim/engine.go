// Package sim is a deterministic discrete-time, event-driven simulation
// framework used for million-step regression testing and bug reproduction
// of the compressed bitmap implementation.
//
// Determinism contract: given the same seed, an Engine produces exactly the
// same op stream, event schedule, bitmap states and rolling checksums on
// every run, on every platform. All randomness flows from a single seeded
// source; sparse-id fills use a per-step sub-seed so replays of recorded
// op logs reproduce bit-identical states.
package sim

import (
	"container/heap"
	"math/rand"

	"compressedbitmap/bitmap"
)

// OpKind identifies a bitmap operation.
type OpKind uint8

const (
	OpAddSparse OpKind = iota // insert Count scattered ids in [Start, End)
	OpAddRange                // insert dense interval [Start, End)
	OpAnd                     // Target = Target AND Other
	OpOr                      // Target = Target OR Other
	OpXor                     // Target = Target XOR Other
	OpOptimize                // recompact Target
)

func (k OpKind) String() string {
	switch k {
	case OpAddSparse:
		return "ADD_SPARSE"
	case OpAddRange:
		return "ADD_RANGE"
	case OpAnd:
		return "AND"
	case OpOr:
		return "OR"
	case OpXor:
		return "XOR"
	case OpOptimize:
		return "OPTIMIZE"
	}
	return "UNKNOWN"
}

// Op is a fully deterministic bitmap operation.
type Op struct {
	Kind   OpKind
	Target int
	Other  int
	Start  uint64
	End    uint64
	Count  int
}

type event struct {
	time uint64
	seq  uint64
	op   Op
}

// eventHeap is a priority queue ordered by (time, seq): ties break by
// insertion sequence, keeping the schedule deterministic.
type eventHeap []event

func (h eventHeap) Len() int { return len(h) }
func (h eventHeap) Less(i, j int) bool {
	if h[i].time != h[j].time {
		return h[i].time < h[j].time
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

// Engine is a deterministic discrete-time event-driven simulator.
type Engine struct {
	seed      int64
	Now       uint64 // simulation clock
	steps     uint64 // applied op count
	seq       uint64 // event sequence counter
	rng       *rand.Rand
	fillRng   *rand.Rand
	pq        eventHeap
	Bitmaps   []*bitmap.Bitmap
	IDSpace   uint64 // ids live in [0, IDSpace)
	MaxRange  uint64 // max dense range length
	MaxSparse int    // max ids per sparse insert
	sum       uint64 // rolling state checksum
}

// NewEngine creates an engine with numBitmaps empty bitmaps.
func NewEngine(seed int64, numBitmaps int) *Engine {
	e := &Engine{
		seed:      seed,
		rng:       rand.New(rand.NewSource(seed)),
		fillRng:   rand.New(rand.NewSource(seed ^ 0x5bd1e995)),
		IDSpace:   1 << 18,
		MaxRange:  1 << 10,
		MaxSparse: 16,
		sum:       14695981039346656037,
	}
	e.Bitmaps = make([]*bitmap.Bitmap, numBitmaps)
	for i := range e.Bitmaps {
		e.Bitmaps[i] = bitmap.New()
	}
	heap.Init(&e.pq)
	return e
}

// Checksum returns the rolling state checksum.
func (e *Engine) Checksum() uint64 { return e.sum }

// Steps returns the number of applied ops.
func (e *Engine) Steps() uint64 { return e.steps }

// Schedule enqueues op to run delay ticks after Now.
func (e *Engine) Schedule(delay uint64, op Op) {
	heap.Push(&e.pq, event{time: e.Now + delay, seq: e.seq, op: op})
	e.seq++
}

// genOp draws the next random op from the seeded stream.
func (e *Engine) genOp() Op {
	r := e.rng
	n := len(e.Bitmaps)
	op := Op{Target: r.Intn(n)}
	switch r.Intn(6) {
	case 0:
		op.Kind = OpAddSparse
		op.Start = uint64(r.Int63n(int64(e.IDSpace)))
		span := uint64(r.Int63n(int64(e.IDSpace/4))) + 1
		op.End = op.Start + span
		if op.End > e.IDSpace {
			op.End = e.IDSpace
		}
		op.Count = 1 + r.Intn(e.MaxSparse)
	case 1:
		op.Kind = OpAddRange
		op.Start = uint64(r.Int63n(int64(e.IDSpace)))
		op.End = op.Start + 1 + uint64(r.Int63n(int64(e.MaxRange)))
		if op.End > e.IDSpace {
			op.End = e.IDSpace
		}
	case 2:
		op.Kind = OpAnd
		op.Other = r.Intn(n)
	case 3:
		op.Kind = OpOr
		op.Other = r.Intn(n)
	case 4:
		op.Kind = OpXor
		op.Other = r.Intn(n)
	default:
		op.Kind = OpOptimize
	}
	return op
}

// apply executes op and folds the resulting state into the rolling checksum.
func (e *Engine) apply(op Op) {
	switch op.Kind {
	case OpAddSparse:
		// fillRng is only advanced inside apply, so replays of the
		// recorded op stream regenerate identical ids.
		b := e.Bitmaps[op.Target]
		span := op.End - op.Start
		if span > 0 {
			for i := 0; i < op.Count; i++ {
				b.Add(op.Start + uint64(e.fillRng.Int63n(int64(span))))
			}
		}
	case OpAddRange:
		e.Bitmaps[op.Target].AddRange(op.Start, op.End)
	case OpAnd:
		e.Bitmaps[op.Target] = e.Bitmaps[op.Target].And(e.Bitmaps[op.Other])
	case OpOr:
		e.Bitmaps[op.Target] = e.Bitmaps[op.Target].Or(e.Bitmaps[op.Other])
	case OpXor:
		e.Bitmaps[op.Target] = e.Bitmaps[op.Target].Xor(e.Bitmaps[op.Other])
	case OpOptimize:
		e.Bitmaps[op.Target].Optimize()
	}
	e.steps++
	// Fold the touched bitmap's structure and the op into the rolling hash.
	h := e.sum
	h = (h << 5) | (h >> 59) // rotate-left 5
	h ^= e.Bitmaps[op.Target].StructureChecksum()
	h ^= uint64(op.Kind)*2654435761 + uint64(op.Target)*97 + uint64(op.Other)*13
	e.sum = h
}

// Step advances the simulation by one event and returns its op.
func (e *Engine) Step() Op {
	if e.pq.Len() == 0 {
		// Refill the queue with a deterministic batch of future events.
		for i := 0; i < 16; i++ {
			e.Schedule(uint64(e.rng.Intn(8)), e.genOp())
		}
	}
	ev := heap.Pop(&e.pq).(event)
	e.Now = ev.time
	e.apply(ev.op)
	return ev.op
}

// Run executes steps events, invoking hook after each applied op with the
// 1-based step number, the op and the rolling checksum.
func (e *Engine) Run(steps uint64, hook func(step uint64, op Op, sum uint64)) uint64 {
	for i := uint64(0); i < steps; i++ {
		op := e.Step()
		if hook != nil {
			hook(e.steps, op, e.sum)
		}
	}
	return e.sum
}
