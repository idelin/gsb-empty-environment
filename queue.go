// Package diskpq is a disk-first, crash-recoverable priority queue.
//
// Durability model (see doc.go for the exact crash / power-loss boundary):
//
//   - Every mutating operation is appended as one checksummed record to a
//     write-ahead log (WAL) and fsync'ed before the in-memory state changes
//     and before the call returns.
//   - A snapshot file plus WAL compaction bounds log growth; the snapshot is
//     published atomically via temp-file + rename + directory fsync.
//   - Recovery replays the snapshot and the intact WAL prefix. A torn tail
//     (CRC mismatch or partial line) is discarded, never applied.
//   - PopBatch commits one pop record for the whole batch; if the commit fails,
//     nothing is removed, so a failed pop transaction cannot lose tasks.
//   - Update records never create tasks: an Update for a deleted / unknown id
//     is ignored by both the API and WAL replay, so an old update record can
//     not resurrect a deleted task.
//
// Ordering: smaller Priority pops first; ties break by strictly monotonic
// enqueue Seq, so equal-priority tasks pop FIFO and the order is identical
// after restart.
package diskpq

import (
	"container/heap"
	"errors"
	"os"
	"path/filepath"
	"sync"
)

// ErrClosed is returned after the queue has been closed.
var ErrClosed = errors.New("diskpq: queue is closed")

// ErrDuplicateID is returned when an id already live in the queue is enqueued.
var ErrDuplicateID = errors.New("diskpq: duplicate task id")

const defaultCompactThreshold int64 = 1 << 20 // 1 MiB

// Item is one queued task. Seq is assigned on enqueue and never reused.
type Item struct {
	ID       string
	Priority int64 // smaller pops first; negative is allowed
	Seq      uint64

	index int
}

// Hooks are fault-injection points. A hook returning an error aborts the
// surrounding operation at that point and is useful to simulate disk faults
// and crash moments in tests.
type Hooks struct {
	// BeforeWALAppend runs before the record is written to the WAL.
	BeforeWALAppend func(op string) error
	// BeforeWALSync runs after the bytes are written but before fsync.
	// Simulates "process dies / power lost before durability".
	BeforeWALSync func(op string) error
	// AfterWALSync runs right after a successful fsync, before the record is
	// applied to memory. Simulates "process dies the instant after durability".
	AfterWALSync func(op string) error
	// BeforeSnapshotRename runs after the temp snapshot is fully written and
	// synced, just before the atomic rename.
	BeforeSnapshotRename func() error
}

// Queue is safe for concurrent use. One Queue owns one directory.
type Queue struct {
	dir string

	mu       sync.Mutex
	wal      *os.File
	walSize  int64
	items    itemHeap
	live     map[string]*Item
	lastSeq  uint64
	closed   bool
	hooks    Hooks
	compactN int64
}

// Option configures a Queue.
type Option func(*Queue)

// WithHooks installs fault-injection hooks.
func WithHooks(h Hooks) Option {
	return func(q *Queue) { q.hooks = h }
}

// WithCompactThreshold sets the WAL size that triggers an automatic
// snapshot+compaction. n <= 0 disables automatic compaction.
func WithCompactThreshold(n int64) Option {
	return func(q *Queue) { q.compactN = n }
}

// Open opens (or creates) a queue backed by files in dir. All queue files live
// exclusively in the caller-supplied directory.
func Open(dir string, opts ...Option) (*Queue, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	q := &Queue{
		dir:      dir,
		live:     make(map[string]*Item),
		compactN: defaultCompactThreshold,
	}
	for _, opt := range opts {
		opt(q)
	}
	if err := q.loadSnapshot(); err != nil {
		return nil, err
	}
	if err := q.openWALAndReplay(); err != nil {
		return nil, err
	}
	q.items = make(itemHeap, 0, len(q.live))
	for _, it := range q.live {
		q.items = append(q.items, it)
	}
	heap.Init(&q.items)
	return q, nil
}

