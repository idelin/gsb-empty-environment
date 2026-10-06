package viewbuilder

import (
	"context"
	"errors"
	"maps"
	"sync/atomic"
	"time"
)

var (
	// errBuildFailed is returned when a build attempt fails (including
	// injected failures); Run retries after it.
	errBuildFailed  = errors.New("viewbuilder: build failed")
	errDisconnected = errors.New("viewbuilder: change stream disconnected")
)

// View is an immutable snapshot of the materialized state. Views are
// swapped atomically and never mutated after publication, so readers
// need no locks.
type View struct {
	data map[string][]byte
	seq  uint64
}

// Get returns the value for key as of the view's sequence.
func (v *View) Get(key string) ([]byte, bool) {
	val, ok := v.data[key]
	return val, ok
}

// Len returns the number of live keys in the view.
func (v *View) Len() int { return len(v.data) }

// Seq returns the highest log sequence reflected in the view.
func (v *View) Seq() uint64 { return v.seq }

// Faults controls fault injection on the builder side.
type Faults struct {
	// FailBuilds makes the next n build attempts fail before any work.
	FailBuilds int
}

// Builder incrementally maintains a View over a Log and publishes it
// atomically.
type Builder struct {
	log    *Log
	faults Faults
	cur    atomic.Pointer[View]
}

func NewBuilder(l *Log, f Faults) *Builder {
	b := &Builder{log: l, faults: f}
	b.cur.Store(&View{data: map[string][]byte{}})
	return b
}

// View returns the currently published view.
func (b *Builder) View() *View { return b.cur.Load() }

// Get is a public query against the currently published view.
func (b *Builder) Get(key string) ([]byte, bool) { return b.View().Get(key) }

// Len is a public query against the currently published view.
func (b *Builder) Len() int { return b.View().Len() }

// Seq returns the sequence of the currently published view.
func (b *Builder) Seq() uint64 { return b.View().Seq() }

// applyOrdered buffers ev and applies the contiguous prefix of pending
// events starting at next. Events older than next (replayed after a lost
// acknowledgement) are dropped. Delete events are kept in the buffer and
// applied like any other event. It returns the new next sequence.
func applyOrdered(data map[string][]byte, pending map[uint64]Event, next uint64, ev Event) uint64 {
	if ev.Seq < next {
		return next
	}
	pending[ev.Seq] = ev
	for {
		e, ok := pending[next]
		if !ok {
			return next
		}
		if e.Del {
			delete(data, e.Key)
		} else {
			data[e.Key] = e.Value
		}
		delete(pending, next)
		next++
	}
}

// buildOnce scans a fixed snapshot, replays the changes that arrived
// during the scan (buffering out-of-order events until the contiguous
// prefix is complete), and atomically swaps the new view in once the
// replay reaches the barrier. It then acknowledges the switch and
// returns the open subscription so the caller can keep following it.
func (b *Builder) buildOnce(ctx context.Context) (uint64, chan update, error) {
	if b.faults.FailBuilds > 0 {
		b.faults.FailBuilds--
		return 0, nil, errBuildFailed
	}
	snap, switchSeq := b.log.Snapshot()
	ch := b.log.subscribe(switchSeq + 1)
	data := snap
	next := switchSeq + 1
	pending := map[uint64]Event{}
	for {
		select {
		case <-ctx.Done():
			b.log.unsubscribe(ch)
			return 0, nil, ctx.Err()
		case u, ok := <-ch:
			if !ok {
				return 0, nil, errDisconnected
			}
			if u.barrier {
				b.cur.Store(&View{data: data, seq: next - 1})
				b.log.ackSwitch(next - 1)
				return next, ch, nil
			}
			next = applyOrdered(data, pending, next, u.ev)
		}
	}
}

// follow keeps the published view current by applying live events with
// copy-on-write swaps until the stream disconnects or ctx is cancelled.
func (b *Builder) follow(ctx context.Context, ch chan update, next uint64) {
	pending := map[uint64]Event{}
	for {
		select {
		case <-ctx.Done():
			b.log.unsubscribe(ch)
			return
		case u, ok := <-ch:
			if !ok {
				return // disconnected; Run resubscribes and replays unacked events
			}
			if u.barrier {
				continue
			}
			cur := b.cur.Load()
			if u.ev.Seq <= cur.seq {
				continue // already applied (replayed after a lost ack)
			}
			data := maps.Clone(cur.data)
			next = applyOrdered(data, pending, next, u.ev)
			b.cur.Store(&View{data: data, seq: next - 1})
		}
	}
}

// Build performs a single snapshot build and atomic swap. A non-nil
// error means no switch happened and the caller may retry.
func (b *Builder) Build(ctx context.Context) error {
	_, ch, err := b.buildOnce(ctx)
	if ch != nil {
		b.log.unsubscribe(ch)
	}
	return err
}

// Run keeps the view current until ctx is cancelled: it builds (retrying
// on failure), follows the change stream, and reconnects on
// disconnection, relying on log replay plus sequence dedupe to stay
// correct across lost switch acknowledgements.
func (b *Builder) Run(ctx context.Context) {
	for ctx.Err() == nil {
		next, ch, err := b.buildOnce(ctx)
		if err != nil {
			select {
			case <-ctx.Done():
			case <-time.After(10 * time.Millisecond):
			}
			continue
		}
		b.follow(ctx, ch, next)
	}
}
