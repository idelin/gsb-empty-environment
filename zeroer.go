package netsec

import "sync"

// Zeroer wipes the backing memory of a physical page before the page is
// returned to the pool. Implementations must be safe for concurrent use.
//
// A failed Zero call must leave the page unreturned: the pool moves the page
// to its quarantine instead of the free list, so stale secret bytes can never
// be handed out again.
type Zeroer interface {
	Zero(pageID int, mem []byte) error
}

// PausableZeroer is a Zeroer whose progress can be suspended. While paused,
// Zero calls block until Resume is invoked, which lets tests observe pages
// sitting in the zeroing queue and prove that they are not reallocated before
// being wiped. It also supports deterministic failure injection.
type PausableZeroer struct {
	mu       sync.Mutex
	cond     *sync.Cond
	paused   bool
	failIDs  map[int]struct{}
	failLeft int
	calls    int64
	zeroed   []int
	failed   []int
}

// NewPausableZeroer creates a running (unpaused) zeroer.
func NewPausableZeroer() *PausableZeroer {
	z := &PausableZeroer{failIDs: make(map[int]struct{})}
	z.cond = sync.NewCond(&z.mu)
	return z
}

// Pause suspends subsequent Zero calls. Calls already in progress finish.
func (z *PausableZeroer) Pause() {
	z.mu.Lock()
	z.paused = true
	z.mu.Unlock()
}

// Resume unblocks Zero calls blocked in Pause. It is idempotent.
func (z *PausableZeroer) Resume() {
	z.mu.Lock()
	z.paused = false
	z.cond.Broadcast()
	z.mu.Unlock()
}

// Paused reports whether the zeroer is currently paused.
func (z *PausableZeroer) Paused() bool {
	z.mu.Lock()
	defer z.mu.Unlock()
	return z.paused
}

// FailPage arranges for every future Zero call targeting pageID to fail
// until ResetFailures is called.
func (z *PausableZeroer) FailPage(pageID int) {
	z.mu.Lock()
	z.failIDs[pageID] = struct{}{}
	z.mu.Unlock()
}

// FailNext arranges for the next n Zero calls (regardless of page ID) to
// fail until ResetFailures is called.
func (z *PausableZeroer) FailNext(n int) {
	z.mu.Lock()
	if n > 0 {
		z.failLeft += n
	}
	z.mu.Unlock()
}

// ResetFailures clears all failure injection installed via FailPage/FailNext.
func (z *PausableZeroer) ResetFailures() {
	z.mu.Lock()
	z.failIDs = make(map[int]struct{})
	z.failLeft = 0
	z.mu.Unlock()
}

// Zero implements Zeroer. It blocks while paused, then either fails (leaving
// mem untouched) or overwrites every byte of mem with zero.
func (z *PausableZeroer) Zero(pageID int, mem []byte) error {
	z.mu.Lock()
	for z.paused {
		z.cond.Wait()
	}
	z.calls++
	_, failByID := z.failIDs[pageID]
	if !failByID && z.failLeft > 0 {
		failByID = true
		z.failLeft--
	}
	if failByID {
		z.failed = append(z.failed, pageID)
		z.mu.Unlock()
		return ErrZeroFailed
	}
	z.zeroed = append(z.zeroed, pageID)
	z.mu.Unlock()

	for i := range mem {
		mem[i] = 0
	}
	return nil
}

// Calls returns the total number of Zero invocations, including failed ones.
func (z *PausableZeroer) Calls() int64 {
	z.mu.Lock()
	defer z.mu.Unlock()
	return z.calls
}

// ZeroedIDs returns the IDs of pages wiped successfully, in call order.
func (z *PausableZeroer) ZeroedIDs() []int {
	z.mu.Lock()
	defer z.mu.Unlock()
	return append([]int(nil), z.zeroed...)
}

// FailedIDs returns the IDs of pages whose Zero call failed, in call order.
func (z *PausableZeroer) FailedIDs() []int {
	z.mu.Lock()
	defer z.mu.Unlock()
	return append([]int(nil), z.failed...)
}
