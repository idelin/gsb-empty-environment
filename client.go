package batchclient

import (
	"context"
	"errors"
	"time"
)

// ErrAttemptTimeout marks a batch attempt that exceeded AttemptTimeout. The
// attempt is retried while the original call stays in flight; its late
// responses are still applied through version arbitration.
var ErrAttemptTimeout = errors.New("batchclient: attempt timed out")

// Transport submits one batch and returns per-item responses. Responses may
// arrive out of order, may omit items, may contain duplicates, and may be
// accompanied by a batch-level error. A batch-level error never discards the
// responses that did arrive.
type Transport interface {
	SubmitBatch(ctx context.Context, batch []Request) ([]Response, error)
}

// Client splits requests into batches, dispatches them through a Transport,
// and aligns every response to its request ID.
type Client struct {
	transport      Transport
	batchSize      int
	maxAttempts    int
	parallelism    int
	attemptTimeout time.Duration
}

// Option configures a Client.
type Option func(*Client)

// WithBatchSize sets the maximum number of items per dispatched batch.
func WithBatchSize(n int) Option {
	return func(c *Client) {
		if n > 0 {
			c.batchSize = n
		}
	}
}

// WithMaxAttempts sets how many times a single item may be dispatched
// before it is declared missing.
func WithMaxAttempts(n int) Option {
	return func(c *Client) {
		if n > 0 {
			c.maxAttempts = n
		}
	}
}

// WithParallelism sets the maximum number of batches in flight at once.
func WithParallelism(n int) Option {
	return func(c *Client) {
		if n > 0 {
			c.parallelism = n
		}
	}
}

// WithAttemptTimeout sets how long the client waits for one batch attempt
// before re-dispatching its items. The timed-out attempt is not cancelled:
// the same item may then be in flight across multiple attempts, and the
// late responses are arbitrated by commit version. Zero disables retries
// on timeout.
func WithAttemptTimeout(d time.Duration) Option {
	return func(c *Client) {
		if d > 0 {
			c.attemptTimeout = d
		}
	}
}

