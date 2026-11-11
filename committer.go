package netsec

import (
	"context"
	"errors"
	"sync"
)

// Config configures a Committer.
type Config struct {
	// MaxPending bounds the number of operations that may hold a durable slot
	// without a terminal result at the same time. Acquiring a slot is the first
	// thing Commit does; when every slot is taken Commit blocks until one frees
	// or the context expires, giving the audit receiver explicit backpressure.
	MaxPending int
}

// inflightEntry deduplicates concurrent calls carrying the same OpID. The
// first caller runs the protocol; the others wait on done and share its result.
type inflightEntry struct {
	done   chan struct{}
	result Result
	err    error
}

// Committer serializes the audit-first protocol over a bounded Sink. It is
// safe for concurrent use.
type Committer struct {
	sink Sink

	mu      sync.Mutex
	slots   chan struct{}
	nextSeq uint64

	// known holds the newest durable record of every OpID seen in this process,
	// including records replayed at startup.
	known map[string]Record

	// pending tracks OpIDs whose latest durable record is StatusPending. The
	// set is seeded from replay and is never auto-executed.
	pending map[string]struct{}

	inflight map[string]*inflightEntry

	closed bool
}

// New opens a committer over sink. The sink is replayed once to recover
// durable state; recovered pending operations are exposed through Pending and
// are never re-executed automatically.
func New(ctx context.Context, sink Sink, cfg Config) (*Committer, error) {
	if cfg.MaxPending <= 0 {
		return nil, errors.New("MaxPending must be positive")
	}
	records, err := sink.Replay(ctx)
	if err != nil {
		return nil, err
	}

	c := &Committer{
		sink:     sink,
		slots:    make(chan struct{}, cfg.MaxPending),
		nextSeq:  1,
		known:    make(map[string]Record, len(records)),
		pending:  make(map[string]struct{}),
		inflight: make(map[string]*inflightEntry),
	}
	for _, rec := range records {
		c.known[rec.OpID] = rec
		if rec.Seq >= c.nextSeq {
			c.nextSeq = rec.Seq + 1
		}
	}
	for opID, rec := range c.known {
		if rec.Status == StatusPending {
			c.pending[opID] = struct{}{}
		}
	}
	return c, nil
}

// Commit runs the audit-first protocol for op:

//  1. Acquire a bounded slot (backpressure; no side effect may run yet).
//  2. Durably append the Pending reservation. Only after that append is
//     acknowledged may effect run.
//  3. Invoke effect at most once for op.OpID.
//  4. Durably append the terminal result under the same OpID and release the
//     slot.
//
// A failure of effect is not a lost event: a StatusFailed record carrying the
// error message is appended and the error is also returned. Duplicate OpIDs
// are idempotent: concurrent duplicates share one execution, a terminal OpID
// returns its stored result, and a recovered-pending OpID returns ErrPending
// instead of running again.
func (c *Committer) Commit(ctx context.Context, op Operation, effect SideEffect) (Result, error) {
	if op.OpID == "" {
		return Result{}, ErrEmptyOpID
	}

	entry, leader, replay, err := c.joinOrLead(op.OpID)
	if err != nil {
		return Result{}, err
	}
	if replay != nil {
		return *replay, replay.Failure
	}
	if !leader {
		select {
		case <-entry.done:
		case <-ctx.Done():
			return Result{}, ctx.Err()
		}
		return entry.result, entry.err
	}

	result, runErr := c.runProtocol(ctx, op, effect)
	c.finish(entry, result, runErr)
	return result, runErr
}

// joinOrLead registers interest in opID. The leader runs the protocol; any
// duplicate caller blocks on the returned entry. A non-nil Result means the
// OpID already has a durable terminal outcome and is replayed idempotently.
func (c *Committer) joinOrLead(opID string) (*inflightEntry, bool, *Result, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.closed {
		return nil, false, nil, ErrClosed
	}
	if entry, ok := c.inflight[opID]; ok {
		return entry, false, nil, nil
	}
	if rec, ok := c.known[opID]; ok {
		if rec.Status == StatusPending {
			return nil, false, nil, ErrPending
		}
		replay := resultFromRecord(rec)
		replay.Replayed = true
		return nil, false, &replay, nil
	}

	entry := &inflightEntry{done: make(chan struct{})}
	c.inflight[opID] = entry
	return entry, true, nil, nil
}

func (c *Committer) finish(entry *inflightEntry, result Result, err error) {
	c.mu.Lock()
	entry.result = result
	entry.err = err
	close(entry.done)
	for opID, candidate := range c.inflight {
		if candidate == entry {
			delete(c.inflight, opID)
		}
	}
	c.mu.Unlock()
}

