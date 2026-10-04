// Package objectindex implements an in-memory object index with
// snapshot-pinned prefix pagination.
//
// The store keeps per-key version history so that a list cursor, which
// binds a snapshot version and the last examined key, sees a stable view
// of the key space even while other goroutines put or delete objects.
// Deletes are tombstones: recreating a key always assigns a fresh object
// ID, so a deleted-then-recreated key is never confused with the old
// object.
package objectindex

import (
	"errors"
	"sort"
	"sync"
)

// Sentinel errors returned by List.
var (
	// ErrSnapshotExpired is returned when the cursor's snapshot version
	// has been compacted away and the listing cannot be resumed.
	ErrSnapshotExpired = errors.New("objectindex: snapshot expired")
	// ErrCursorVersion is returned when the cursor carries a version
	// that the store never produced (e.g. from the future).
	ErrCursorVersion = errors.New("objectindex: invalid cursor version")
	// ErrInvalidLimit is returned when a non-positive limit is passed.
	ErrInvalidLimit = errors.New("objectindex: limit must be positive")
)

// DefaultRetention is the number of recent versions kept when Options
// does not specify a retention window.
const DefaultRetention = 64

// DefaultScanFactor multiplies the page limit to derive the per-call
// scan budget (number of candidate keys examined). The budget bounds
// the work done by one List call when the prefix filter matches few or
// none of the scanned keys.
const DefaultScanFactor = 8

// Options configures a Store.
type Options struct {
	// Retention is the number of recent versions whose snapshots remain
	// listable. Older snapshots report ErrSnapshotExpired.
	Retention uint64
	// ScanFactor derives the scan budget: budget = limit * ScanFactor.
	ScanFactor int
}

func (o Options) withDefaults() Options {
	if o.Retention == 0 {
		o.Retention = DefaultRetention
	}
	if o.ScanFactor <= 0 {
		o.ScanFactor = DefaultScanFactor
	}
	return o
}

// Item is a single listed object.
type Item struct {
	Key   string
	ID    uint64 // unique per object incarnation; never reused
	Value []byte
}

// Cursor marks a position in a paginated listing. The zero Cursor
// starts a new listing pinned to the store's current version.
type Cursor struct {
	Version uint64 // snapshot version the listing is pinned to
	LastKey string // last key examined (not necessarily returned)
	Done    bool   // no more keys can match
}

// Page is the result of one List call.
type Page struct {
	Items  []Item
	Cursor Cursor // pass back to List for the next page
}

// record is one historical state of a key.
type record struct {
	id      uint64
	value   []byte
	deleted bool // tombstone
	version uint64
}

// Store is an in-memory object index. It is safe for concurrent use.
type Store struct {
	mu    sync.RWMutex
	opts  Options
	ver   uint64
	seq   uint64
	byKey map[string][]record // per-key history, ascending version
}

// NewStore creates an empty Store.
func NewStore(opts Options) *Store {
	return &Store{
		opts:  opts.withDefaults(),
		ver:   1, // version 0 is reserved to mean "no snapshot pinned yet"
		byKey: make(map[string][]record),
	}
}

// Version reports the store's current version.
func (s *Store) Version() uint64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.ver
}

