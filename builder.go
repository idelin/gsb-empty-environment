package viewbuilder

import (
	"errors"
	"sync"
	"sync/atomic"
)

// Faults groups the fault-injection hooks used by the builder.
type Faults struct {
	// FailScan, when non-nil, is consulted before a snapshot scan; a
	// non-nil error aborts the build.
	FailScan func() error
	// FailReplay, when non-nil, is consulted before each replay round
	// at the view's current watermark; a non-nil error aborts the build.
	FailReplay func(seq uint64) error
	// FailSwap, when non-nil, is consulted just before the atomic
	// switch; a non-nil error aborts the build.
	FailSwap func() error
	// AfterScan, when non-nil, runs after the snapshot scan and before
	// the switch target is fixed. Tests use it to commit the changes
	// that a concurrent writer would make during the scan window.
	AfterScan func()
}

// reorderBuffer holds out-of-order events and releases only the
// contiguous prefix starting at next. Every event, deletes included,
// is retained until it is released or superseded.
type reorderBuffer struct {
	next uint64
	pend map[uint64]Event
}

func newReorderBuffer(next uint64) *reorderBuffer {
	return &reorderBuffer{next: next, pend: make(map[uint64]Event)}
}

// add buffers an event; duplicates and already-applied events are dropped.
func (r *reorderBuffer) add(ev Event) {
	if ev.Seq < r.next {
		return
	}
	if _, ok := r.pend[ev.Seq]; !ok {
		r.pend[ev.Seq] = ev
	}
}

// drain releases the contiguous prefix starting at next. A gap stops
// the drain; the missing events must be fetched to fill the prefix.
func (r *reorderBuffer) drain() []Event {
	var out []Event
	for {
		ev, ok := r.pend[r.next]
		if !ok {
			return out
		}
		delete(r.pend, r.next)
		out = append(out, ev)
		r.next++
	}
}

// Builder scans a fixed snapshot, replays the changes committed during
// the scan, and atomically switches the serving view once the switch
// sequence is reached.
type Builder struct {
	src    *Source
	faults Faults

	live atomic.Value // *View currently serving

	mu   sync.Mutex // serializes Rebuild/CatchUp
	gen  uint64
	last uint64 // switch seq of the last successful build

	// Stats for tests.
	Builds   int64
	Replays  int64
	AcksSent int64
	AcksLost int64
}

// NewBuilder returns a builder serving an empty view until the first
// successful build.
func NewBuilder(src *Source, f Faults) *Builder {
	b := &Builder{src: src, faults: f}
	b.live.Store(newView(0))
	return b
}

// View returns the currently serving view.
func (b *Builder) View() *View { return b.live.Load().(*View) }

// SwitchSeq returns the switch sequence of the last successful build.
func (b *Builder) SwitchSeq() uint64 {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.last
}

// replayTo pulls events from the source and applies them in order to v
// until v.Seq() reaches target. Out-of-order deliveries are buffered;
// gaps are filled by re-polling the complete source log.
func (b *Builder) replayTo(v *View, target uint64) error {
	buf := newReorderBuffer(v.Seq() + 1)
	for v.Seq() < target {
		if b.faults.FailReplay != nil {
			if err := b.faults.FailReplay(v.Seq()); err != nil {
				return err
			}
		}
		batch := b.src.Poll(v.Seq(), 0)
		for _, ev := range batch {
			buf.add(ev)
		}
		contiguous := buf.drain()
		if len(contiguous) == 0 {
			return errors.New("viewbuilder: no contiguous event available")
		}
		for _, ev := range contiguous {
			if ev.Seq > target {
				break
			}
			v.apply(ev)
			atomic.AddInt64(&b.Replays, 1)
		}
	}
	return nil
}

// ackLoop delivers the switch acknowledgment, retrying while acks are
// lost. Recording on the source is idempotent per generation, so a
// retry after an ambiguous loss is safe.
func (b *Builder) ackLoop(ack Ack) {
	for {
		atomic.AddInt64(&b.AcksSent, 1)
		if b.src.SendAck(ack) {
			return
		}
		atomic.AddInt64(&b.AcksLost, 1)
	}
}

// Rebuild performs one full build cycle: snapshot scan, replay of the
// changes committed during the scan up to the switch sequence, atomic
// switch of the serving view, and a (retried) switch acknowledgment.
// On an injected failure the old view keeps serving untouched.
func (b *Builder) Rebuild() (uint64, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.faults.FailScan != nil {
		if err := b.faults.FailScan(); err != nil {
			return 0, err
		}
	}
	snapSeq, snap := b.src.Snapshot()
	if b.faults.AfterScan != nil {
		b.faults.AfterScan()
	}

	b.gen++
	gen := b.gen
	nv := newView(gen)
	for k, val := range snap {
		nv.data[k] = val
	}
	nv.seq = snapSeq

	switchSeq := b.src.LatestSeq()
	if err := b.replayTo(nv, switchSeq); err != nil {
		return 0, err
	}

	if b.faults.FailSwap != nil {
		if err := b.faults.FailSwap(); err != nil {
			return 0, err
		}
	}
	b.live.Store(nv) // atomic replacement of the old view
	b.last = switchSeq
	atomic.AddInt64(&b.Builds, 1)

	b.ackLoop(Ack{Generation: gen, Seq: switchSeq})
	return switchSeq, nil
}

// CatchUp brings the serving view up to the source's latest sequence
// by applying the contiguous event stream in place.
func (b *Builder) CatchUp() (uint64, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	target := b.src.LatestSeq()
	if err := b.replayTo(b.View(), target); err != nil {
		return 0, err
	}
	return target, nil
}
