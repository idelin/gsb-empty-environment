package netsec

import (
	"container/heap"
	"context"
	"errors"
	"sync"
)

// FailureClass is produced by the explicit Classifier for a failed attempt.
// Only ClassInfrastructure is eligible for retry; every other class ends the
// sample immediately so that a real crash is never masked by retries.
type FailureClass int

const (
	// ClassUnspecified is the zero value. It is terminal: the host failed to
	// classify the error, so the queue must not guess that a retry is safe.
	ClassUnspecified FailureClass = iota
	// ClassInfrastructure denotes an environmental failure (flaky network,
	// unavailable fixture, ...) that may be retried within the attempt budget.
	ClassInfrastructure
	// ClassCrash denotes a genuine crash of the security sample. It is always
	// terminal and the crashing attempt is retained in the sample result.
	ClassCrash
)

// Status is the terminal status of a sample.
type Status int

const (
	StatusUnknown Status = iota
	// StatusSucceeded means an attempt completed without error.
	StatusSucceeded
	// StatusCrashed means an attempt was classified as ClassCrash.
	StatusCrashed
	// StatusInfrastructureExhausted means every allowed attempt was spent on
	// infrastructure failures without ever reaching a definitive outcome.
	StatusInfrastructureExhausted
	// StatusFailed means a failure was classified with a terminal class other
	// than ClassCrash (for example the zero ClassUnspecified value).
	StatusFailed
)

// Classifier is the explicit extension point the test host implements to tell
// the queue why an attempt failed. It is invoked at most once per completed
// attempt and never for a successful attempt.
type Classifier interface {
	Classify(attempt Attempt, cause error) FailureClass
}

// ClassifierFunc adapts a plain function to the Classifier interface.
type ClassifierFunc func(attempt Attempt, cause error) FailureClass

// Classify implements Classifier.
func (f ClassifierFunc) Classify(attempt Attempt, cause error) FailureClass {
	return f(attempt, cause)
}

// Sample is a unit of security-test work submitted to the queue.
type Sample struct {
	// ID uniquely identifies a sample for the lifetime of the queue.
	ID string
	// Priority orders waiting samples: a larger value is served first.
	Priority int
	// Payload is the opaque test payload handed back to the claiming worker.
	Payload any
}

// Attempt identifies one execution of a sample. Number starts at 1 and grows
// monotonically per sample; a given (SampleID, Number) pair is leased at most
// once for the lifetime of the queue.
type Attempt struct {
	SampleID string
	Number   int
}

// Lease grants a worker the right to execute exactly one attempt. It must be
// handed back to Queue.Complete exactly once; a second completion is rejected.
type Lease struct {
	Attempt Attempt
	Sample  Sample
}

// AttemptRecord keeps the outcome of one attempt. Every attempt, including
// retried infrastructure failures, is retained on the final sample result.
type AttemptRecord struct {
	Attempt Attempt
	OK      bool
	Err     error
}

// Result is the immutable terminal outcome of a sample and carries the full
// history of attempts in execution order.
type Result struct {
	Sample   Sample
	Status   Status
	Attempts []AttemptRecord
}

var (
	// ErrDuplicateSample is returned by Enqueue when the sample ID is already
	// known to the queue, whether it is waiting, in flight or finished.
	ErrDuplicateSample = errors.New("netsec: duplicate sample id")
	// ErrInvalidSample is returned by Enqueue for a sample without an ID.
	ErrInvalidSample = errors.New("netsec: sample id is empty")
	// ErrLeaseNotHeld is returned when Complete receives a lease that was never
	// issued, has already been completed, or belongs to a superseded attempt.
	ErrLeaseNotHeld = errors.New("netsec: lease is not currently held")
	// ErrQueueClosed is returned by Claim/Enqueue after Close and by Wait when
	// the queue is closed with no buffered result left.
	ErrQueueClosed = errors.New("netsec: queue closed")
)

