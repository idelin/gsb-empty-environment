package tls13zerortt

import (
	"sync"
	"time"
)

// ReplayVerdict classifies a single-use check result.
type ReplayVerdict uint8

const (
	// ReplayFirstUse means the identity was never seen in the active window;
	// it has now been registered and early_data may be admitted.
	ReplayFirstUse ReplayVerdict = iota
	// ReplayDuplicate means the identity already consumed its single 0-RTT
	// attempt; early_data must be rejected and the handshake downgraded to
	// 1-RTT resumption.
	ReplayDuplicate
	// ReplaySaturated means the bounded registry evicted state and can no
	// longer prove single-use semantics; the host fails closed to 1-RTT.
	ReplaySaturated
)

// ReplayFilter is the single-use gate for ticket identities. Implementations
// must be safe for concurrent use across gateway nodes' local state; a
// distributed host can back this interface with shared storage while keeping
// the same fail-closed contract.
type ReplayFilter interface {
	// CheckAndRegister atomically records identity for the bucket containing
	// now and reports whether this is the first accepted 0-RTT use.
	CheckAndRegister(identity string, now time.Time) ReplayVerdict
	// Rotate advances time-bucket rotation eagerly; normal calls rotate
	// lazily, so this is optional for hosts that prefer a timer.
	Rotate(now time.Time)
}

// replayBucket is one time window of observed identities.
type replayBucket struct {
	index     int64
	seen      map[string]struct{}
	saturated bool
}

func newReplayBucket(index int64) *replayBucket {
	return &replayBucket{index: index, seen: make(map[string]struct{})}
}

// bucketRegistry keeps the current and immediately preceding bucket so that an
// identity accepted near a rotation boundary is still detected after rotation.
// Two buckets are sufficient because a ClientHello can only straddle one
// boundary; older buckets are dropped, bounding memory.
type bucketRegistry struct {
	mu       sync.Mutex
	window   time.Duration
	current  *replayBucket
	previous *replayBucket
}

func (r *bucketRegistry) bucketIndex(now time.Time) int64 {
	return now.UnixNano() / r.window.Nanoseconds()
}

// Rotate retires buckets older than the bucket immediately preceding the
// bucket containing now.
func (r *bucketRegistry) Rotate(now time.Time) {
	idx := r.bucketIndex(now)
	r.mu.Lock()
	r.rotateLocked(idx)
	r.mu.Unlock()
}

func (r *bucketRegistry) rotateLocked(idx int64) {
	switch {
	case r.current == nil:
		r.current = newReplayBucket(idx)
	case idx == r.current.index:
		// Same window.
	case idx == r.current.index+1:
		r.previous = r.current
		r.current = newReplayBucket(idx)
	default:
		// Jumped at least two windows: neither retained bucket can contain
		// identities relevant to idx, so drop all state.
		r.previous = nil
		r.current = newReplayBucket(idx)
	}
}

// exactRegistry retains every identity seen within the two live buckets.
// Memory is bounded by the rotation window and traffic volume, hence it is
// "exact" but not hard-capped.
type exactRegistry struct {
	bucketRegistry
}

// NewExactReplayFilter builds an exact (non-evicting) two-bucket registry with
// the given rotation window. window must be positive.
func NewExactReplayFilter(window time.Duration) (ReplayFilter, error) {
	if window <= 0 {
		return nil, errInvalidWindow
	}
	return &exactRegistry{bucketRegistry: bucketRegistry{window: window}}, nil
}

// CheckAndRegister implements ReplayFilter.
func (r *exactRegistry) CheckAndRegister(identity string, now time.Time) ReplayVerdict {
	idx := r.bucketIndex(now)
	r.mu.Lock()
	defer r.mu.Unlock()
	r.rotateLocked(idx)

	if r.previous != nil {
		if _, ok := r.previous.seen[identity]; ok {
			return ReplayDuplicate
		}
	}
	if _, ok := r.current.seen[identity]; ok {
		return ReplayDuplicate
	}
	r.current.seen[identity] = struct{}{}
	return ReplayFirstUse
}

// boundedRegistry hard-caps the number of identities per bucket. On overflow
// the bucket is marked saturated: unknown identities then fail closed
// (ReplaySaturated) instead of being admitted on incomplete state, while known
// identities are still reliably detected as replays.
type boundedRegistry struct {
	bucketRegistry
	capacity int
}

// NewBoundedReplayFilter builds a memory-bounded registry that retains at most
// capacity identities per bucket (up to 2*capacity across the two live
// buckets). window must be positive and capacity at least one.
func NewBoundedReplayFilter(window time.Duration, capacity int) (ReplayFilter, error) {
	if window <= 0 {
		return nil, errInvalidWindow
	}
	if capacity <= 0 {
		return nil, errInvalidCapacity
	}
	return &boundedRegistry{
		bucketRegistry: bucketRegistry{window: window},
		capacity:       capacity,
	}, nil
}

// CheckAndRegister implements ReplayFilter with fail-closed saturation.
func (r *boundedRegistry) CheckAndRegister(identity string, now time.Time) ReplayVerdict {
	idx := r.bucketIndex(now)
	r.mu.Lock()
	defer r.mu.Unlock()
	r.rotateLocked(idx)

	if r.previous != nil {
		if _, ok := r.previous.seen[identity]; ok {
			return ReplayDuplicate
		}
		// A saturated previous bucket may have evicted this identity, so a
		// miss cannot prove freshness. Fail closed rather than admitting a
		// possible cross-bucket replay.
		if r.previous.saturated {
			return ReplaySaturated
		}
	}
	if _, ok := r.current.seen[identity]; ok {
		return ReplayDuplicate
	}
	if r.current.saturated {
		return ReplaySaturated
	}
	if len(r.current.seen) >= r.capacity {
		r.current.saturated = true
		return ReplaySaturated
	}
	r.current.seen[identity] = struct{}{}
	return ReplayFirstUse
}
