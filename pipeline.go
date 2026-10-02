// Package logpipe implements a high-throughput, strictly ordered log
// pipeline. Multiple producers concurrently append records; every record
// receives a monotonic LSN. Explicit durability barriers can be inserted
// into the stream: no record positioned after a barrier is published
// until every record before the barrier is durable on disk. On failure
// the pipeline rolls back and releases all unconfirmed in-memory buffers.
package logpipe

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"sort"
	"sync"
)

var (
	// ErrPipelineClosed is returned by operations on a closed pipeline.
	ErrPipelineClosed = errors.New("logpipe: pipeline is closed")
	// ErrPipelineFailed is returned after the pipeline has rolled back.
	ErrPipelineFailed = errors.New("logpipe: pipeline failed, unconfirmed buffers rolled back")
)

// memEntryOverhead approximates the per-entry bookkeeping cost (slice
// header, struct, map/slice amortisation) so memory accounting reflects
// more than the raw payload.
const memEntryOverhead = 96

type entry struct {
	lsn  uint64
	data []byte
}

// Options configures a Pipeline.
type Options struct {
	// FailAtLSN simulates a write failure when the flusher reaches the
	// given LSN. Zero disables fault injection. Intended for tests and
	// demos; set before the pipeline is used and never mutated after.
	FailAtLSN uint64
}

// Pipeline is a concurrent, ordered log stream backed by a WAL file.
type Pipeline struct {
	mu   sync.Mutex
	cond *sync.Cond

	wal  *os.File
	opts Options

	nextLSN    uint64   // next LSN to assign (LSNs start at 1)
	durableLSN uint64   // highest LSN known to be fsynced
	pending    []entry  // appended but not yet durable, LSN-ordered
	barriers   []uint64 // pending barrier positions, ascending

	memCount int64 // unconfirmed buffered entries
	memBytes int64 // unconfirmed buffered bytes (payload + overhead)

	closed  bool
	failed  bool
	failErr error

	flushDone chan struct{}
}

// Open creates a pipeline writing to path with default options.
func Open(path string) (*Pipeline, error) {
	return OpenWithOptions(path, Options{})
}

// OpenWithOptions creates a pipeline writing to path.
func OpenWithOptions(path string, opts Options) (*Pipeline, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return nil, err
	}
	p := &Pipeline{
		wal:       f,
		opts:      opts,
		nextLSN:   1,
		flushDone: make(chan struct{}),
	}
	p.cond = sync.NewCond(&p.mu)
	go p.flushLoop()
	return p, nil
}

// Append publishes data into the stream and returns its monotonic LSN.
// If an undurable barrier sits in front of this record, Append blocks until
// every record up to the barrier position is durable on disk.
func (p *Pipeline) Append(data []byte) (uint64, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for {
		p.popBarriersLocked()
		if p.failed {
			return 0, ErrPipelineFailed
		}
		if p.closed {
			return 0, ErrPipelineClosed
		}
		// Block only if this record would be positioned after a barrier
		// that is not yet durable.
		if len(p.barriers) == 0 || p.barriers[0] >= p.nextLSN {
			break
		}
		p.cond.Wait()
	}
	lsn := p.nextLSN
	p.nextLSN++
	buf := make([]byte, len(data))
	copy(buf, data)
	p.pending = append(p.pending, entry{lsn: lsn, data: buf})
	p.memCount++
	p.memBytes += int64(len(buf)) + memEntryOverhead
	p.cond.Broadcast()
	return lsn, nil
}

// InsertBarrier inserts a durability barrier at the current end of the
// stream and blocks until every record before it is durable.
func (p *Pipeline) InsertBarrier() error {
	p.mu.Lock()
	lsn := p.nextLSN - 1
	p.mu.Unlock()
	return p.InsertBarrierAt(lsn)
}

