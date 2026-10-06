package seqstore

import (
	"context"
	"sort"
	"sync"
)

// version is one committed value of a key, tagged with the unified
// sequence number of the transaction that wrote it.
type version struct {
	seq uint64
	val int64
}

// Replica is one table's copy of the unified commit log. Entries may
// arrive out of order; they are buffered and applied strictly in
// sequence order, so the watermark only ever advances contiguously and
// never skips a transaction. Values are kept per version so historical
// reads at any applied sequence number stay answerable.
type Replica struct {
	name      string
	mu        sync.Mutex
	watermark uint64
	pending   map[uint64]LogEntry
	data      map[string][]version // key -> versions ascending by seq
	changed   chan struct{}        // closed and replaced on every Receive
}

func NewReplica(name string) *Replica {
	return &Replica{
		name:    name,
		pending: make(map[uint64]LogEntry),
		data:    make(map[string][]version),
		changed: make(chan struct{}),
	}
}

// Receive buffers one log entry and applies every entry that has
// become contiguous. Entries for other tables still advance the
// watermark, because it tracks the unified log, not per-table writes.
func (r *Replica) Receive(e LogEntry) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if e.Seq <= r.watermark {
		return
	}
	r.pending[e.Seq] = e
	for {
		next, ok := r.pending[r.watermark+1]
		if !ok {
			break
		}
		delete(r.pending, next.Seq)
		for k, v := range next.Writes[r.name] {
			r.data[k] = append(r.data[k], version{seq: next.Seq, val: v})
		}
		r.watermark = next.Seq
	}
	close(r.changed)
	r.changed = make(chan struct{})
}

// Watermark returns the highest contiguously applied sequence number.
func (r *Replica) Watermark() uint64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.watermark
}

// WaitFor blocks until the watermark reaches seq or ctx is done.
func (r *Replica) WaitFor(ctx context.Context, seq uint64) error {
	for {
		r.mu.Lock()
		if r.watermark >= seq {
			r.mu.Unlock()
			return nil
		}
		ch := r.changed
		r.mu.Unlock()
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ch:
		}
	}
}

// readAt returns the value of key as of seq. The caller must have
// verified seq <= Watermark so no committed write <= seq is missing.
func (r *Replica) readAt(key string, seq uint64) (int64, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	vs := r.data[key]
	i := sort.Search(len(vs), func(i int) bool { return vs[i].seq > seq })
	if i == 0 {
		return 0, false
	}
	return vs[i-1].val, true
}
