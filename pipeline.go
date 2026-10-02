// Package logpipe implements a high-throughput, strictly ordered log
// stream pipeline with explicit persistence barriers.
//
// Multiple producers may call Append concurrently; the pipeline assigns
// each record a monotonic LSN and a single flusher goroutine persists
// records to the WAL strictly in LSN order. A barrier guarantees that
// every record before it is flushed and fsynced before any subsequent
// record is written or published. If persistence fails, the pipeline
// rolls back: every unacknowledged in-memory buffer is released and its
// waiter is unblocked with the failure error, keeping the buffer
// accounting conserved (Allocated == Released, Outstanding == 0).
package logpipe

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"sync"
	"sync/atomic"
)

// ErrClosed is returned by Append/InsertBarrier on a closed pipeline.
var ErrClosed = errors.New("logpipe: pipeline is closed")

// Record is a single durable log entry delivered to subscribers.
type Record struct {
	LSN  uint64
	Data []byte
}

// Handle is the acknowledgement token for one appended record. Wait
// resolves with nil once the record is durable and published, or with
// the pipeline failure if it was rolled back.
type Handle struct {
	lsn uint64
	ack chan error
}

// LSN returns the sequence number assigned to the record.
func (h Handle) LSN() uint64 { return h.lsn }

// Wait blocks until the record is durable (nil) or rolled back (error).
func (h Handle) Wait() error { return <-h.ack }

// Barrier is the acknowledgement token for a persistence barrier. Wait
// resolves once every record ahead of the barrier is flushed and fsynced.
type Barrier struct{ ack chan error }

// Wait blocks until the barrier is durable (nil) or the pipeline fails.
func (b Barrier) Wait() error { return <-b.ack }

// WAL is the durability target of the pipeline (e.g. an *os.File).
type WAL interface {
	io.Writer
	Sync() error
}

// Options tunes a Pipeline.
type Options struct {
	// BatchSize is the maximum number of records buffered between
	// flush+fsync cycles. Defaults to 64.
	BatchSize int
	// PublishBuffer is the capacity of the subscriber channel.
	// Defaults to 1024.
	PublishBuffer int
	// FaultLSN, when non-zero, makes the flusher fail with FaultErr
	// instead of persisting the record with that LSN. Test hook.
	FaultLSN uint64
	// FaultErr is the error injected at FaultLSN. Defaults to a
	// generic "injected fault" error.
	FaultErr error
}

// entry is one slot in the ordered flush queue. A barrier entry carries
// no data and occupies a position between two records.
type entry struct {
	lsn     uint64
	data    []byte
	barrier bool
	ack     chan error
}

// Pipeline is the ordered log stream pipeline. It is safe for
// concurrent use by multiple producers.
type Pipeline struct {
	mu     sync.Mutex
	cond   sync.Cond
	queue  []entry
	seq    uint64
	closed bool
	failed error

	// barrierAt holds barriers pinned to a specific LSN: after the
	// record with that LSN is persisted, a flush+fsync must happen
	// before any later record is written.
	barrierAt map[uint64][]chan error

	wal WAL
	w   *bufio.Writer
	opt Options

	// pending holds records written to the bufio layer but not yet
	// fsynced; only the flusher goroutine touches it.
	pending []entry

	lastWritten atomic.Int64
	durable     atomic.Int64

	allocated atomic.Int64
	released  atomic.Int64

	publishCh chan Record
	done      chan struct{}
}

// New creates a Pipeline persisting to wal and starts its flusher.
func New(wal WAL, opt Options) *Pipeline {
	if opt.BatchSize <= 0 {
		opt.BatchSize = 64
	}
	if opt.PublishBuffer <= 0 {
		opt.PublishBuffer = 1024
	}
	if opt.FaultLSN != 0 && opt.FaultErr == nil {
		opt.FaultErr = errors.New("logpipe: injected fault")
	}
	p := &Pipeline{
		wal:       wal,
		w:         bufio.NewWriter(wal),
		opt:       opt,
		barrierAt: make(map[uint64][]chan error),
		publishCh: make(chan Record, opt.PublishBuffer),
		done:      make(chan struct{}),
	}
	p.cond.L = &p.mu
	go p.run()
	return p
}

// Append assigns the next monotonic LSN to data and queues it for
// persistence. It returns a Handle whose Wait resolves on durability or
// rollback. Append never blocks on disk I/O.
func (p *Pipeline) Append(data []byte) (Handle, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.failed != nil {
		return Handle{}, p.failed
	}
	if p.closed {
		return Handle{}, ErrClosed
	}
	p.seq++
	e := entry{lsn: p.seq, data: append([]byte(nil), data...), ack: make(chan error, 1)}
	p.queue = append(p.queue, e)
	p.allocated.Add(1)
	p.cond.Signal()
	return Handle{lsn: e.lsn, ack: e.ack}, nil
}

// InsertBarrier appends a barrier at the current tail of the stream:
// every record appended so far must be durable before any later record
// is written or published.
func (p *Pipeline) InsertBarrier() (Barrier, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.failed != nil {
		return Barrier{}, p.failed
	}
	if p.closed {
		return Barrier{}, ErrClosed
	}
	ack := make(chan error, 1)
	p.queue = append(p.queue, entry{barrier: true, ack: ack})
	p.cond.Signal()
	return Barrier{ack: ack}, nil
}