// Enqueue adds a task with the given id and priority. Negative priorities are
// valid. Durable when err == nil.
func (q *Queue) Enqueue(id string, priority int64) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.closed {
		return ErrClosed
	}
	if id == "" {
		return errors.New("diskpq: task id must not be empty")
	}
	if _, ok := q.live[id]; ok {
		return ErrDuplicateID
	}
	seq := q.lastSeq + 1
	rec := walRecord{Op: opEnqueue, ID: id, Priority: priority, Seq: seq}
	if err := q.appendWAL(rec); err != nil {
		return err
	}
	q.lastSeq = seq
	it := &Item{ID: id, Priority: priority, Seq: seq}
	q.live[id] = it
	heap.Push(&q.items, it)
	return q.maybeCompactLocked()
}

// Update changes the priority of an existing task and reports whether the id
// was live. An Update for a deleted or never-seen id returns (false, nil) and
// writes nothing: stale updates can never resurrect a deleted task.
func (q *Queue) Update(id string, priority int64) (bool, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.closed {
		return false, ErrClosed
	}
	it, ok := q.live[id]
	if !ok {
		return false, nil
	}
	rec := walRecord{Op: opUpdate, ID: id, Priority: priority}
	if err := q.appendWAL(rec); err != nil {
		return false, err
	}
	it.Priority = priority
	heap.Fix(&q.items, it.index)
	return true, nil
}

// Delete removes a task and reports whether the id was live.
func (q *Queue) Delete(id string) (bool, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.closed {
		return false, ErrClosed
	}
	it, ok := q.live[id]
	if !ok {
		return false, nil
	}
	rec := walRecord{Op: opDelete, ID: id}
	if err := q.appendWAL(rec); err != nil {
		return false, err
	}
	heap.Remove(&q.items, it.index)
	delete(q.live, id)
	if err := q.maybeCompactLocked(); err != nil {
		return true, err
	}
	return true, nil
}

// PopBatch atomically removes and returns up to n highest-priority items
// (Priority asc, then FIFO Seq). n is capped at Len. An empty queue or n <= 0
// returns (nil, nil).
//
// Atomicity: the full batch is removed only after one pop record is durably
// committed. If the commit (or any injected fault) fails, all candidate items
// are restored to the heap and still in the queue — a failed pop transaction
// loses nothing. On success the batch is exactly what was committed, so it can
// never reappear after restart.
func (q *Queue) PopBatch(n int) ([]Item, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.closed {
		return nil, ErrClosed
	}
	if n <= 0 || q.items.Len() == 0 {
		return nil, nil
	}
	if n > q.items.Len() {
		n = q.items.Len()
	}

	chosen := make([]*Item, 0, n)
	for i := 0; i < n; i++ {
		chosen = append(chosen, heap.Pop(&q.items).(*Item))
	}

	ids := make([]string, n)
	for i, it := range chosen {
		ids[i] = it.ID
	}
	rec := walRecord{Op: opPop, IDs: ids}
	if err := q.appendWAL(rec); err != nil {
		// Commit failed: the transaction never happened. Restore candidates
		// (they were never removed from q.live) and preserve no order info:
		// the heap rebuilds ordering on push.
		for _, it := range chosen {
			heap.Push(&q.items, it)
		}
		return nil, err
	}

	out := make([]Item, n)
	for i, it := range chosen {
		delete(q.live, it.ID)
		out[i] = *it
	}
	if err := q.maybeCompactLocked(); err != nil {
		return out, err
	}
	return out, nil
}

// Len reports the number of live tasks.
func (q *Queue) Len() int {
	q.mu.Lock()
	defer q.mu.Unlock()
	return len(q.live)
}

// Close fsyncs and closes the files. It does not delete anything.
func (q *Queue) Close() error {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.closed {
		return nil
	}
	q.closed = true
	if q.wal != nil {
		if err := q.wal.Sync(); err != nil {
			q.wal.Close()
			return err
		}
		return q.wal.Close()
	}
	return nil
}

func (q *Queue) walPath() string { return filepath.Join(q.dir, walFileName) }
func (q *Queue) snapPath() string {
	return filepath.Join(q.dir, snapshotFileName)
}
func (q *Queue) snapTmpPath() string {
	return filepath.Join(q.dir, snapshotTmpName)
}