// Put adds or replaces the object at key and returns its new object ID.
// Every Put assigns a fresh, never-reused ID, so an object recreated
// after a tombstone delete is distinguishable from its predecessor.
// Keys must be non-empty; Put of an empty key is a no-op returning 0.
func (s *Store) Put(key string, value []byte) uint64 {
	if key == "" {
		return 0
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ver++
	s.seq++
	cp := make([]byte, len(value))
	copy(cp, value)
	s.byKey[key] = append(s.byKey[key], record{id: s.seq, value: cp, version: s.ver})
	s.compactLocked()
	return s.seq
}

// Delete tombstones key. It reports whether a live object existed.
func (s *Store) Delete(key string) bool {
	if key == "" {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	hist := s.byKey[key]
	if len(hist) == 0 || hist[len(hist)-1].deleted {
		return false
	}
	s.ver++
	s.seq++
	s.byKey[key] = append(hist, record{id: s.seq, deleted: true, version: s.ver})
	s.compactLocked()
	return true
}

// Get returns the live object at key, if any.
func (s *Store) Get(key string) (Item, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	hist := s.byKey[key]
	if len(hist) == 0 {
		return Item{}, false
	}
	r := hist[len(hist)-1]
	if r.deleted {
		return Item{}, false
	}
	return Item{Key: key, ID: r.id, Value: append([]byte(nil), r.value...)}, true
}

// minVersionLocked is the oldest snapshot version still servable.
func (s *Store) minVersionLocked() uint64 {
	if s.ver+1 > s.opts.Retention {
		return s.ver + 1 - s.opts.Retention
	}
	return 0
}

// compactLocked drops history that no retained snapshot can observe.
// For each key it keeps every record newer than the retention floor,
// plus the newest record at or below the floor if it is live (a
// tombstone at the floor can simply be forgotten).
func (s *Store) compactLocked() {
	floor := s.minVersionLocked()
	if floor == 0 {
		return
	}
	for key, hist := range s.byKey {
		cut := 0
		for cut < len(hist) && hist[cut].version <= floor {
			cut++
		}
		if cut == 0 {
			continue
		}
		keep := hist[cut:]
		if !hist[cut-1].deleted {
			keep = append([]record{hist[cut-1]}, keep...)
		}
		if len(keep) == 0 {
			delete(s.byKey, key)
		} else {
			s.byKey[key] = keep
		}
	}
}

// prefixEnd returns the smallest key greater than every key with the
// given prefix, and whether such a key exists.
func prefixEnd(prefix string) (string, bool) {
	b := []byte(prefix)
	for i := len(b) - 1; i >= 0; i-- {
		if b[i] != 0xff {
			b[i]++
			return string(b[:i+1]), true
		}
	}
	return "", false
}

// List returns the next page of live objects whose key has the given
// prefix, as of the cursor's snapshot version.
//
// The page limit counts returned items only. Scanning still advances
// over keys skipped by the prefix filter, so a call may return an empty
// page with an advanced cursor when the filter matches nothing in the
// scanned window; callers should keep paging until Cursor.Done.
func (s *Store) List(prefix string, cur Cursor, limit int) (Page, error) {
	if limit <= 0 {
		return Page{}, ErrInvalidLimit
	}
	s.mu.RLock()
	defer s.mu.RUnlock()

	if cur.Version == 0 {
		cur.Version = s.ver
		cur.LastKey = ""
		cur.Done = false
	} else {
		if cur.Version > s.ver {
			return Page{}, ErrCursorVersion
		}
		if cur.Version < s.minVersionLocked() {
			return Page{}, ErrSnapshotExpired
		}
	}
	if cur.Done {
		return Page{Cursor: cur}, nil
	}

	// Materialize the snapshot view as sorted key/record pairs.
	type kv struct {
		key string
		rec record
	}
	pairs := make([]kv, 0, len(s.byKey))
	for key, hist := range s.byKey {
		i := sort.Search(len(hist), func(i int) bool { return hist[i].version > cur.Version })
		if i == 0 {
			continue
		}
		r := hist[i-1]
		if r.deleted {
			continue
		}
		pairs = append(pairs, kv{key: key, rec: r})
	}
	sort.Slice(pairs, func(i, j int) bool { return pairs[i].key < pairs[j].key })

	start := sort.Search(len(pairs), func(i int) bool { return pairs[i].key > cur.LastKey })

	end, hasEnd := prefixEnd(prefix)
	budget := limit * s.opts.ScanFactor
	page := Page{Cursor: Cursor{Version: cur.Version, LastKey: cur.LastKey}}
	scanned := 0
	for i := start; i < len(pairs); i++ {
		key := pairs[i].key
		if hasEnd && key >= end {
			// Past the prefix range: nothing further can match.
			page.Cursor.Done = true
			return page, nil
		}
		page.Cursor.LastKey = key
		scanned++
		if len(key) >= len(prefix) && key[:len(prefix)] == prefix {
			page.Items = append(page.Items, Item{
				Key:   key,
				ID:    pairs[i].rec.id,
				Value: append([]byte(nil), pairs[i].rec.value...),
			})
			if len(page.Items) == limit {
				return page, nil
			}
		}
		if scanned == budget {
			// Budget exhausted: return what we have (possibly an
			// empty page) with the cursor advanced to the last
			// examined key.
			return page, nil
		}
	}
	page.Cursor.Done = true
	return page, nil
}