// New creates a Client over t.
func New(t Transport, opts ...Option) *Client {
	c := &Client{
		transport:   t,
		batchSize:   32,
		maxAttempts: 3,
		parallelism: 4,
	}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

// Result carries the per-request outcomes (aligned to input order) and a
// checkpoint that can resume an incomplete run.
type Result struct {
	Outcomes   []Outcome
	Checkpoint Checkpoint
}

// Complete reports whether every request reached a confirmed state.
func (r *Result) Complete() bool { return len(r.Checkpoint.Missing) == 0 }

// Submit runs all requests to a terminal state and returns outcomes aligned
// to the input order. An empty input produces no transport calls.
func (c *Client) Submit(ctx context.Context, reqs []Request) []Outcome {
	return c.Run(ctx, reqs).Outcomes
}

// Run is Submit with access to the resumable checkpoint.
func (c *Client) Run(ctx context.Context, reqs []Request) *Result {
	r := newRun(c, reqs)
	return r.exec(ctx)
}

// Resume continues from a checkpoint produced by an earlier run. Request
// identity, confirmed versions, and the missing set are preserved; only
// missing items are dispatched again, under their original IDs.
func (c *Client) Resume(ctx context.Context, cp Checkpoint) *Result {
	r := newRun(c, cp.Requests)
	for id, conf := range cp.Confirmed {
		if st, ok := r.states[id]; ok {
			st.confirmed = true
			st.version = conf.Version
			st.value = conf.Value
			st.errMsg = conf.Err
			st.conflict = conf.Conflict
		}
	}
	missing := make(map[string]bool, len(cp.Missing))
	for _, id := range cp.Missing {
		missing[id] = true
	}
	pending := make([]string, 0, len(cp.Missing))
	for _, id := range r.order {
		if missing[id] && !r.states[id].confirmed {
			pending = append(pending, id)
		}
	}
	r.pending = pending
	return r.exec(ctx)
}

// itemState is the arbitration state for one request ID.
type itemState struct {
	req       Request
	attempts  int
	confirmed bool
	version   uint64
	value     string
	errMsg    string
	conflict  bool
	missing   bool
	cancelled bool
}

// apply folds one response into the state. The highest commit version wins;
// a lower version is stale and ignored; an equal version with a different
// result isolates the item as a conflict.
func (st *itemState) apply(resp Response) {
	switch {
	case !st.confirmed || resp.Version > st.version:
		st.confirmed = true
		st.version = resp.Version
		st.value = resp.Value
		st.errMsg = resp.Err
		st.conflict = false
	case resp.Version == st.version:
		if resp.Value != st.value || resp.Err != st.errMsg {
			st.conflict = true
		}
	}
}

func (st *itemState) outcome() Outcome {
	o := Outcome{Request: st.req, Version: st.version, Value: st.value, Err: st.errMsg}
	switch {
	case st.conflict:
		o.Status = StatusConflict
	case st.confirmed && st.errMsg != "":
		o.Status = StatusFailed
	case st.confirmed:
		o.Status = StatusOK
	case st.missing:
		o.Status = StatusMissing
	default:
		o.Status = StatusCancelled
	}
	return o
}

// batchResult is one attempt's delivery back to the run loop.
type batchResult struct {
	batch    []Request
	resps    []Response
	err      error
	timedOut bool // attempt timed out; items were requeued, call still in flight
	late     bool // actual delivery of a previously timed-out attempt
}

type run struct {
	client  *Client
	input   []Request
	states  map[string]*itemState
	order   []string // unique IDs in first-seen order
	pending []string
}

func newRun(c *Client, reqs []Request) *run {
	r := &run{
		client: c,
		input:  append([]Request(nil), reqs...),
		states: make(map[string]*itemState, len(reqs)),
	}
	for _, req := range reqs {
		if _, ok := r.states[req.ID]; !ok {
			r.states[req.ID] = &itemState{req: req}
			r.order = append(r.order, req.ID)
		}
	}
	r.pending = append([]string(nil), r.order...)
	return r
}

func (r *run) exec(ctx context.Context) *Result {
	if len(r.input) == 0 {
		return r.result()
	}
	results := make(chan batchResult)
	sem := make(chan struct{}, r.client.parallelism)
	unsettled := 0 // attempts whose final delivery has not been processed
	cancelled := false
	done := ctx.Done()

	for {
		// Dispatch pending items while capacity remains. Cancellation only
		// stops un-dispatched items; in-flight attempts run to completion.
		for len(r.pending) > 0 && !cancelled && ctx.Err() == nil && len(sem) < r.client.parallelism {
			n := r.client.batchSize
			if len(r.pending) < n {
				n = len(r.pending)
			}
			ids := r.pending[:n]
			r.pending = r.pending[n:]
			batch := make([]Request, 0, n)
			for _, id := range ids {
				st := r.states[id]
				if st.confirmed {
					continue // confirmed by a late response while queued
				}
				st.attempts++
				batch = append(batch, st.req)
			}
			if len(batch) == 0 {
				continue
			}
			sem <- struct{}{}
			unsettled++
			go r.attempt(ctx, batch, results)
		}
		if unsettled == 0 {
			for _, id := range r.pending {
				r.states[id].cancelled = true
			}
			r.pending = nil
			break
		}
		select {
		case br := <-results:
			if !br.late {
				<-sem // dispatch capacity is released at first delivery
			}
			if !br.timedOut {
				unsettled-- // a timed-out attempt settles on its late delivery
			}
			r.handle(br)
		case <-done:
			done = nil
			cancelled = true
			for _, id := range r.pending {
				r.states[id].cancelled = true
			}
			r.pending = nil
		}
	}

	return r.result()
}

// attempt performs one batch dispatch. With an attempt timeout, a slow call
// is reported as timed out so its items can be retried, while the real
// delivery is forwarded later as a late result.
func (r *run) attempt(ctx context.Context, batch []Request, results chan<- batchResult) {
	if r.client.attemptTimeout <= 0 {
		resps, err := r.client.transport.SubmitBatch(ctx, batch)
		results <- batchResult{batch: batch, resps: resps, err: err}
		return
	}
	ch := make(chan batchResult, 1)
	go func() {
		resps, err := r.client.transport.SubmitBatch(ctx, batch)
		ch <- batchResult{batch: batch, resps: resps, err: err}
	}()
	timer := time.NewTimer(r.client.attemptTimeout)
	defer timer.Stop()
	select {
	case br := <-ch:
		results <- br
	case <-timer.C:
		results <- batchResult{batch: batch, err: ErrAttemptTimeout, timedOut: true}
		go func() {
			br := <-ch
			br.late = true
			results <- br
		}()
	}
}

// handle folds one batch result into the run state. Batch-level failures
// never discard confirmed items; only items missing from the attempt are
// retried, under their original IDs.
func (r *run) handle(br batchResult) {
	if br.late {
		for _, resp := range br.resps {
			r.apply(resp)
		}
		return
	}
	responded := make(map[string]bool, len(br.resps))
	for _, resp := range br.resps {
		responded[resp.ID] = true
		r.apply(resp)
	}
	for _, req := range br.batch {
		st := r.states[req.ID]
		if st.confirmed || responded[req.ID] {
			continue
		}
		if st.attempts < r.client.maxAttempts {
			r.pending = append(r.pending, req.ID)
		} else {
			st.missing = true
		}
	}
}

func (r *run) apply(resp Response) {
	if st, ok := r.states[resp.ID]; ok {
		st.apply(resp)
	}
}

func (r *run) result() *Result {
	res := &Result{
		Outcomes:   make([]Outcome, len(r.input)),
		Checkpoint: r.checkpoint(),
	}
	for i, req := range r.input {
		res.Outcomes[i] = r.states[req.ID].outcome()
		res.Outcomes[i].Request = req
	}
	return res
}

func (r *run) checkpoint() Checkpoint {
	cp := Checkpoint{
		Requests:  append([]Request(nil), r.input...),
		Confirmed: make(map[string]ConfirmedItem),
	}
	for _, id := range r.order {
		st := r.states[id]
		if st.confirmed {
			cp.Confirmed[id] = ConfirmedItem{
				Version:  st.version,
				Value:    st.value,
				Err:      st.errMsg,
				Conflict: st.conflict,
			}
		} else {
			cp.Missing = append(cp.Missing, id)
		}
	}
	return cp
}
