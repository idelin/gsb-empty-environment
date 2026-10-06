// Package sortexec implements an external-sort query executor that spills
// sorted runs to disk, k-way merges them, and delivers ordered batches.
// Cancellation may strike during the produce, merge, or deliver phase:
// already-delivered batches stay valid and owned by the caller, while
// undelivered files and iterators are closed exactly once.
package sortexec

import (
	"bufio"
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

// ErrClosed is returned by Iterator.Next after the iterator was closed.
var ErrClosed = errors.New("sortexec: iterator closed")

// Record is a single sortable row. Keys order the stream; values ride along.
type Record struct {
	Key   string
	Value []byte
}

// Source yields the unsorted input stream, returning io.EOF when drained.
type Source interface {
	Next(ctx context.Context) (Record, error)
}

// SliceSource adapts an in-memory slice to Source.
type SliceSource []Record

// Next implements Source.
func (s *SliceSource) Next(context.Context) (Record, error) {
	if len(*s) == 0 {
		return Record{}, io.EOF
	}
	rec := (*s)[0]
	*s = (*s)[1:]
	return rec, nil
}

// Options tunes an Executor.
type Options struct {
	RunSize   int // records sorted in memory per spill run (default 1024)
	BatchSize int // records per delivered batch (default 128)
	Hooks     Hooks
}

// Hooks are observability/cancellation points invoked at phase boundaries.
// A nil hook is skipped; a hook error aborts the phase and is propagated.
type Hooks struct {
	AfterRun     func(context.Context) error // after each run is spilled (produce)
	AfterProduce func(context.Context) error // after all runs are spilled
	BeforeMerge  func(context.Context) error // before merge iterators open
	AfterMerge   func(context.Context) error // after the merge heap is built
	BeforeBatch  func(context.Context) error // before each delivered batch
}

func call(hook func(context.Context) error, ctx context.Context) error {
	if hook == nil {
		return nil
	}
	return hook(ctx)
}

var executorIDs atomic.Uint64

// Executor spills sorted runs to a FileSystem and merges them back.
type Executor struct {
	fs   FileSystem
	opts Options
	id   uint64
	seq  atomic.Uint64

	// Tracker counts live file handles and merge iterators.
	Tracker *Tracker
}

// New builds an Executor writing spill files to fs.
func New(fs FileSystem, opts Options) *Executor {
	if opts.RunSize <= 0 {
		opts.RunSize = 1024
	}
	if opts.BatchSize <= 0 {
		opts.BatchSize = 128
	}
	return &Executor{
		fs:      fs,
		opts:    opts,
		id:      executorIDs.Add(1),
		Tracker: &Tracker{},
	}
}

// Sort consumes src, spills sorted runs, opens merge iterators, and returns
// an Iterator for the deliver phase. On any error or cancellation every
// file and iterator opened so far is closed and removed exactly once.
func (e *Executor) Sort(ctx context.Context, src Source) (*Iterator, error) {
	var runs []string
	cleanup := func(err error) (*Iterator, error) {
		return nil, errors.Join(err, e.removeAll(runs))
	}

	// Produce phase: sort chunks in memory and spill them.
	for {
		if err := ctx.Err(); err != nil {
			return cleanup(err)
		}
		recs, err := e.readRun(ctx, src)
		if err != nil {
			return cleanup(err)
		}
		if len(recs) == 0 {
			break
		}
		name := fmt.Sprintf("run-%d-%d", e.id, e.seq.Add(1))
		runs = append(runs, name)
		if err := e.spillRun(name, recs); err != nil {
			return cleanup(err)
		}
		if err := call(e.opts.Hooks.AfterRun, ctx); err != nil {
			return cleanup(err)
		}
	}
	if err := call(e.opts.Hooks.AfterProduce, ctx); err != nil {
		return cleanup(err)
	}

	// Merge phase: open one iterator per run and prime the heap.
	if err := call(e.opts.Hooks.BeforeMerge, ctx); err != nil {
		return cleanup(err)
	}
	it := &Iterator{exec: e, runs: runs}
	fail := func(err error) (*Iterator, error) {
		it.Close()
		return nil, err
	}
	for _, name := range runs {
		if err := ctx.Err(); err != nil {
			return fail(err)
		}
		ri, err := e.openRun(name)
		if err != nil {
			return fail(err)
		}
		it.iters = append(it.iters, ri)
		if err := ri.advance(); err != nil {
			return fail(err)
		}
		if ri.ok {
			it.heap = append(it.heap, ri)
		}
	}
	heap.Init(&it.heap)
	if err := call(e.opts.Hooks.AfterMerge, ctx); err != nil {
		return fail(err)
	}
	return it, nil
}

func (e *Executor) readRun(ctx context.Context, src Source) ([]Record, error) {
	recs := make([]Record, 0, e.opts.RunSize)
	for len(recs) < e.opts.RunSize {
		rec, err := src.Next(ctx)
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		recs = append(recs, rec)
	}
	sort.SliceStable(recs, func(i, j int) bool { return recs[i].Key < recs[j].Key })
	return recs, nil
}

func (e *Executor) spillRun(name string, recs []Record) error {
	f, err := e.fs.Create(name)
	if err != nil {
		return err
	}
	e.Tracker.addFile(1)
	w := bufio.NewWriter(f)
	for _, rec := range recs {
		if err = writeRecord(w, rec); err != nil {
			break
		}
	}
	if err == nil {
		err = w.Flush()
	}
	cerr := f.Close()
	e.Tracker.addFile(-1)
	if err != nil {
		return err
	}
	return cerr
}

func (e *Executor) openRun(name string) (*runIter, error) {
	f, err := e.fs.Open(name)
	if err != nil {
		return nil, err
	}
	e.Tracker.addFile(1)
	e.Tracker.addIter(1)
	return &runIter{file: f, r: bufio.NewReader(f), tracker: e.Tracker}, nil
}

func (e *Executor) removeAll(names []string) error {
	var errs []error
	for _, name := range names {
		if err := e.fs.Remove(name); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// Iterator delivers merged records in batches during the deliver phase.
// Each returned batch is freshly allocated and stays valid after
// cancellation or Close. Iterator is safe for concurrent use, though Next
// is expected to be called from a single goroutine.
type Iterator struct {
	exec  *Executor
	runs  []string
	iters []*runIter
	heap  runHeap

	once   sync.Once
	closed atomic.Bool
	err    error
}

// Next returns the next batch of up to BatchSize records, or io.EOF when
// the stream is drained. Any non-EOF error (including cancellation) closes
// the iterator and releases all undelivered resources exactly once.
func (it *Iterator) Next(ctx context.Context) ([]Record, error) {
	if it.closed.Load() {
		return nil, ErrClosed
	}
	if err := call(it.exec.opts.Hooks.BeforeBatch, ctx); err != nil {
		it.Close()
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		it.Close()
		return nil, err
	}
	if it.heap.Len() == 0 {
		it.Close()
		return nil, io.EOF
	}
	batch := make([]Record, 0, it.exec.opts.BatchSize)
	for len(batch) < it.exec.opts.BatchSize && it.heap.Len() > 0 {
		top := it.heap[0]
		batch = append(batch, top.cur)
		if err := top.advance(); err != nil {
			it.Close()
			return nil, err
		}
		if top.ok {
			heap.Fix(&it.heap, 0)
		} else {
			heap.Pop(&it.heap)
		}
	}
	return batch, nil
}

// Close releases every undelivered run iterator and removes every spill
// file. It is idempotent: each underlying resource is closed exactly once
// and the same error is returned on every call.
func (it *Iterator) Close() error {
	it.once.Do(func() {
		it.closed.Store(true)
		var errs []error
		for _, ri := range it.iters {
			if err := ri.close(); err != nil {
				errs = append(errs, err)
			}
		}
		if err := it.exec.removeAll(it.runs); err != nil {
			errs = append(errs, err)
		}
		it.err = errors.Join(errs...)
	})
	return it.err
}

// runIter streams records from one spill file. close is idempotent.
type runIter struct {
	file    File
	r       *bufio.Reader
	tracker *Tracker

	cur Record
	ok  bool

	once sync.Once
	err  error
}

func (ri *runIter) advance() error {
	rec, err := readRecord(ri.r)
	if err == io.EOF {
		ri.ok = false
		return nil
	}
	if err != nil {
		return err
	}
	ri.cur = rec
	ri.ok = true
	return nil
}

func (ri *runIter) close() error {
	ri.once.Do(func() {
		ri.err = ri.file.Close()
		ri.tracker.addFile(-1)
		ri.tracker.addIter(-1)
	})
	return ri.err
}

// runHeap is a min-heap of run iterators ordered by their current key.
type runHeap []*runIter

func (h runHeap) Len() int            { return len(h) }
func (h runHeap) Less(i, j int) bool  { return h[i].cur.Key < h[j].cur.Key }
func (h runHeap) Swap(i, j int)       { h[i], h[j] = h[j], h[i] }
func (h *runHeap) Push(x interface{}) { *h = append(*h, x.(*runIter)) }
func (h *runHeap) Pop() interface{} {
	old := *h
	n := len(old)
	it := old[n-1]
	old[n-1] = nil
	*h = old[:n-1]
	return it
}

func writeRecord(w *bufio.Writer, rec Record) error {
	var buf [binary.MaxVarintLen64]byte
	n := binary.PutUvarint(buf[:], uint64(len(rec.Key)))
	if _, err := w.Write(buf[:n]); err != nil {
		return err
	}
	if _, err := w.WriteString(rec.Key); err != nil {
		return err
	}
	n = binary.PutUvarint(buf[:], uint64(len(rec.Value)))
	if _, err := w.Write(buf[:n]); err != nil {
		return err
	}
	_, err := w.Write(rec.Value)
	return err
}

func readRecord(r *bufio.Reader) (Record, error) {
	keyLen, err := binary.ReadUvarint(r)
	if err != nil {
		return Record{}, err
	}
	key := make([]byte, keyLen)
	if _, err := io.ReadFull(r, key); err != nil {
		return Record{}, err
	}
	valLen, err := binary.ReadUvarint(r)
	if err != nil {
		return Record{}, err
	}
	val := make([]byte, valLen)
	if _, err := io.ReadFull(r, val); err != nil {
		return Record{}, err
	}
	return Record{Key: string(key), Value: val}, nil
}
