package sortexec

import (
	"container/heap"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"sort"
	"sync"
	"sync/atomic"
)

// Record is a single sortable row. Batches delivered by a Cursor are
// freshly decoded copies: they stay valid after cancellation or Close.
type Record struct {
	Key     int64
	Payload []byte
}

// ErrClosed is returned by Cursor.Next after the cursor was closed.
var ErrClosed = errors.New("sortexec: cursor closed")

var executorSeq atomic.Int64

// Executor spills sorted runs to disk, merges them, and delivers ordered
// batches. Cancellation in any phase cleans up undelivered files and
// iterators exactly once.
type Executor struct {
	fs        FileSystem
	runSize   int
	batchSize int
	barriers  Barriers
	tracker   *Tracker
	prefix    string
}

// Option configures an Executor.
type Option func(*Executor)

// WithRunSize sets how many records each in-memory sorted run holds.
func WithRunSize(n int) Option {
	return func(e *Executor) {
		if n > 0 {
			e.runSize = n
		}
	}
}

// WithBatchSize sets the maximum records delivered per Cursor.Next call.
func WithBatchSize(n int) Option {
	return func(e *Executor) {
		if n > 0 {
			e.batchSize = n
		}
	}
}

// WithBarriers installs phase barriers (test hooks).
func WithBarriers(b Barriers) Option {
	return func(e *Executor) { e.barriers = b }
}

// WithTracker installs a resource tracker.
func WithTracker(t *Tracker) Option {
	return func(e *Executor) { e.tracker = t }
}

// NewExecutor builds an Executor storing spill files in fs.
func NewExecutor(fs FileSystem, opts ...Option) *Executor {
	e := &Executor{
		fs:        fs,
		runSize:   1024,
		batchSize: 256,
		tracker:   &Tracker{},
		prefix:    fmt.Sprintf("exec-%d-", executorSeq.Add(1)),
	}
	for _, opt := range opts {
		opt(e)
	}
	return e
}

// Run produces sorted runs, merges them, and returns a Cursor over the
// sorted stream. If produce or merge fails or is cancelled, all spill
// files and handles are released before the error is returned.
func (e *Executor) Run(ctx context.Context, input []Record) (*Cursor, error) {
	runs, err := e.produce(ctx, input)
	if err != nil {
		return nil, err
	}
	sorted, err := e.merge(ctx, runs)
	if err != nil {
		return nil, err
	}
	e.barriers.enter(PhaseDeliver)
	r, err := e.fs.Open(sorted)
	if err != nil {
		e.fs.Remove(sorted)
		return nil, fmt.Errorf("sortexec: deliver: %w", err)
	}
	return &Cursor{
		fs:        e.fs,
		name:      sorted,
		r:         newCountedReadFile(r, e.tracker),
		barriers:  e.barriers,
		batchSize: e.batchSize,
	}, nil
}

// produce sorts input in runSize chunks and spills each chunk to a file.
// On cancellation or failure every file it created is removed.
func (e *Executor) produce(ctx context.Context, input []Record) (runs []string, err error) {
	e.barriers.enter(PhaseProduce)
	defer func() {
		if err != nil {
			for _, name := range runs {
				e.fs.Remove(name)
			}
			runs = nil
		}
	}()
	var n int64
	for start := 0; start < len(input); start += e.runSize {
		end := min(start+e.runSize, len(input))
		run := append([]Record(nil), input[start:end]...)
		sort.Slice(run, func(i, j int) bool { return run[i].Key < run[j].Key })

		name := fmt.Sprintf("%srun-%d.dat", e.prefix, len(runs))
		w, cerr := e.fs.Create(name)
		if cerr != nil {
			err = fmt.Errorf("sortexec: produce: %w", cerr)
			return
		}
		cw := newCountedWriteFile(w, e.tracker)
		for _, rec := range run {
			e.barriers.step(PhaseProduce, n)
			n++
			if cerr := ctx.Err(); cerr != nil {
				cw.Close()
				e.fs.Remove(name)
				err = cerr
				return
			}
			if cerr := writeRecord(cw, rec); cerr != nil {
				cw.Close()
				e.fs.Remove(name)
				err = fmt.Errorf("sortexec: produce: %w", cerr)
				return
			}
		}
		if cerr := cw.Close(); cerr != nil {
			e.fs.Remove(name)
			err = fmt.Errorf("sortexec: produce: %w", cerr)
			return
		}
		runs = append(runs, name)
	}
	return runs, nil
}

