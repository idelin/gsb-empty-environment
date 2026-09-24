package canonkey

import (
	"bytes"
	"context"
	"errors"
	"sync"
	"time"
)

// ErrClosed reports use of a closed Store.
var ErrClosed = errors.New("canonkey: store is closed")

// Store is a local, in-memory result cache. It deliberately has no remote
// dependency: all data lives in this process. Entries are bucketed by
// digest; a lookup only hits when the stored canonical bytes exactly
// equal the request's canonical bytes, so a forced digest collision can
// never produce a wrong hit.
type Store struct {
	mu            sync.Mutex
	buckets       map[[32]byte][]*entry
	now           func() time.Time
	ttl           time.Duration
	sweepInterval time.Duration
	stop          chan struct{}
	done          chan struct{}
	closeOnce     sync.Once
	closed        bool
}

type entry struct {
	canon     []byte
	value     any
	expiresAt time.Time // zero when no TTL
}

// StoreOption configures a Store.
type StoreOption func(*Store)

// WithClock injects the clock used for TTL expiry (default time.Now).
func WithClock(now func() time.Time) StoreOption {
	return func(s *Store) { s.now = now }
}

// WithTTL enables entry expiry and starts a background sweeper.
func WithTTL(d time.Duration) StoreOption {
	return func(s *Store) { s.ttl = d }
}

// WithSweepInterval sets how often the sweeper evicts expired entries.
func WithSweepInterval(d time.Duration) StoreOption {
	return func(s *Store) { s.sweepInterval = d }
}

// NewStore creates a Store. Call Close to release its resources.
func NewStore(opts ...StoreOption) *Store {
	s := &Store{
		buckets:       make(map[[32]byte][]*entry),
		now:           time.Now,
		sweepInterval: 10 * time.Millisecond,
		done:          make(chan struct{}),
	}
	for _, o := range opts {
		o(s)
	}
	if s.ttl > 0 {
		s.stop = make(chan struct{})
		go s.sweepLoop()
	}
	return s
}

func (s *Store) sweepLoop() {
	t := time.NewTicker(s.sweepInterval)
	defer t.Stop()
	for {
		select {
		case <-s.stop:
			close(s.done)
			return
		case <-t.C:
			s.evictExpired()
		}
	}
}

func (s *Store) evictExpired() {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	for d, bucket := range s.buckets {
		kept := bucket[:0]
		for _, e := range bucket {
			if e.expiresAt.IsZero() || now.Before(e.expiresAt) {
				kept = append(kept, e)
			}
		}
		if len(kept) == 0 {
			delete(s.buckets, d)
		} else {
			s.buckets[d] = kept
		}
	}
}

// Get returns the cached value for k. The digest selects a bucket; the
// hit is confirmed by byte-exact comparison of the canonical request.
func (s *Store) Get(k Key) (any, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil, false
	}
	now := s.now()
	for _, e := range s.buckets[k.Digest] {
		if !e.expiresAt.IsZero() && !now.Before(e.expiresAt) {
			continue
		}
		if bytes.Equal(e.canon, k.Canon) {
			return e.value, true
		}
	}
	return nil, false
}

// Put stores v under k.
func (s *Store) Put(k Key, v any) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return ErrClosed
	}
	e := &entry{canon: append([]byte(nil), k.Canon...), value: v}
	if s.ttl > 0 {
		e.expiresAt = s.now().Add(s.ttl)
	}
	bucket := s.buckets[k.Digest]
	for i, old := range bucket {
		if bytes.Equal(old.canon, e.canon) {
			bucket[i] = e
			s.buckets[k.Digest] = bucket
			return nil
		}
	}
	s.buckets[k.Digest] = append(bucket, e)
	return nil
}

// Len reports the number of stored entries.
func (s *Store) Len() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for _, b := range s.buckets {
		n += len(b)
	}
	return n
}

// Close releases the store's resources, stopping the background sweeper
// if any. It is idempotent. Done closes once shutdown completed.
func (s *Store) Close() error {
	s.closeOnce.Do(func() {
		s.mu.Lock()
		s.closed = true
		s.mu.Unlock()
		if s.stop != nil {
			close(s.stop)
		} else {
			close(s.done)
		}
	})
	return nil
}

// Done returns a channel closed when the store has fully shut down.
func (s *Store) Done() <-chan struct{} { return s.done }

// Resolver resolves requests through the local store, falling back to an
// injected data source on miss. Source, clock and failure handling are
// all injectable functions; nothing here talks to a remote cache.
type Resolver struct {
	Keyer *Keyer
	Store *Store
	// Source loads the result for a request on cache miss (data source).
	Source func(ctx context.Context, r Request) (any, error)
	// OnError observes source failures (failure injection hook).
	OnError func(err error)
}

// Resolve returns the result for req, caching source results locally.
// Source errors are not cached.
func (r *Resolver) Resolve(ctx context.Context, req Request) (any, error) {
	key, err := r.Keyer.BuildKey(req)
	if err != nil {
		return nil, err
	}
	if v, ok := r.Store.Get(key); ok {
		return v, nil
	}
	v, err := r.Source(ctx, req)
	if err != nil {
		if r.OnError != nil {
			r.OnError(err)
		}
		return nil, err
	}
	if err := r.Store.Put(key, v); err != nil {
		return nil, err
	}
	return v, nil
}
