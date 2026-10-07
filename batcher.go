// Package batcher implements a batching request client that aligns
// responses by request ID, tolerates out-of-order, missing and duplicate
// responses, retries only missing items, and arbitrates final state by
// the server's monotonic commit version.
package batcher

import (
	"context"
	"errors"
)

// Request is a single unit of work. ID must be unique within an Engine
// and is preserved across retries.
type Request struct {
	ID   string
	Data string
}

// Response is the server's answer for one request, aligned by ID.
// Version is the server's monotonic commit version; higher wins.
// Err marks a per-item failure; failures never displace a confirmation.
type Response struct {
	ID      string
	Version uint64
	Value   string
	Err     error
}

// Transport delivers one batch and returns per-item responses. A
// batch-level failure is reported as a non-nil error, in which case
// responses are discarded and confirmed items are left untouched.
type Transport interface {
	Send(ctx context.Context, reqs []Request) ([]Response, error)
}

// Status is the terminal disposition of an item.
type Status int

const (
	// StatusPending means the item was never confirmed (still missing,
	// attempts exhausted, or dispatch cancelled before it was sent).
	StatusPending Status = iota
	// StatusConfirmed means the item has a committed version.
	StatusConfirmed
	// StatusConflict means two committed results arrived with the same
	// version but different values; the item is isolated from retries.
	StatusConflict
)

// Conflict records a same-version/different-value collision.
type Conflict struct {
	ID       string
	Version  uint64
	Existing string
	Incoming string
}

// Result is the final per-item view, emitted in original input order.
type Result struct {
	Request Request
	Status  Status
	Version uint64
	Value   string
	LastErr error
}

type itemState struct {
	req       Request
	confirmed bool
	version   uint64
	value     string
	conflict  bool
	lastErr   error
}

// Engine drives a fixed set of requests through a Transport in batches.
type Engine struct {
	transport   Transport
	batchSize   int
	maxAttempts int

	order     []string
	items     map[string]*itemState
	conflicts []Conflict
}

// New builds an Engine for reqs. Duplicate IDs are ignored after the
// first occurrence. batchSize <= 0 means "all in one batch";
// maxAttempts <= 0 means a single attempt.
func New(t Transport, batchSize, maxAttempts int, reqs []Request) *Engine {
	if batchSize <= 0 {
		batchSize = len(reqs)
		if batchSize == 0 {
			batchSize = 1
		}
	}
	if maxAttempts <= 0 {
		maxAttempts = 1
	}
	e := &Engine{
		transport:   t,
		batchSize:   batchSize,
		maxAttempts: maxAttempts,
		items:       make(map[string]*itemState, len(reqs)),
	}
	for _, r := range reqs {
		if _, dup := e.items[r.ID]; dup {
			continue
		}
		e.order = append(e.order, r.ID)
		e.items[r.ID] = &itemState{req: r}
	}
	return e
}

// Run dispatches batches until every item is confirmed or conflicted,
// attempts are exhausted, or ctx is cancelled. Cancellation only stops
// undispatched items; a batch already in flight runs to completion.
func (e *Engine) Run(ctx context.Context) error {
	for attempt := 0; attempt < e.maxAttempts; attempt++ {
		pending := e.pending()
		if len(pending) == 0 {
			return nil
		}
		for start := 0; start < len(pending); start += e.batchSize {
			if err := ctx.Err(); err != nil {
				return err
			}
			end := start + e.batchSize
			if end > len(pending) {
				end = len(pending)
			}
			batch := pending[start:end]
			resps, err := e.transport.Send(ctx, batch)
			if err != nil {
				// Batch-level failure: record it but never let it
				// overwrite already confirmed items.
				for _, r := range batch {
					e.items[r.ID].lastErr = err
				}
				continue
			}
			e.apply(resps)
		}
	}
	return nil
}

// pending returns unconfirmed, non-conflicted items in input order.
// These are exactly the items retried, with their original IDs.
func (e *Engine) pending() []Request {
	var out []Request
	for _, id := range e.order {
		it := e.items[id]
		if !it.confirmed && !it.conflict {
			out = append(out, it.req)
		}
	}
	return out
}

// apply folds responses into item state, aligning by request ID.
// Responses may arrive out of order, duplicated, or refer to items
// outside the batch that was just sent (e.g. a late answer from an
// earlier attempt whose item is in flight again).
func (e *Engine) apply(resps []Response) {
	for _, r := range resps {
		it, ok := e.items[r.ID]
		if !ok || it.conflict {
			continue
		}
		if r.Err != nil {
			// Per-item failure: never overwrites a confirmation,
			// regardless of version. An old failure for an item
			// since confirmed is simply stale.
			it.lastErr = r.Err
			continue
		}
		if !it.confirmed {
			it.confirmed = true
			it.version, it.value = r.Version, r.Value
			it.lastErr = nil
			continue
		}
		switch {
		case r.Version < it.version:
			// Stale commit: ignore.
		case r.Version == it.version:
			if r.Value != it.value {
				it.conflict = true
				e.conflicts = append(e.conflicts, Conflict{
					ID:       r.ID,
					Version:  r.Version,
					Existing: it.value,
					Incoming: r.Value,
				})
			}
		default:
			it.version, it.value = r.Version, r.Value
		}
	}
}

// Results returns per-item outcomes in original input order.
func (e *Engine) Results() []Result {
	out := make([]Result, 0, len(e.order))
	for _, id := range e.order {
		it := e.items[id]
		st := StatusPending
		switch {
		case it.conflict:
			st = StatusConflict
		case it.confirmed:
			st = StatusConfirmed
		}
		out = append(out, Result{
			Request: it.req,
			Status:  st,
			Version: it.version,
			Value:   it.value,
			LastErr: it.lastErr,
		})
	}
	return out
}

// Conflicts returns every same-version/different-value collision seen.
func (e *Engine) Conflicts() []Conflict {
	return append([]Conflict(nil), e.conflicts...)
}

var errCheckpointInconsistent = errors.New("batcher: inconsistent checkpoint")