// InsertBarrierAt pins a barrier to a specific LSN: once the record
// with that LSN is persisted, the flusher performs a flush+fsync before
// writing any later record. If that LSN was already written, the
// barrier degrades to a tail barrier.
func (p *Pipeline) InsertBarrierAt(lsn uint64) (Barrier, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.failed != nil {
		return Barrier{}, p.failed
	}
	if p.closed {
		return Barrier{}, ErrClosed
	}
	ack := make(chan error, 1)
	if lsn <= uint64(p.lastWritten.Load()) {
		p.queue = append(p.queue, entry{barrier: true, ack: ack})
		p.cond.Signal()
	} else {
		p.barrierAt[lsn] = append(p.barrierAt[lsn], ack)
	}
	return Barrier{ack: ack}, nil
}

// Subscribe returns the channel on which records are delivered, in LSN
// order, only after they are durable. The channel is closed when the
// flusher exits (normal close or failure). Exactly one goroutine should
// drain it.
func (p *Pipeline) Subscribe() <-chan Record { return p.publishCh }

// Close marks the pipeline closed, drains and persists every queued
// record, and waits for the flusher to exit. It returns the failure
// error if the pipeline rolled back.
func (p *Pipeline) Close() error {
	p.mu.Lock()
	p.closed = true
	p.mu.Unlock()
	p.cond.Broadcast()
	<-p.done
	return p.Failed()
}

// Failed reports the error that put the pipeline into the failed
// state, or nil.
func (p *Pipeline) Failed() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.failed
}

// DurableLSN is the highest LSN known to be flushed and fsynced.
func (p *Pipeline) DurableLSN() uint64 { return uint64(p.durable.Load()) }

// Allocated is the total number of record buffers ever allocated.
func (p *Pipeline) Allocated() int64 { return p.allocated.Load() }

// Released is the total number of record buffers released, either by
// durable acknowledgement or by rollback.
func (p *Pipeline) Released() int64 { return p.released.Load() }

// Outstanding is the number of record buffers currently held in memory
// (Allocated - Released). It returns to zero once every appended record
// is either durable or rolled back.
func (p *Pipeline) Outstanding() int64 { return p.allocated.Load() - p.released.Load() }

// run is the single flusher goroutine. It is the only writer to the WAL
// and processes the queue strictly in LSN order.
func (p *Pipeline) run() {
	defer close(p.done)
	defer close(p.publishCh)
	sinceSync := 0
	for {
		p.mu.Lock()
		for len(p.queue) == 0 && !p.closed {
			p.cond.Wait()
		}
		if len(p.queue) == 0 { // closed and drained
			p.mu.Unlock()
			if err := p.syncPoint(); err != nil {
				p.abort(err)
				return
			}
			p.settleBarriers(nil)
			return
		}
		e := p.queue[0]
		p.queue = p.queue[1:]
		empty := len(p.queue) == 0
		p.mu.Unlock()

		if e.barrier {
			if err := p.syncPoint(); err != nil {
				p.abort(err, e)
				return
			}
			e.ack <- nil
			continue
		}
		if p.opt.FaultLSN != 0 && e.lsn == p.opt.FaultLSN {
			p.abort(p.opt.FaultErr, e)
			return
		}
		fmt.Fprintf(p.w, "%d %s\n", e.lsn, e.data)
		p.pending = append(p.pending, e)
		p.lastWritten.Store(int64(e.lsn))
		sinceSync++

		_, pinned := p.barrierAt[e.lsn]
		if pinned || sinceSync >= p.opt.BatchSize || empty {
			if err := p.syncPoint(); err != nil {
				p.abort(err)
				return
			}
			sinceSync = 0
		}
	}
}

// syncPoint flushes the bufio layer, fsyncs the WAL, then publishes and
// acknowledges every pending record and every due pinned barrier.
// Only the flusher goroutine calls it.
func (p *Pipeline) syncPoint() error {
	if err := p.w.Flush(); err != nil {
		return err
	}
	if err := p.wal.Sync(); err != nil {
		return err
	}
	for _, e := range p.pending {
		p.publishCh <- Record{LSN: e.lsn, Data: e.data}
		e.ack <- nil
		p.released.Add(1)
	}
	p.pending = p.pending[:0]
	p.durable.Store(p.lastWritten.Load())
	p.settleBarriers(nil)
	return nil
}

// settleBarriers resolves pinned barriers. With a nil error it resolves
// every barrier whose LSN is already durable; with a non-nil error it
// fails all of them. Caller must not hold p.mu.
func (p *Pipeline) settleBarriers(err error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for lsn, chans := range p.barrierAt {
		if err != nil || lsn <= uint64(p.lastWritten.Load()) {
			for _, c := range chans {
				c <- err
			}
			delete(p.barrierAt, lsn)
		}
	}
}

// abort puts the pipeline into the failed state and rolls back every
// unacknowledged buffer: the pending (written but unsynced) records,
// the extra entries, and everything still queued. Only the flusher
// goroutine calls it, immediately before returning.
func (p *Pipeline) abort(err error, extra ...entry) {
	p.mu.Lock()
	if p.failed == nil {
		p.failed = err
	}
	q := p.queue
	p.queue = nil
	p.mu.Unlock()
	p.cond.Broadcast()

	for _, e := range extra {
		p.rollbackEntry(e, err)
	}
	for _, e := range p.pending {
		p.rollbackEntry(e, err)
	}
	p.pending = nil
	for _, e := range q {
		p.rollbackEntry(e, err)
	}
	p.settleBarriers(err)
}

// rollbackEntry fails one entry and releases its buffer, if any.
func (p *Pipeline) rollbackEntry(e entry, err error) {
	if !e.barrier {
		p.released.Add(1)
	}
	e.ack <- err
}