// InsertBarrierAt inserts a durability barrier immediately after the given LSN
// and blocks until every record with LSN <= lsn is durable on disk. While
// the barrier is pending, records positioned after it may not be
// published; concurrent Appends block until the barrier completes.
func (p *Pipeline) InsertBarrierAt(lsn uint64) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.failed {
		return ErrPipelineFailed
	}
	if p.closed {
		return ErrPipelineClosed
	}
	i := sort.Search(len(p.barriers), func(i int) bool { return p.barriers[i] >= lsn })
	if i == len(p.barriers) || p.barriers[i] != lsn {
		p.barriers = append(p.barriers, 0)
		copy(p.barriers[i+1:], p.barriers[i:])
		p.barriers[i] = lsn
	}
	for p.durableLSN < lsn && !p.failed && !p.closed {
		p.cond.Wait()
	}
	if p.failed {
		return ErrPipelineFailed
	}
	if p.durableLSN < lsn {
		return ErrPipelineClosed
	}
	return nil
}

// DurableLSN returns the highest LSN known to be durable on disk.
func (p *Pipeline) DurableLSN() uint64 {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.durableLSN
}

// MemStats returns the number of unconfirmed buffered entries and their
// accounted bytes. After a rollback both are zero (memory is conserved).
func (p *Pipeline) MemStats() (entries int64, bytes int64) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.memCount, p.memBytes
}

// Err returns the error that failed the pipeline, or nil.
func (p *Pipeline) Err() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.failErr
}

// Done closes when the flusher has exited (after Close or a failure).
func (p *Pipeline) Done() <-chan struct{} {
	return p.flushDone
}

// Close flushes all pending records, then closes the WAL. After a
// failure there is nothing to flush; Close simply releases the file.
func (p *Pipeline) Close() error {
	p.mu.Lock()
	if !p.closed {
		p.closed = true
		p.cond.Broadcast()
	}
	p.mu.Unlock()
	<-p.flushDone
	return p.wal.Close()
}

// popBarriersLocked drops barriers already covered by durableLSN.
func (p *Pipeline) popBarriersLocked() {
	for len(p.barriers) > 0 && p.barriers[0] <= p.durableLSN {
		p.barriers = p.barriers[1:]
	}
}

// rollback fails the pipeline: every unconfirmed buffered record is
// discarded and its accounted memory released. Durable records are
// untouched. All blocked producers and barriers wake up with an error.
func (p *Pipeline) rollback(err error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.failed {
		return
	}
	p.failed = true
	p.failErr = err
	p.pending = nil
	p.memCount = 0
	p.memBytes = 0
	_ = p.wal.Sync()
	p.cond.Broadcast()
}

// flushLoop is the single writer goroutine. It consumes pending records in
// LSN order, writes them to the WAL, fsyncs each batch, and only then
// advances durableLSN and releases the corresponding buffer memory.
func (p *Pipeline) flushLoop() {
	defer close(p.flushDone)
	for {
		p.mu.Lock()
		for len(p.pending) == 0 && !p.closed && !p.failed {
			p.cond.Wait()
		}
		if len(p.pending) == 0 {
			p.mu.Unlock()
			return
		}
		batch := p.pending
		p.pending = nil
		failAt := p.opts.FailAtLSN
		p.mu.Unlock()

		var buf bytes.Buffer
		written := 0
		injectFailure := false
		for i, e := range batch {
			if failAt != 0 && e.lsn >= failAt {
				injectFailure = true
				break
			}
			fmt.Fprintf(&buf, "%d %d %s\n", e.lsn, len(e.data), e.data)
			written = i + 1
		}
		if written > 0 {
			if _, err := p.wal.Write(buf.Bytes()); err != nil {
				p.rollback(err)
				return
			}
			if err := p.wal.Sync(); err != nil {
				p.rollback(err)
				return
			}
			p.mu.Lock()
			p.durableLSN = batch[written-1].lsn
			for _, e := range batch[:written] {
				p.memCount--
				p.memBytes -= int64(len(e.data)) + memEntryOverhead
			}
			p.popBarriersLocked()
			p.cond.Broadcast()
			p.mu.Unlock()
		}
		if injectFailure {
			// Records before failAt in this batch were written and
			// fsynced above; the failing record and everything after it
			// is rolled back.
			p.rollback(fmt.Errorf("logpipe: write failed at LSN %d", failAt))
			return
		}
	}
}