func (c *Committer) runProtocol(ctx context.Context, op Operation, effect SideEffect) (Result, error) {
	// Step 1: bounded slot. Backpressure happens here, before any durable
	// write and therefore before any side effect.
	select {
	case c.slots <- struct{}{}:
	case <-ctx.Done():
		return Result{}, ctx.Err()
	}
	defer func() { <-c.slots }()

	// Step 2: durable reservation. nextSeq is assigned under the lock so two
	// operations can never reuse a sequence number.
	reservation := Record{
		Seq:    c.takeSeq(),
		OpID:   op.OpID,
		Actor:  op.Actor,
		Action: op.Action,
		Detail: op.Detail,
		Status: StatusPending,
	}
	if err := c.appendDurable(ctx, reservation); err != nil {
		// No acknowledgement means no slot was granted: effect never runs.
		return Result{}, err
	}
	c.markKnown(reservation, true)

	// Step 3: the side effect is reachable only after durable acknowledgement.
	effectErr := effect(ctx)

	// Step 4: terminal record under the same OpID. A failed effect is still a
	// completed audit outcome, not a missing event.
	terminal := Record{
		Seq:    c.takeSeq(),
		OpID:   op.OpID,
		Actor:  op.Actor,
		Action: op.Action,
		Detail: op.Detail,
		Status: StatusSucceeded,
	}
	var failure error
	if effectErr != nil {
		terminal.Status = StatusFailed
		terminal.Failure = effectErr.Error()
		failure = effectErr
	}

	recordErr := c.appendDurable(ctx, terminal)
	if recordErr != nil {
		// The effect already happened once. Keep the in-memory outcome so this
		// process does not run it again; on restart the reservation is still
		// pending and the host must call Resolve instead of re-executing.
		c.markKnown(terminal, false)
		return Result{OpID: op.OpID, Seq: terminal.Seq, Status: terminal.Status, Failure: failure},
			errors.Join(recordErr, failure)
	}
	c.markKnown(terminal, false)

	return Result{
		OpID:    op.OpID,
		Seq:     terminal.Seq,
		Status:  terminal.Status,
		Failure: failure,
	}, failure
}

// appendDurable appends one record, compacting the bounded log once on
// ErrSinkFull and retrying so ordinary churn does not surface as backpressure.
// A still-full second attempt is returned as ErrSinkFull.
func (c *Committer) appendDurable(ctx context.Context, rec Record) error {
	if err := c.sink.Append(ctx, rec); err != nil {
		if !errors.Is(err, ErrSinkFull) {
			return err
		}
		if compactErr := c.sink.Compact(ctx); compactErr != nil {
			return errors.Join(err, compactErr)
		}
		if retryErr := c.sink.Append(ctx, rec); retryErr != nil {
			return retryErr
		}
	}
	return nil
}

func (c *Committer) takeSeq() uint64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	seq := c.nextSeq
	c.nextSeq++
	return seq
}

// resultFromRecord rebuilds the host-facing result of a terminal record. A
// failed record carries its persisted error message as a plain error value;
// identity of the original error cannot survive serialization by design.
func resultFromRecord(rec Record) Result {
	res := Result{OpID: rec.OpID, Seq: rec.Seq, Status: rec.Status}
	if rec.Status == StatusFailed && rec.Failure != "" {
		res.Failure = errors.New(rec.Failure)
	}
	return res
}

// markKnown updates the in-memory index. isPending distinguishes a fresh
// reservation from a terminal outcome; a failed terminal append still records
// the outcome locally so the effect is never invoked twice in this process.
func (c *Committer) markKnown(rec Record, isPending bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.known[rec.OpID] = rec
	if isPending || rec.Status == StatusPending {
		c.pending[rec.OpID] = struct{}{}
	} else {
		delete(c.pending, rec.OpID)
	}
}

// Resolve records the outcome of an operation that is pending because of a
// crash or an undelivered terminal record. It never invokes a side effect:
// the host supplies the known outcome. Passing a nil failure marks success.
func (c *Committer) Resolve(ctx context.Context, opID string, failure error) (Result, error) {
	if opID == "" {
		return Result{}, ErrEmptyOpID
	}

	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return Result{}, ErrClosed
	}
	rec, ok := c.known[opID]
	if !ok || rec.Status != StatusPending {
		c.mu.Unlock()
		return Result{}, ErrPending
	}
	c.mu.Unlock()

	terminal := Record{
		Seq:    c.takeSeq(),
		OpID:   rec.OpID,
		Actor:  rec.Actor,
		Action: rec.Action,
		Detail: rec.Detail,
		Status: StatusSucceeded,
	}
	if failure != nil {
		terminal.Status = StatusFailed
		terminal.Failure = failure.Error()
	}
	if err := c.appendDurable(ctx, terminal); err != nil {
		return Result{}, err
	}
	c.markKnown(terminal, false)
	return Result{
		OpID:    terminal.OpID,
		Seq:     terminal.Seq,
		Status:  terminal.Status,
		Failure: failure,
	}, nil
}

// Pending returns the OpIDs whose latest durable record is a reservation
// without a terminal outcome, including operations recovered after a crash.
func (c *Committer) Pending() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]string, 0, len(c.pending))
	for opID := range c.pending {
		out = append(out, opID)
	}
	return out
}

// Status returns the newest durable state known for opID.
func (c *Committer) Status(opID string) (Record, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	rec, ok := c.known[opID]
	return rec, ok
}

// Close releases the underlying sink.
func (c *Committer) Close() error {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return ErrClosed
	}
	c.closed = true
	c.mu.Unlock()
	return c.sink.Close()
}
