// Package snapshot provides a snapshot streaming library.
//
// A Stream publishes immutable, versioned snapshots. Each snapshot is
// fragmented for delivery. Every subscriber is always offered the latest
// complete version: when a subscriber falls behind, intermediate versions
// may be skipped wholesale, but fragments of different versions are never
// spliced together — a subscriber's queue holds fragments of at most one
// version at any time, and the receiver always observes a contiguous
// prefix (starting at index 0) of each version it sees.
//
// Lifecycle of a fragment:
//
//	published -> queued (per subscriber) -> received -> acknowledged -> released
//
// Received fragments stay referenced (counted as retained) until the
// receiver calls Ack for their version. Cancel immediately dereferences
// everything held for the subscriber.
//
// Implementation note: the per-subscriber queue is a slice guarded by a
// mutex, not a channel. A channel's direct handoff to a parked receiver
// commits a receive at send time, bypassing the buffer; a later wholesale
// eviction could then leave the receiver observing fragments that were
// never contiguous in the queue. Popping and evicting under one mutex
// makes the prefix invariant hold by construction.
package snapshot

import (
	"errors"
	"sync"
)

var (
	// ErrClosed is returned by Recv after the subscription is cancelled
	// or the stream is closed.
	ErrClosed = errors.New("snapshot: subscription closed")
	// ErrEmptySnapshot is returned by Publish for a snapshot with no parts.
	ErrEmptySnapshot = errors.New("snapshot: snapshot has no parts")
	// ErrVersionTooLarge is returned by Publish when the snapshot has more
	// fragments than a subscriber queue can ever hold.
	ErrVersionTooLarge = errors.New("snapshot: fragment count exceeds queue capacity")
	// ErrVersionRegression is returned by Publish when the snapshot version
	// is not greater than the latest published version.
	ErrVersionRegression = errors.New("snapshot: version must be strictly increasing")
)

// Fragment is one immutable chunk of a versioned snapshot. Fragments of
// different versions must never be reassembled together; Version, Index
// and Count let a receiver detect and discard partial versions.
type Fragment struct {
	Version uint64
	Index   int
	Count   int
	Data    []byte
}

// Snapshot is a complete versioned content unit. Parts are the ordered
// content fragments; they are copied on Publish, so the caller may reuse
// the buffers afterwards.
type Snapshot struct {
	Version uint64
	Parts   [][]byte
}

func (s Snapshot) fragments() []Fragment {
	frags := make([]Fragment, len(s.Parts))
	for i, p := range s.Parts {
		cp := make([]byte, len(p))
		copy(cp, p)
		frags[i] = Fragment{Version: s.Version, Index: i, Count: len(s.Parts), Data: cp}
	}
	return frags
}

// Stream publishes versioned snapshots to any number of subscribers.
// It is safe for concurrent use, but Publish calls are serialized and
// versions must be strictly increasing.
type Stream struct {
	mu     sync.Mutex
	cap    int // per-subscriber queue capacity, in fragments
	subs   map[*Subscription]struct{}
	latest []Fragment // latest complete version, offered to new subscribers
	lastV  uint64
	hasV   bool
	closed bool
}

// NewStream creates a Stream whose per-subscriber queues hold up to
// queueCapacity fragments. A snapshot with more fragments than
// queueCapacity can never be delivered and is rejected by Publish.
func NewStream(queueCapacity int) *Stream {
	if queueCapacity < 1 {
		queueCapacity = 1
	}
	return &Stream{
		cap:  queueCapacity,
		subs: make(map[*Subscription]struct{}),
	}
}

// Publish fragments snap and offers it to every subscriber. Subscribers
// whose queues still hold fragments of older versions have those fragments
// evicted atomically — intermediate versions are skipped, never spliced.
// The stream keeps only the latest version for future subscribers.
func (s *Stream) Publish(snap Snapshot) error {
	if len(snap.Parts) == 0 {
		return ErrEmptySnapshot
	}
	if len(snap.Parts) > s.cap {
		return ErrVersionTooLarge
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return ErrClosed
	}
	if s.hasV && snap.Version <= s.lastV {
		return ErrVersionRegression
	}
	frags := snap.fragments()
	s.latest = frags
	s.lastV, s.hasV = snap.Version, true
	for sub := range s.subs {
		sub.enqueue(frags)
	}
	return nil
}

// Subscribe returns a new Subscription. If a version has already been
// published, the subscription is pre-loaded with the latest complete
// version so a late joiner starts up to date.
func (s *Stream) Subscribe() *Subscription {
	sub := newSubscription(s, s.cap)
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		sub.cancelLocked()
		return sub
	}
	if s.latest != nil {
		sub.enqueue(s.latest)
	}
	s.subs[sub] = struct{}{}
	return sub
}

