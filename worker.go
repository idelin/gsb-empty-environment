package quota

import (
	"sync"
	"sync/atomic"
	"time"
)

// renewThreshold triggers an asynchronous renewal once the local budget
// drops below 20% of the grant, so the heartbeat lands before exhaustion.
const renewThreshold = 5 // tokens*5 < grant  =>  below 20%

// minBatch is the smallest renewal batch, keeping RPCs coarse-grained.
const minBatch = 100

// minRenewInterval bounds the heartbeat rate when the pool is exhausted
// and renewals come back empty-handed.
const minRenewInterval = 20 * time.Millisecond

// Worker consumes tokens from a locally pre-allocated lease and renews
// quota from the Master with batched, asynchronous heartbeats.
type Worker struct {
	id     string
	master *Master

	mu     sync.Mutex
	tokens int64 // local tokens still available in this window
	grant  int64 // current lease (tokens per window)
	used   int64 // consumed since the last heartbeat report

	inflight  int32 // 1 while a renewal heartbeat is in flight
	lastRenew int64 // unix-nano of the last heartbeat start
	rpcs      int64 // heartbeats sent, exposed for tests/metrics

	stopCh chan struct{}
	done   chan struct{}
}

func newWorker(id string, m *Master, grant int64) *Worker {
	w := &Worker{
		id:     id,
		master: m,
		grant:  grant,
		tokens: grant,
		stopCh: make(chan struct{}),
		done:   make(chan struct{}),
	}
	go w.refillLoop()
	return w
}

// Allow consumes one local token. It never blocks on the network; when the
// local budget runs low it merely kicks off an asynchronous renewal.
func (w *Worker) Allow() bool {
	w.mu.Lock()
	if w.tokens <= 0 {
		w.mu.Unlock()
		w.maybeRenew()
		return false
	}
	w.tokens--
	w.used++
	low := w.tokens*renewThreshold < w.grant
	w.mu.Unlock()

	if low {
		w.maybeRenew()
	}
	return true
}

// maybeRenew starts at most one renewal heartbeat at a time; concurrent
// triggers coalesce into the in-flight batch, keeping RPC frequency low.
func (w *Worker) maybeRenew() {
	now := time.Now()
	last := time.Unix(0, atomic.LoadInt64(&w.lastRenew))
	if now.Sub(last) < minRenewInterval {
		return
	}
	if !atomic.CompareAndSwapInt32(&w.inflight, 0, 1) {
		return
	}
	atomic.StoreInt64(&w.lastRenew, now.UnixNano())
	go func() {
		defer atomic.StoreInt32(&w.inflight, 0)

		w.mu.Lock()
		used := w.used
		want := w.grant
		if want < minBatch {
			want = minBatch
		}
		w.mu.Unlock()

		resp := w.master.Heartbeat(HeartbeatRequest{
			WorkerID: w.id,
			Used:     used,
			Want:     want,
		})
		atomic.AddInt64(&w.rpcs, 1)

		w.mu.Lock()
		w.used -= used
		delta := resp.Grant - w.grant
		w.grant = resp.Grant
		w.tokens += delta
		if w.tokens < 0 {
			w.tokens = 0
		}
		w.mu.Unlock()
	}()
}

// refillLoop slides the local window: each window starts with a full lease.
func (w *Worker) refillLoop() {
	t := time.NewTicker(w.master.window)
	defer t.Stop()
	defer close(w.done)
	for {
		select {
		case <-w.stopCh:
			return
		case <-t.C:
			w.mu.Lock()
			w.tokens = w.grant
			w.mu.Unlock()
		}
	}
}

// Grant returns the worker's current lease.
func (w *Worker) Grant() int64 {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.grant
}

// Tokens returns the locally remaining tokens in the current window.
func (w *Worker) Tokens() int64 {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.tokens
}

// RPCCount returns how many heartbeat RPCs this worker has sent.
func (w *Worker) RPCCount() int64 {
	return atomic.LoadInt64(&w.rpcs)
}

// Close stops the background refill loop.
func (w *Worker) Close() {
	close(w.stopCh)
	<-w.done
}
