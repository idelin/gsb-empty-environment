package quota

import (
	"sync"
	"sync/atomic"
	"time"
)

// HeartbeatRequest is the batched renewal a worker sends to the Master.
type HeartbeatRequest struct {
	WorkerID string
	Used     int64 // tokens consumed since the previous heartbeat
	Want     int64 // additional tokens requested for this window
}

// HeartbeatResponse carries the outcome of a Register or Heartbeat call.
type HeartbeatResponse struct {
	Additional int64     // extra tokens granted on top of the current lease
	Remaining  int64     // master's view of the lease balance (granted - used)
	ExpiresAt  time.Time // lease deadline; tokens die after this instant
}

// Heartbeater is the transport between a Worker and the Master.
// RPCHeartbeater implements it over net/rpc; tests may use a direct call.
type Heartbeater interface {
	Heartbeat(req HeartbeatRequest) (HeartbeatResponse, error)
}

// Worker consumes a leased quota locally. Allow is a pure in-memory
// operation; network RPCs happen only on the asynchronous renewal path,
// at most one in flight at any time.
type Worker struct {
	id     string
	master Heartbeater

	batchSize      int64   // tokens requested per heartbeat
	renewThreshold float64 // low watermark as a fraction of the last grant

	mu              sync.Mutex
	tokens          int64 // locally spendable balance
	lastGrant       int64 // last granted batch, basis for the watermark
	usedSinceReport int64 // consumed but not yet reported to the Master
	leaseEnd        time.Time

	renewing atomic.Bool // at most one heartbeat in flight
	wg       sync.WaitGroup

	heartbeats atomic.Int64 // renewals actually performed (observability)
	now        func() time.Time
}

// NewWorker creates a worker that renews in batches of batchSize tokens
// whenever its local balance falls below renewThreshold (a fraction in
// (0,1]) of the last grant, or hits zero.
func NewWorker(id string, master Heartbeater, batchSize int64, renewThreshold float64) *Worker {
	return &Worker{
		id:             id,
		master:         master,
		batchSize:      batchSize,
		renewThreshold: renewThreshold,
		now:            time.Now,
	}
}

// SetClock overrides the time source. Intended for tests.
func (w *Worker) SetClock(now func() time.Time) { w.now = now }

// Register performs the initial quota acquisition from the Master.
func (w *Worker) Register() error {
	resp, err := w.master.Heartbeat(HeartbeatRequest{WorkerID: w.id, Want: 0})
	if err != nil {
		return err
	}
	w.mu.Lock()
	w.tokens += resp.Additional
	w.lastGrant = resp.Additional
	w.leaseEnd = resp.ExpiresAt
	w.mu.Unlock()
	return nil
}

// Allow consumes one local token. It never blocks on the network: when
// the balance runs low it fires an asynchronous batched renewal and
// either serves the request from the remaining balance or fails closed.
func (w *Worker) Allow() bool {
	w.mu.Lock()
	if !w.now().Before(w.leaseEnd) {
		w.tokens = 0 // lease expired; must renew before spending
	}
	ok := w.tokens > 0
	if ok {
		w.tokens--
		w.usedSinceReport++
	}
	low := w.tokens <= int64(float64(w.lastGrant)*w.renewThreshold)
	w.mu.Unlock()

	if low || !ok {
		w.triggerRenew()
	}
	return ok
}

// triggerRenew starts one asynchronous renewal if none is in flight.
func (w *Worker) triggerRenew() {
	if !w.renewing.CompareAndSwap(false, true) {
		return
	}
	w.wg.Add(1)
	go func() {
		defer w.wg.Done()
		defer w.renewing.Store(false)
		w.renew(w.batchSize)
	}()
}

// renew performs a single heartbeat round-trip, asking for want tokens.
func (w *Worker) renew(want int64) {
	w.mu.Lock()
	used := w.usedSinceReport
	w.usedSinceReport = 0
	w.mu.Unlock()

	resp, err := w.master.Heartbeat(HeartbeatRequest{WorkerID: w.id, Used: used, Want: want})
	if err != nil {
		// Roll the usage back so it is reported on the next attempt.
		w.mu.Lock()
		w.usedSinceReport += used
		w.mu.Unlock()
		return
	}
	w.heartbeats.Add(1)
	w.mu.Lock()
	w.tokens += resp.Additional
	if resp.Additional > 0 {
		w.lastGrant = resp.Additional
	}
	// Reconcile with the master's view: if the master recalled part of
	// this lease to serve a busier peer, clamp the local balance so the
	// cluster converges back to the global cap. Usage accumulated while
	// the RPC was in flight is not yet reflected in resp.Remaining.
	if allowed := resp.Remaining - w.usedSinceReport; w.tokens > allowed {
		w.tokens = allowed
	}
	if w.tokens < 0 {
		w.tokens = 0
	}
	w.leaseEnd = resp.ExpiresAt
	w.mu.Unlock()
}

// Flush forces a synchronous heartbeat without requesting new tokens: it
// reports pending usage, applies any recall, and refreshes the lease.
// Useful on shutdown or before an idle period so the master can safely
// reallocate this worker's unused quota to busier peers.
func (w *Worker) Flush() { w.renew(0) }

// Wait blocks until all in-flight renewals have completed. Useful for
// demos and tests that need a quiescent point before asserting state.
func (w *Worker) Wait() { w.wg.Wait() }

// Stats is a point-in-time view of a worker's local state.
type Stats struct {
	Tokens     int64 // locally spendable balance
	Used       int64 // consumed but not yet reported
	Heartbeats int64 // renewals performed so far
	LeaseEnd   time.Time
}

// Stats returns the worker's current local state.
func (w *Worker) Stats() Stats {
	w.mu.Lock()
	defer w.mu.Unlock()
	return Stats{
		Tokens:     w.tokens,
		Used:       w.usedSinceReport,
		Heartbeats: w.heartbeats.Load(),
		LeaseEnd:   w.leaseEnd,
	}
}