// Queue is a concurrency-safe result queue for a Go test host.
//
// Waiting samples are served by descending Priority. Samples of equal priority
// are served in enqueue order. When an infrastructure failure is retried the
// sample re-enters the tail of its priority level with a fresh enqueue tick,
// so newly admitted samples are always served before an older sample's retry
// and retries cannot starve fresh work.
type Queue struct {
	mu         sync.Mutex
	cond       *sync.Cond
	classifier Classifier
	// maxAttempts is the total attempt budget per sample (the initial attempt
	// plus at most maxAttempts-1 infrastructure retries).
	maxAttempts int

	ready readyHeap

	states      map[string]*sampleState
	results     []*Result
	resultByID  map[string]*Result
	nextEnqueue int
	closed      bool
}

type sampleState struct {
	sample Sample
	// attempt is the number of the latest claimed attempt (0 before the first
	// claim) and completed is the number of the latest completed attempt.
	attempt   int
	completed int
	history   []AttemptRecord
}

type entry struct {
	priority int
	// seq is the enqueue tick; re-enqueued retries receive a fresh one so that
	// they line up behind every sample that was admitted earlier.
	seq      int
	sampleID string
}

type readyHeap []entry

func (h readyHeap) Len() int { return len(h) }

func (h readyHeap) Less(i, j int) bool {
	if h[i].priority != h[j].priority {
		return h[i].priority > h[j].priority
	}
	return h[i].seq < h[j].seq
}

func (h readyHeap) Swap(i, j int) { h[i], h[j] = h[j], h[i] }

func (h *readyHeap) Push(x any) { *h = append(*h, x.(entry)) }

func (h *readyHeap) Pop() any {
	old := *h
	last := len(old) - 1
	item := old[last]
	*h = old[:last]
	return item
}

// NewQueue creates a queue that allows up to maxAttempts total executions per
// sample (one initial attempt plus at most maxAttempts-1 retries). It panics
// on a nil classifier or a non-positive attempt budget.
func NewQueue(classifier Classifier, maxAttempts int) *Queue {
	if classifier == nil {
		panic("netsec: nil classifier")
	}
	if maxAttempts < 1 {
		panic("netsec: maxAttempts must be at least 1")
	}
	q := &Queue{
		classifier:  classifier,
		maxAttempts: maxAttempts,
		states:      make(map[string]*sampleState),
		resultByID:  make(map[string]*Result),
	}
	q.cond = sync.NewCond(&q.mu)
	return q
}