// merge k-way merges run files into a single sorted file, then removes
// the runs. Iterators are closed exactly once on every path.
func (e *Executor) merge(ctx context.Context, runs []string) (string, error) {
	e.barriers.enter(PhaseMerge)
	out := e.prefix + "sorted.dat"
	cleanup := func(err error) (string, error) {
		for _, name := range runs {
			e.fs.Remove(name)
		}
		e.fs.Remove(out)
		return "", err
	}

	iters := make([]*runIterator, 0, len(runs))
	defer func() {
		for _, it := range iters {
			it.Close()
		}
	}()

	h := &mergeHeap{}
	for _, name := range runs {
		it, err := e.openIterator(name)
		if err != nil {
			return cleanup(fmt.Errorf("sortexec: merge: %w", err))
		}
		iters = append(iters, it)
		rec, err := it.next()
		if err != nil && err != io.EOF {
			return cleanup(fmt.Errorf("sortexec: merge: %w", err))
		}
		if err == nil {
			heap.Push(h, mergeItem{rec: rec, it: it})
		}
	}

	w, err := e.fs.Create(out)
	if err != nil {
		return cleanup(fmt.Errorf("sortexec: merge: %w", err))
	}
	cw := newCountedWriteFile(w, e.tracker)

	var n int64
	for h.Len() > 0 {
		e.barriers.step(PhaseMerge, n)
		n++
		if err := ctx.Err(); err != nil {
			cw.Close()
			return cleanup(err)
		}
		item := heap.Pop(h).(mergeItem)
		if err := writeRecord(cw, item.rec); err != nil {
			cw.Close()
			return cleanup(fmt.Errorf("sortexec: merge: %w", err))
		}
		rec, err := item.it.next()
		if err != nil && err != io.EOF {
			cw.Close()
			return cleanup(fmt.Errorf("sortexec: merge: %w", err))
		}
		if err == nil {
			heap.Push(h, mergeItem{rec: rec, it: item.it})
		}
	}
	if err := cw.Close(); err != nil {
		return cleanup(fmt.Errorf("sortexec: merge: %w", err))
	}
	for _, name := range runs {
		e.fs.Remove(name)
	}
	return out, nil
}

func (e *Executor) openIterator(name string) (*runIterator, error) {
	r, err := e.fs.Open(name)
	if err != nil {
		return nil, err
	}
	return newRunIterator(newCountedReadFile(r, e.tracker), e.tracker), nil
}

// Cursor streams sorted batches from the merged file. Batches already
// returned stay valid after cancellation or Close; the underlying file
// is closed and removed exactly once.
type Cursor struct {
	fs        FileSystem
	name      string
	r         *countedReadFile
	barriers  Barriers
	batchSize int

	mu        sync.Mutex
	closed    bool
	stickyErr error
	delivered int64
}

// Next returns the next batch of up to batchSize records, or io.EOF when
// the stream is exhausted. Cancellation or read failure closes the cursor
// and the error is sticky.
func (c *Cursor) Next(ctx context.Context) ([]Record, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.stickyErr != nil {
		return nil, c.stickyErr
	}
	if c.closed {
		return nil, ErrClosed
	}
	batch := make([]Record, 0, c.batchSize)
	for len(batch) < c.batchSize {
		c.barriers.step(PhaseDeliver, c.delivered)
		if err := ctx.Err(); err != nil {
			c.stickyErr = err
			c.cleanupLocked()
			return nil, err
		}
		rec, err := readRecord(c.r)
		if err == io.EOF {
			break
		}
		if err != nil {
			c.stickyErr = err
			c.cleanupLocked()
			return nil, fmt.Errorf("sortexec: deliver: %w", err)
		}
		batch = append(batch, rec)
		c.delivered++
	}
	if len(batch) == 0 {
		c.cleanupLocked()
		return nil, io.EOF
	}
	return batch, nil
}

// Close releases the merged file. It is idempotent.
func (c *Cursor) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.cleanupLocked()
	return nil
}

func (c *Cursor) cleanupLocked() {
	if c.closed {
		return
	}
	c.closed = true
	c.r.Close()
	c.fs.Remove(c.name)
}

// runIterator decodes records from one run file. Close shuts the file
// exactly once.
type runIterator struct {
	r    *countedReadFile
	tr   *Tracker
	once sync.Once
	err  error
}

func newRunIterator(r *countedReadFile, tr *Tracker) *runIterator {
	tr.iterOpened()
	return &runIterator{r: r, tr: tr}
}

func (it *runIterator) next() (Record, error) {
	return readRecord(it.r)
}

func (it *runIterator) Close() error {
	it.once.Do(func() {
		it.err = it.r.Close()
		it.tr.iterClosed()
	})
	return it.err
}

type mergeItem struct {
	rec Record
	it  *runIterator
}

type mergeHeap []mergeItem

func (h mergeHeap) Len() int            { return len(h) }
func (h mergeHeap) Less(i, j int) bool  { return h[i].rec.Key < h[j].rec.Key }
func (h mergeHeap) Swap(i, j int)       { h[i], h[j] = h[j], h[i] }
func (h *mergeHeap) Push(x interface{}) { *h = append(*h, x.(mergeItem)) }
func (h *mergeHeap) Pop() interface{} {
	old := *h
	n := len(old)
	item := old[n-1]
	*h = old[:n-1]
	return item
}

func writeRecord(w io.Writer, rec Record) error {
	var hdr [12]byte
	binary.BigEndian.PutUint64(hdr[0:8], uint64(rec.Key))
	binary.BigEndian.PutUint32(hdr[8:12], uint32(len(rec.Payload)))
	if _, err := w.Write(hdr[:]); err != nil {
		return err
	}
	if len(rec.Payload) > 0 {
		if _, err := w.Write(rec.Payload); err != nil {
			return err
		}
	}
	return nil
}

func readRecord(r io.Reader) (Record, error) {
	var hdr [12]byte
	if _, err := io.ReadFull(r, hdr[:]); err != nil {
		return Record{}, err
	}
	rec := Record{Key: int64(binary.BigEndian.Uint64(hdr[0:8]))}
	if n := binary.BigEndian.Uint32(hdr[8:12]); n > 0 {
		rec.Payload = make([]byte, n)
		if _, err := io.ReadFull(r, rec.Payload); err != nil {
			return Record{}, err
		}
	}
	return rec, nil
}
