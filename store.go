// Package tagcache implements an in-memory, tag-versioned cache.
//
// Entries may carry multiple tags. Bumping a tag's generation invalidates
// every related entry. Deleting an entry cleans up the reverse index.
// Loads are generation-checked so a tag change racing with a load never
// leaves a stale valid entry behind. Data source, clock and failure
// handling are injectable; there is no remote cache dependency.
package tagcache

import (
	"context"
	"errors"
	"sync"
	"time"
)

// ErrGenerationConflict is returned when tag generations keep changing
// during load and the retry budget is exhausted.
var ErrGenerationConflict = errors.New("tagcache: tag generation changed during load")

// Source loads a value for a key. Injectable.
type Source func(ctx context.Context, key string) (any, error)

// Clock returns the current time. Injectable.
type Clock func() time.Time

// FailureHandler observes load failures. Injectable.
type FailureHandler func(key string, err error)

// Options configures a Store.
type Options struct {
	Source    Source
	Clock     Clock
	OnFailure FailureHandler
	// TTL controls entry freshness; zero means no expiry.
	TTL time.Duration
	// MaxLoadAttempts bounds retries when generations change mid-load.
	// Values < 1 are treated as 1.
	MaxLoadAttempts int
}

type entry struct {
	value     any
	tags      map[string]uint64 // tag -> generation captured at commit
	expiresAt time.Time         // zero when TTL is disabled
}

// Store is a goroutine-safe tag-versioned cache.
type Store struct {
	mu          sync.Mutex
	source      Source
	clock       Clock
	onFailure   FailureHandler
	ttl         time.Duration
	maxAttempts int

	entries  map[string]*entry
	tagIndex map[string]map[string]struct{} // tag -> set of keys (reverse index)
	tagGen   map[string]uint64              // tag -> current generation

	billedLoads int // one bill per entry load, regardless of tag count
}

// New builds a Store. Source is required.
func New(opts Options) *Store {
	if opts.Clock == nil {
		opts.Clock = time.Now
	}
	if opts.MaxLoadAttempts < 1 {
		opts.MaxLoadAttempts = 1
	}
	return &Store{
		source:      opts.Source,
		clock:       opts.Clock,
		onFailure:   opts.OnFailure,
		ttl:         opts.TTL,
		maxAttempts: opts.MaxLoadAttempts,
		entries:     make(map[string]*entry),
		tagIndex:    make(map[string]map[string]struct{}),
		tagGen:      make(map[string]uint64),
	}
}

// Stats reports resource usage for release verification.
type Stats struct {
	Entries     int
	Tags        int // tags still holding a reverse index
	BilledLoads int
}

// Stats returns a snapshot of internal resource usage.
func (s *Store) Stats() Stats {
	s.mu.Lock()
	defer s.mu.Unlock()
	return Stats{Entries: len(s.entries), Tags: len(s.tagIndex), BilledLoads: s.billedLoads}
}

// Get returns the cached value for key, loading it via Source on miss.
// tags associate the entry with tag generations; duplicates are ignored
// and never cause double billing.
func (s *Store) Get(ctx context.Context, key string, tags ...string) (any, error) {
	uniq := dedup(tags)
	if v, ok := s.lookup(key); ok {
		return v, nil
	}
	var err error
	for attempt := 0; attempt < s.maxAttempts; attempt++ {
		var gens map[string]uint64
		s.mu.Lock()
		gens = s.snapshotGensLocked(uniq)
		s.mu.Unlock()

		v, srcErr := s.source(ctx, key)
		if srcErr != nil {
			err = srcErr
			break
		}
		if s.commit(key, uniq, gens, v) {
			return v, nil
		}
		err = ErrGenerationConflict
	}
	if s.onFailure != nil {
		s.onFailure(key, err)
	}
	return nil, err
}

// lookup returns a fresh, generation-valid entry if present.
func (s *Store) lookup(key string) (any, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.entries[key]
	if !ok {
		return nil, false
	}
	if s.expiredLocked(e) || !s.gensValidLocked(e.tags) {
		s.removeEntryLocked(key, e)
		return nil, false
	}
	return e.value, true
}

// commit stores v only if no tag generation changed since the load began.
// Returns false when the load raced with an invalidation; nothing is stored.
func (s *Store) commit(key string, tags []string, gens map[string]uint64, v any) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.gensValidLocked(gens) {
		return false
	}
	if old, ok := s.entries[key]; ok {
		s.removeEntryLocked(key, old)
	}
	e := &entry{value: v, tags: make(map[string]uint64, len(tags))}
	if s.ttl > 0 {
		e.expiresAt = s.clock().Add(s.ttl)
	}
	for _, tag := range tags {
		e.tags[tag] = gens[tag]
		set := s.tagIndex[tag]
		if set == nil {
			set = make(map[string]struct{})
			s.tagIndex[tag] = set
		}
		set[key] = struct{}{}
	}
	s.entries[key] = e
	s.billedLoads++
	return true
}

// Delete removes an entry and cleans its reverse-index relations,
// releasing empty index sets.
func (s *Store) Delete(key string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if e, ok := s.entries[key]; ok {
		s.removeEntryLocked(key, e)
	}
}

// InvalidateTag bumps the tag generation and evicts every related entry,
// cleaning each evicted entry's relations under all other tags too.
func (s *Store) InvalidateTag(tag string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.tagGen[tag]++
	for key := range s.tagIndex[tag] {
		if e, ok := s.entries[key]; ok {
			s.removeEntryLocked(key, e)
		}
	}
	delete(s.tagIndex, tag)
}

// DeleteTag detaches tag from all entries without touching their other
// tag relations, then releases the tag's index and generation state.
func (s *Store) DeleteTag(tag string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for key := range s.tagIndex[tag] {
		if e, ok := s.entries[key]; ok {
			delete(e.tags, tag)
		}
	}
	delete(s.tagIndex, tag)
	delete(s.tagGen, tag)
}

// removeEntryLocked deletes the entry and every reverse-index relation,
// releasing empty sets so no memory is retained.
func (s *Store) removeEntryLocked(key string, e *entry) {
	for tag := range e.tags {
		if set, ok := s.tagIndex[tag]; ok {
			delete(set, key)
			if len(set) == 0 {
				delete(s.tagIndex, tag)
			}
		}
	}
	delete(s.entries, key)
}

func (s *Store) snapshotGensLocked(tags []string) map[string]uint64 {
	gens := make(map[string]uint64, len(tags))
	for _, tag := range tags {
		gens[tag] = s.tagGen[tag]
	}
	return gens
}

func (s *Store) gensValidLocked(gens map[string]uint64) bool {
	for tag, gen := range gens {
		if s.tagGen[tag] != gen {
			return false
		}
	}
	return true
}

func (s *Store) expiredLocked(e *entry) bool {
	return !e.expiresAt.IsZero() && !s.clock().Before(e.expiresAt)
}

func dedup(tags []string) []string {
	seen := make(map[string]struct{}, len(tags))
	out := tags[:0:0]
	for _, tag := range tags {
		if _, ok := seen[tag]; ok {
			continue
		}
		seen[tag] = struct{}{}
		out = append(out, tag)
	}
	return out
}