// Enqueue admits a new sample at the tail of its priority level. It fails on
// empty or duplicate sample IDs.
func (q *Queue) Enqueue(sample Sample) error {
	if sample.ID == "" {
		return ErrInvalidSample
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.closed {
		return ErrQueueClosed
	}
	if _, exists := q.states[sample.ID]; exists {
		return ErrDuplicateSample
	}
	if _, finished := q.resultByID[sample.ID]; finished {
		return ErrDuplicateSample
	}
	q.states[sample.ID] = &sampleState{sample: sample}
	q.nextEnqueue++
	heap.Push(&q.ready, entry{
		priority: sample.Priority,
		seq:      q.nextEnqueue,
		sampleID: sample.ID,
	})
	q.cond.Signal()
	return nil
}

// Claim blocks until a sample is available, the context is canceled or the
// queue is closed. Every leased attempt is unique: concurrent Claim calls never
// receive the same (SampleID, AttemptNumber) pair.
func (q *Queue) Claim(ctx context.Context) (*Lease, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	for q.ready.Len() == 0 {
		if q.closed {
			return nil, ErrQueueClosed
		}
		if err := q.waitLocked(ctx); err != nil {
			return nil, err
		}
	}
	item := heap.Pop(&q.ready).(entry)
	state := q.states[item.sampleID]
	state.attempt++
	return &Lease{
		Attempt: Attempt{SampleID: state.sample.ID, Number: state.attempt},
		Sample:  state.sample,
	}, nil
}

// Complete reports the outcome of a leased attempt.
//
// On success the sample finishes with StatusSucceeded. On failure the explicit
// Classifier is consulted: ClassInfrastructure results in a re-enqueued retry
// while attempts remain, while every other class is terminal. All attempts are
// preserved on the eventual Result, including the retried ones.
func (q *Queue) Complete(lease *Lease, ok bool, cause error) error {
	if lease == nil {
		return ErrLeaseNotHeld
	}
	q.mu.Lock()
	state, exists := q.states[lease.Attempt.SampleID]
	if q.closed {
		q.mu.Unlock()
		return ErrQueueClosed
	}
	if !exists || state.attempt != lease.Attempt.Number || lease.Attempt.Number <= state.completed {
		q.mu.Unlock()
		return ErrLeaseNotHeld
	}
	sample := state.sample
	q.mu.Unlock()

	// Classification happens outside the lock so a slow host classifier cannot
	// block enqueue/claim operations.
	class := ClassUnspecified
	if !ok {
		class = q.classifier.Classify(lease.Attempt, cause)
	}

	q.mu.Lock()
	defer q.mu.Unlock()
	// Re-check under the lock: only the current outstanding attempt may be
	// completed, and only once.
	state, exists = q.states[lease.Attempt.SampleID]
	if !exists || state.attempt != lease.Attempt.Number || lease.Attempt.Number <= state.completed {
		return ErrLeaseNotHeld
	}
	state.completed = lease.Attempt.Number
	state.history = append(state.history, AttemptRecord{
		Attempt: lease.Attempt,
		OK:      ok,
		Err:     cause,
	})

	switch {
	case ok:
		q.publishLocked(state, StatusSucceeded)
		return nil
	case class == ClassInfrastructure && len(state.history) < q.maxAttempts:
		// Re-enter at the tail of the priority level with a fresh tick so that
		// any sample admitted in the meantime is served first.
		q.nextEnqueue++
		heap.Push(&q.ready, entry{
			priority: sample.Priority,
			seq:      q.nextEnqueue,
			sampleID: sample.ID,
		})
		q.cond.Signal()
		return nil
	case class == ClassCrash:
		q.publishLocked(state, StatusCrashed)
		return nil
	case class == ClassInfrastructure:
		q.publishLocked(state, StatusInfrastructureExhausted)
		return nil
	default:
		q.publishLocked(state, StatusFailed)
		return nil
	}
}

func (q *Queue) publishLocked(state *sampleState, status Status) {
	result := &Result{
		Sample:   state.sample,
		Status:   status,
		Attempts: append([]AttemptRecord(nil), state.history...),
	}
	q.results = append(q.results, result)
	q.resultByID[state.sample.ID] = result
	delete(q.states, state.sample.ID)
	q.cond.Broadcast()
}

// Wait blocks until at least one terminal result is available, the context is
// canceled, or the queue closes with the result buffer drained.
func (q *Queue) Wait(ctx context.Context) (int, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	for len(q.results) == 0 && !q.closed {
		if err := q.waitLocked(ctx); err != nil {
			return 0, err
		}
	}
	if len(q.results) == 0 {
		return 0, ErrQueueClosed
	}
	return len(q.results), nil
}

// WaitResult blocks until the named sample reaches a terminal state.
func (q *Queue) WaitResult(ctx context.Context, sampleID string) (*Result, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	for q.resultByID[sampleID] == nil {
		if q.closed {
			return nil, ErrQueueClosed
		}
		if err := q.waitLocked(ctx); err != nil {
			return nil, err
		}
	}
	return q.resultByID[sampleID], nil
}

// Result returns the terminal result of a sample, or nil if it has not finished.
func (q *Queue) Result(sampleID string) *Result {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.resultByID[sampleID]
}

// Results returns a snapshot of all terminal results in completion order.
func (q *Queue) Results() []*Result {
	q.mu.Lock()
	defer q.mu.Unlock()
	out := make([]*Result, len(q.results))
	copy(out, q.results)
	return out
}

// Len reports the number of samples currently waiting to be claimed. Samples
// that are in flight or already finished are not counted.
func (q *Queue) Len() int {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.ready.Len()
}

// Close marks the queue as closed and wakes all blocked callers. Already
// enqueued samples can still be claimed and completed; calls that would block
// once the queue is drained return ErrQueueClosed.
func (q *Queue) Close() {
	q.mu.Lock()
	defer q.mu.Unlock()
	if !q.closed {
		q.closed = true
		q.cond.Broadcast()
	}
}

// waitLocked suspends the caller on the condition variable until a signal or
// ctx cancellation. The caller must hold q.mu.
func (q *Queue) waitLocked(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	stop := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			q.mu.Lock()
			q.cond.Broadcast()
			q.mu.Unlock()
		case <-stop:
		}
	}()
	q.cond.Wait()
	close(stop)
	return ctx.Err()
}