// Close cancels all subscriptions and releases the retained latest
// version. Publishing after Close returns ErrClosed.
func (s *Stream) Close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return
	}
	s.closed = true
	s.latest = nil
	for sub := range s.subs {
		sub.cancelLocked()
	}
	s.subs = make(map[*Subscription]struct{})
}

// Stats is a point-in-time snapshot of stream state.
type Stats struct {
	Subscribers   int
	LatestVersion uint64
	HasVersion    bool
}

// Stats returns current stream statistics.
func (s *Stream) Stats() Stats {
	s.mu.Lock()
	defer s.mu.Unlock()
	return Stats{Subscribers: len(s.subs), LatestVersion: s.lastV, HasVersion: s.hasV}
}

// Subscription is a per-subscriber view of a Stream. Recv is safe to call
// concurrently with Publish and Cancel, but at most one goroutine should
// call Recv at a time.
type Subscription struct {
	stream *Stream

	mu          sync.Mutex
	cond        sync.Cond
	queue       []Fragment     // fragments of at most one version
	queuedBytes int            // bytes sitting in queue, not yet received
	unacked     map[uint64]int // version -> received bytes awaiting ack
	closed      bool
}

func newSubscription(stream *Stream, capacity int) *Subscription {
	s := &Subscription{
		stream:  stream,
		queue:   make([]Fragment, 0, capacity),
		unacked: make(map[uint64]int),
	}
	s.cond.L = &s.mu
	return s
}

// enqueue atomically replaces any queued older-version fragments with
// frags, which must all belong to a single newer version and fit the
// queue capacity. Called by the stream while publishing.
func (s *Subscription) enqueue(frags []Fragment) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return
	}
	// Evict the previous version wholesale: skip, never splice.
	s.queue = s.queue[:0]
	s.queuedBytes = 0
	s.queue = append(s.queue, frags...)
	for _, f := range frags {
		s.queuedBytes += len(f.Data)
	}
	s.cond.Broadcast()
}

// Recv returns the next queued fragment, blocking until one is available
// or the subscription is cancelled (or the stream is closed). Received
// fragments are retained (accounted) until Ack releases them.
//
// The received stream is a concatenation of contiguous version prefixes:
// each run of same-version fragments starts at index 0 and is strictly
// ordered, so a receiver can always detect a truncated version and wait
// for the next one.
func (s *Subscription) Recv() (Fragment, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for len(s.queue) == 0 && !s.closed {
		s.cond.Wait()
	}
	if len(s.queue) == 0 {
		return Fragment{}, ErrClosed
	}
	return s.popLocked(), nil
}

// TryRecv is the non-blocking form of Recv: it reports whether a fragment
// was available.
func (s *Subscription) TryRecv() (Fragment, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.queue) == 0 {
		return Fragment{}, false
	}
	return s.popLocked(), true
}

func (s *Subscription) popLocked() Fragment {
	f := s.queue[0]
	s.queue[0] = Fragment{} // drop the reference eagerly
	s.queue = s.queue[1:]
	s.queuedBytes -= len(f.Data)
	s.unacked[f.Version] += len(f.Data)
	return f
}

// Ack releases all received fragments of versions <= version. Acknowledging
// a complete version tells the stream its old fragments may be freed.
func (s *Subscription) Ack(version uint64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for v := range s.unacked {
		if v <= version {
			delete(s.unacked, v)
		}
	}
}

// Cancel immediately dereferences every fragment held for this
// subscriber — queued and received-but-unacked alike — and detaches the
// subscription from the stream. It is idempotent and unblocks Recv.
func (s *Subscription) Cancel() {
	// Lock ordering is stream.mu -> sub.mu everywhere, so detach first
	// to avoid inverting it.
	s.stream.mu.Lock()
	delete(s.stream.subs, s)
	s.stream.mu.Unlock()
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cancelLocked()
}

func (s *Subscription) cancelLocked() {
	if s.closed {
		return
	}
	s.closed = true
	s.queue = nil
	s.queuedBytes = 0
	s.unacked = make(map[uint64]int)
	s.cond.Broadcast()
}

// RetainedBytes reports how many fragment bytes this subscription still
// references: queued fragments plus received-but-unacked fragments.
func (s *Subscription) RetainedBytes() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := s.queuedBytes
	for _, b := range s.unacked {
		n += b
	}
	return n
}
