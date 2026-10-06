// Package snapshot provides a fan-out snapshot stream: each subscriber
// receives the latest complete published version. Under backlog,
// intermediate versions may be skipped, but fragments from different
// versions are never spliced together. Fragments are reference counted;
// a receiver releases them by acking the snapshot, and cancelling a
// subscription dereferences everything it holds immediately.
package snapshot

import (
	"errors"
	"sync"
	"sync/atomic"
)

// ErrClosed is returned by operations on a closed publisher or a
// cancelled subscription.
var ErrClosed = errors.New("snapshot: closed")

// Fragment is one immutable chunk of a version. Its Data must not be
// mutated after Publish returns.
type Fragment struct {
	Data []byte

	refs int32
	pub  *Publisher
}

func (f *Fragment) acquire() {
	atomic.AddInt32(&f.refs, 1)
}

func (f *Fragment) release() {
	if atomic.AddInt32(&f.refs, -1) == 0 && f.pub != nil {
		atomic.AddInt64(&f.pub.live, -1)
	}
}

// version is a complete, immutable set of fragments published atomically.
type version struct {
	id        uint64
	fragments []*Fragment
}

func (v *version) acquire() {
	for _, f := range v.fragments {
		f.acquire()
	}
}

func (v *version) release() {
	for _, f := range v.fragments {
		f.release()
	}
}

// Publisher fans versions out to subscribers.
type Publisher struct {
	mu     sync.Mutex
	subs   map[*Subscription]struct{}
	seq    uint64
	closed bool
	live   int64 // live fragment count, for leak tracking
}

// NewPublisher returns a ready-to-use Publisher.
func NewPublisher() *Publisher {
	return &Publisher{subs: make(map[*Subscription]struct{})}
}

// Publish atomically publishes one version built from the given fragment
// payloads. Every subscriber queues the whole version or nothing; a
// subscriber whose queue is full drops its oldest queued version (never
// part of one). Returns the new version ID.
func (p *Publisher) Publish(fragments ...[]byte) (uint64, error) {
	return p.PublishFunc(len(fragments), func(_ uint64, i int) []byte {
		return fragments[i]
	})
}

// PublishFunc is like Publish but generates each fragment payload with
// gen(version, index), so content can embed the version ID being
// published. gen must not retain or mutate the returned slices.
func (p *Publisher) PublishFunc(n int, gen func(version uint64, index int) []byte) (uint64, error) {
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return 0, ErrClosed
	}
	p.seq++
	v := &version{id: p.seq, fragments: make([]*Fragment, n)}
	for i := 0; i < n; i++ {
		f := &Fragment{Data: gen(p.seq, i), refs: 1, pub: p} // 1 = publisher hold
		v.fragments[i] = f
		atomic.AddInt64(&p.live, 1)
	}
	for s := range p.subs {
		s.enqueue(v)
	}
	p.mu.Unlock()
	v.release() // drop publisher hold; queued refs keep it alive
	return v.id, nil
}

// LiveFragments reports how many published fragments are still
// referenced (queued or held by receivers). Zero means no leaks.
func (p *Publisher) LiveFragments() int64 {
	return atomic.LoadInt64(&p.live)
}

// Subscribe returns a Subscription whose queue holds at most capacity
// complete versions. Capacity must be >= 1.
func (p *Publisher) Subscribe(capacity int) (*Subscription, error) {
	if capacity < 1 {
		return nil, errors.New("snapshot: capacity must be >= 1")
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return nil, ErrClosed
	}
	s := &Subscription{pub: p, cap: capacity}
	s.cond = sync.NewCond(&s.mu)
	p.subs[s] = struct{}{}
	return s, nil
}

func (p *Publisher) remove(s *Subscription) {
	p.mu.Lock()
	delete(p.subs, s)
	p.mu.Unlock()
}

// Close cancels all subscriptions and rejects further publishes.
func (p *Publisher) Close() {
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return
	}
	p.closed = true
	subs := make([]*Subscription, 0, len(p.subs))
	for s := range p.subs {
		subs = append(subs, s)
	}
	p.subs = make(map[*Subscription]struct{})
	p.mu.Unlock()
	for _, s := range subs {
		s.cancel()
	}
}

// Subscription is one receiver's bounded queue of complete versions.
type Subscription struct {
	pub    *Publisher
	mu     sync.Mutex
	cond   *sync.Cond
	queue  []*version
	cap    int
	closed bool
}

// enqueue appends a reference to v, dropping (and releasing) the oldest
// queued version if the queue is full. Whole versions only.
func (s *Subscription) enqueue(v *version) {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	v.acquire()
	if len(s.queue) == s.cap {
		s.queue[0].release()
		s.queue = s.queue[1:]
	}
	s.queue = append(s.queue, v)
	s.cond.Signal()
	s.mu.Unlock()
}

// Next blocks until a complete version is available and returns it, or
// ok == false once the subscription is cancelled and drained.
func (s *Subscription) Next() (snap *Snapshot, ok bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for len(s.queue) == 0 && !s.closed {
		s.cond.Wait()
	}
	if len(s.queue) == 0 {
		return nil, false
	}
	v := s.queue[0]
	s.queue = s.queue[1:]
	return &Snapshot{v: v}, true
}

// Cancel unsubscribes and immediately releases every queued version.
// In-flight Snapshots already handed out stay valid until Released.
func (s *Subscription) Cancel() {
	s.pub.remove(s)
	s.cancel()
}

func (s *Subscription) cancel() {
	s.mu.Lock()
	for _, v := range s.queue {
		v.release()
	}
	s.queue = nil
	s.closed = true
	s.cond.Broadcast()
	s.mu.Unlock()
}

// Snapshot is one complete version handed to a receiver. Call Release
// (the ack) when done; its fragments are freed once no snapshot or
// queue references them.
type Snapshot struct {
	v    *version
	once sync.Once
}

// Version returns the published version ID.
func (s *Snapshot) Version() uint64 { return s.v.id }

// Fragments returns the version's fragments. Valid until Release.
func (s *Snapshot) Fragments() []Fragment {
	frags := make([]Fragment, len(s.v.fragments))
	for i, f := range s.v.fragments {
		frags[i] = Fragment{Data: f.Data}
	}
	return frags
}

// Release acks the snapshot and dereferences its fragments. Idempotent.
func (s *Snapshot) Release() {
	s.once.Do(func() { s.v.release() })
}
