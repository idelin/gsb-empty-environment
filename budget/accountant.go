package budget

import "sync"

// accountant is the single thread-safe authority for the runtime budgets. All
// enforcement decisions are made under the mutex so concurrent leaf streams can
// never individually pass a check and collectively overshoot the limit.
type accountant struct {
	mu sync.Mutex

	limits Limits

	// total is the cumulative number of bytes actually accepted by sinks
	// across every reference. It only ever grows, so short-write refunds never
	// erase bytes that were genuinely decoded on another reference.
	total int64

	// staged is the number of bytes currently held in live staging buffers.
	// It grows on reserved writes and shrinks on short-write refunds and on
	// reclaim.
	staged int64

	// live is the number of buffers currently in flight (decode started, not
	// yet committed or reclaimed).
	live int

	peakLive  int
	peakBytes int64
}

func newAccountant(limits Limits) *accountant {
	return &accountant{limits: limits}
}

// reserve attempts to reserve n bytes before they are written, evaluated
// against the cumulative decoded total. It returns ErrTotalBytesExceeded when
// the reservation would overshoot the byte budget; in that case no state
// changes and the caller must not write anything.
func (a *accountant) reserve(n int) error {
	if n < 0 {
		return ErrTotalBytesExceeded
	}
	a.mu.Lock()
	defer a.mu.Unlock()

	next := a.total + int64(n)
	if next < a.total || next > a.limits.MaxTotalBytes {
		return ErrTotalBytesExceeded
	}
	a.total = next
	a.staged += int64(n)
	if a.staged > a.peakBytes {
		a.peakBytes = a.staged
	}
	return nil
}

// refund gives back bytes reserved but never actually accepted (a short write).
// It reduces only the current staged count; the cumulative decoded total keeps
// counting bytes that other references really produced.
func (a *accountant) refund(n int) {
	if n <= 0 {
		return
	}
	a.mu.Lock()
	a.staged -= int64(n)
	if a.staged < 0 {
		a.staged = 0
	}
	a.mu.Unlock()
}

// enterLive registers a newly in-flight buffer. The fixed worker pool in
// Unfold guarantees live never exceeds MaxLiveBuffers; the peak is recorded
// here for observability and test verification.
func (a *accountant) enterLive() {
	a.mu.Lock()
	a.live++
	if a.live > a.peakLive {
		a.peakLive = a.live
	}
	a.mu.Unlock()
}

func (a *accountant) exitLive() {
	a.mu.Lock()
	if a.live > 0 {
		a.live--
	}
	a.mu.Unlock()
}

// reclaim removes the bytes of one staged buffer from the current staged
// count when that buffer is published or reclaimed.
func (a *accountant) reclaim(n int) {
	if n <= 0 {
		return
	}
	a.mu.Lock()
	a.staged -= int64(n)
	if a.staged < 0 {
		a.staged = 0
	}
	a.mu.Unlock()
}

func (a *accountant) snapshot() (total int64, peakLive int, peakBytes int64) {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.total, a.peakLive, a.peakBytes
}
