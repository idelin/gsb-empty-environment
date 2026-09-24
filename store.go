// Package tagcache maintains entries associated with versioned tags.
//
// Entries may be linked to multiple tags. Bumping a tag's version
// invalidates the entries loaded under the older version. All external
// effects (data source, wall clock, failure injection) are injectable
// functions; the package itself never touches a remote cache.
package tagcache

import (
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"
)

// ErrStaleGeneration is returned by LoadTag when the tag's generation
// changed while the load was in flight; the loaded data is discarded.
var ErrStaleGeneration = errors.New("tagcache: stale tag generation")

// Entry is a cached item associated with one or more tags.
type Entry struct {
	ID   string
	Data string
}

// LoadResult carries the entries produced by a DataSource together with a
// release function that frees any resources held by the load (buffers,
// handles, temp files). Release is guaranteed to be called exactly once by
// the Store, on success, on failure, and on stale-generation discard.
type LoadResult struct {
	Entries []Entry
	Release func()
}

// DataSource loads the entries for a tag at a given generation. It is
// injected by the caller, so tests can supply in-memory fakes.
type DataSource func(tag string, generation uint64) (LoadResult, error)

// Clock supplies the current time; injectable for deterministic tests.
type Clock func() time.Time

// FailPoint is invoked at named points inside Store operations. Returning a
// non-nil error aborts the operation at that point, letting tests inject
// failures without a real backend.
type FailPoint func(point string) error

// record is the internal per-entry state.
type record struct {
	entry    Entry
	tags     map[string]uint64 // tag -> generation the link was loaded under
	loadedAt time.Time
}

// Store keeps entries, a tag->entries reverse index, and per-tag
// generations. It is safe for concurrent use.
type Store struct {
	mu      sync.Mutex
	source  DataSource
	clock   Clock
	fail    FailPoint
	records map[string]*record
	byTag   map[string]map[string]struct{} // reverse index: tag -> entry IDs
	tagGen  map[string]uint64              // current generation per tag
}

// New builds a Store. nil clock/fail default to time.Now and "never fail".
func New(source DataSource, clock Clock, fail FailPoint) *Store {
	if clock == nil {
		clock = time.Now
	}
	if fail == nil {
		fail = func(string) error { return nil }
	}
	return &Store{
		source:  source,
		clock:   clock,
		fail:    fail,
		records: map[string]*record{},
		byTag:   map[string]map[string]struct{}{},
		tagGen:  map[string]uint64{},
	}
}

// LoadTag loads the entries for tag from the data source and commits them
// only if the tag generation is unchanged since the load started. Resources
// from the load are released in every outcome.
func (s *Store) LoadTag(tag string) error {
	s.mu.Lock()
	gen := s.tagGen[tag]
	s.mu.Unlock()

	res, err := s.source(tag, gen)
	if err != nil {
		return fmt.Errorf("tagcache: load tag %q: %w", tag, err)
	}
	if res.Release != nil {
		defer res.Release()
	}
	if err := s.fail("loadtag.commit"); err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.tagGen[tag] != gen {
		// Invalidated (or deleted) while loading: discard, keep nothing.
		return ErrStaleGeneration
	}
	for _, e := range res.Entries {
		s.putLocked(e, tag, gen)
	}
	return nil
}

func (s *Store) putLocked(e Entry, tag string, gen uint64) {
	rec, ok := s.records[e.ID]
	if !ok {
		rec = &record{tags: map[string]uint64{}}
		s.records[e.ID] = rec
	}
	rec.entry = e
	rec.tags[tag] = gen
	rec.loadedAt = s.clock()
	set := s.byTag[tag]
	if set == nil {
		set = map[string]struct{}{}
		s.byTag[tag] = set
	}
	set[e.ID] = struct{}{}
}

// InvalidateTag bumps the tag's generation and drops every entry link that
// was loaded under the old generation. Entries linked to other tags survive
// via those tags. In-flight loads for the old generation will be discarded
// by the generational check in LoadTag.
func (s *Store) InvalidateTag(tag string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.tagGen[tag]++
	s.unlinkTagLocked(tag)
}

// DeleteTag removes the tag entirely: its generation, its reverse index,
// and its links from entries. Links of the same entries to other tags are
// left untouched.
func (s *Store) DeleteTag(tag string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.tagGen, tag)
	s.unlinkTagLocked(tag)
}

// unlinkTagLocked removes only the given tag's relations.
func (s *Store) unlinkTagLocked(tag string) {
	for id := range s.byTag[tag] {
		if rec := s.records[id]; rec != nil {
			delete(rec.tags, tag)
			if len(rec.tags) == 0 {
				delete(s.records, id)
			}
		}
	}
	delete(s.byTag, tag)
}

// DeleteEntry removes an entry and cleans every reverse-index reference to
// it, so no tag keeps pointing at a dead entry.
func (s *Store) DeleteEntry(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rec := s.records[id]
	if rec == nil {
		return
	}
	for tag := range rec.tags {
		if set := s.byTag[tag]; set != nil {
			delete(set, id)
			if len(set) == 0 {
				delete(s.byTag, tag)
			}
		}
	}
	delete(s.records, id)
}

// EntriesForTag returns the entries currently linked to tag, sorted by ID.
func (s *Store) EntriesForTag(tag string) []Entry {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []Entry
	for id := range s.byTag[tag] {
		if rec := s.records[id]; rec != nil {
			out = append(out, rec.entry)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// TagsOf returns the tags currently linked to an entry, sorted.
func (s *Store) TagsOf(id string) []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	rec := s.records[id]
	if rec == nil {
		return nil
	}
	out := make([]string, 0, len(rec.tags))
	for tag := range rec.tags {
		out = append(out, tag)
	}
	sort.Strings(out)
	return out
}

// BillableEntries returns each stored entry exactly once, no matter how
// many tags it is linked to: an entry with two tags is never charged twice.
func (s *Store) BillableEntries() []Entry {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Entry, 0, len(s.records))
	for _, rec := range s.records {
		out = append(out, rec.entry)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// LoadedAt reports when the entry's link was last (re)loaded, using the
// injected clock.
func (s *Store) LoadedAt(id string) (time.Time, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if rec := s.records[id]; rec != nil {
		return rec.loadedAt, true
	}
	return time.Time{}, false
}

// CheckInvariants verifies internal consistency: every reverse-index link
// resolves to a live record, and every record's tag links were loaded under
// the tag's current generation (no stale valid entries after races).
func (s *Store) CheckInvariants() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for tag, set := range s.byTag {
		for id := range set {
			rec := s.records[id]
			if rec == nil {
				return fmt.Errorf("tagcache: reverse index %q -> %q dangles", tag, id)
			}
			gen, ok := rec.tags[tag]
			if !ok {
				return fmt.Errorf("tagcache: record %q missing back-link to %q", id, tag)
			}
			if gen != s.tagGen[tag] {
				return fmt.Errorf("tagcache: record %q linked to %q at stale generation %d (current %d)",
					id, tag, gen, s.tagGen[tag])
			}
		}
	}
	for id, rec := range s.records {
		if len(rec.tags) == 0 {
			return fmt.Errorf("tagcache: record %q has no tags", id)
		}
		for tag := range rec.tags {
			if _, ok := s.byTag[tag][id]; !ok {
				return fmt.Errorf("tagcache: record %q -> %q missing from reverse index", id, tag)
			}
		}
	}
	return nil
}
