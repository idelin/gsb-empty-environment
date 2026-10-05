// Package objindex implements an in-memory object index with tombstone
// deletions and snapshot-based prefix pagination.
//
// The index assigns a monotonically increasing version to every mutation.
// A listing is bound to the snapshot version captured by its cursor, so
// concurrent mutations never affect an in-flight listing. Every create of a
// key produces a new generation; a deleted and recreated key is therefore a
// distinct object and is never conflated with its predecessor.
package objindex

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"sync"
)

var (
	// ErrSnapshotExpired is returned when the cursor's snapshot version is
	// older than the retained version window.
	ErrSnapshotExpired = errors.New("objindex: snapshot expired")
	// ErrSnapshotFuture is returned when the cursor's snapshot version is
	// newer than the index's current version.
	ErrSnapshotFuture = errors.New("objindex: snapshot version is in the future")
	// ErrInvalidLimit is returned for non-positive page limits.
	ErrInvalidLimit = errors.New("objindex: limit must be positive")
)

// Object is a single visible index entry.
type Object struct {
	Key        string
	Value      []byte
	Generation uint64
}

// Cursor binds a listing to a snapshot version and the last examined
// position. The zero Cursor starts a new listing at the current version.
type Cursor struct {
	Snapshot uint64
	LastKey  string
	LastGen  uint64
}

// Zero reports whether c is the zero (fresh) cursor.
func (c Cursor) Zero() bool {
	return c.Snapshot == 0 && c.LastKey == "" && c.LastGen == 0
}

// Encode serializes the cursor into an opaque token.
func (c Cursor) Encode() string {
	b, err := json.Marshal(c)
	if err != nil {
		return ""
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

// DecodeCursor parses a token produced by Cursor.Encode.
func DecodeCursor(token string) (Cursor, error) {
	b, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil {
		return Cursor{}, err
	}
	var c Cursor
	if err := json.Unmarshal(b, &c); err != nil {
		return Cursor{}, err
	}
	return c, nil
}

// Page is one page of a listing. Cursor always advances past every examined
// entry, including filtered tombstones, so an empty page still makes
// progress. Done reports that the snapshot was exhausted.
type Page struct {
	Objects []Object
	Cursor  Cursor
	Done    bool
}

type entry struct {
	gen     uint64
	value   []byte
	created uint64
	deleted uint64
}

func (e *entry) tombstonedAt(snap uint64) bool {
	return e.deleted != 0 && e.deleted <= snap
}

// Index is a concurrent-safe in-memory object index.
type Index struct {
	mu      sync.RWMutex
	version uint64
	retain  uint64
	maxScan int
	gens    map[string]uint64
	byKey   map[string][]*entry
	keys    []string
}

// Option customizes an Index.
type Option func(*Index)

// WithRetainVersions sets how many recent versions remain listable; older
// snapshot cursors fail with ErrSnapshotExpired.
func WithRetainVersions(n uint64) Option {
	return func(ix *Index) {
		if n > 0 {
			ix.retain = n
		}
	}
}

// WithMaxScan bounds how many raw entries a single List call may examine.
// Because the limit counts returned (post-filter) items, a page full of
// tombstones would otherwise scan unboundedly; the cap yields an empty page
// with an advanced cursor instead.
func WithMaxScan(n int) Option {
	return func(ix *Index) {
		if n > 0 {
			ix.maxScan = n
		}
	}
}

// New creates an empty Index.
func New(opts ...Option) *Index {
	ix := &Index{
		retain:  64,
		maxScan: 256,
		gens:    make(map[string]uint64),
		byKey:   make(map[string][]*entry),
	}
	for _, opt := range opts {
		opt(ix)
	}
	return ix
}

// Version returns the current mutation version.
func (ix *Index) Version() uint64 {
	ix.mu.RLock()
	defer ix.mu.RUnlock()
	return ix.version
}

// Add creates or replaces key, returning its new generation.
func (ix *Index) Add(key string, value []byte) uint64 {
	ix.mu.Lock()
	defer ix.mu.Unlock()
	ix.version++
	gen := ix.gens[key] + 1
	ix.gens[key] = gen
	e := &entry{gen: gen, value: append([]byte(nil), value...), created: ix.version}
	if _, ok := ix.byKey[key]; !ok {
		ix.insertKey(key)
	}
	ix.byKey[key] = append(ix.byKey[key], e)
	ix.compactLocked()
	return gen
}

// Delete tombstones key. It reports whether a live object was removed.
func (ix *Index) Delete(key string) bool {
	ix.mu.Lock()
	defer ix.mu.Unlock()
	list := ix.byKey[key]
	if len(list) == 0 {
		return false
	}
	last := list[len(list)-1]
	if last.deleted != 0 {
		return false
	}
	ix.version++
	last.deleted = ix.version
	ix.compactLocked()
	return true
}

// List returns up to limit visible objects whose key starts with prefix,
// in ascending key order, as of the cursor's snapshot version. The limit
// counts only returned objects; filtered tombstones never consume it, but
// they do advance the returned cursor so empty pages still make progress.
func (ix *Index) List(prefix string, cur Cursor, limit int) (Page, error) {
	if limit < 1 {
		return Page{}, ErrInvalidLimit
	}
	ix.mu.RLock()
	defer ix.mu.RUnlock()

	snap := cur.Snapshot
	if cur.Zero() {
		snap = ix.version
	}
	if snap > ix.version {
		return Page{}, ErrSnapshotFuture
	}
	if snap < ix.minValidLocked() {
		return Page{}, ErrSnapshotExpired
	}

	page := Page{Done: true, Cursor: Cursor{Snapshot: snap, LastKey: cur.LastKey, LastGen: cur.LastGen}}
	from := prefix
	if cur.LastKey > from {
		from = cur.LastKey
	}
	scanned := 0
	full := false
	for i := sort.SearchStrings(ix.keys, from); i < len(ix.keys); i++ {
		key := ix.keys[i]
		if !strings.HasPrefix(key, prefix) {
			break
		}
		vis := ix.latestVisibleLocked(key, snap)
		if vis == nil {
			continue
		}
		if key == cur.LastKey && vis.gen <= cur.LastGen {
			continue
		}
		if full || scanned == ix.maxScan {
			page.Done = false
			break
		}
		scanned++
		page.Cursor.LastKey = key
		page.Cursor.LastGen = vis.gen
		if vis.tombstonedAt(snap) {
			continue
		}
		page.Objects = append(page.Objects, Object{
			Key:        key,
			Value:      append([]byte(nil), vis.value...),
			Generation: vis.gen,
		})
		if len(page.Objects) == limit {
			full = true
		}
	}
	return page, nil
}

// Freeze returns the current version and a frozen sorted-map view of all
// live objects at that version. It serves as an independent reference for
// verifying paginated listings.
func (ix *Index) Freeze() (uint64, map[string]Object) {
	ix.mu.RLock()
	defer ix.mu.RUnlock()
	snap := ix.version
	out := make(map[string]Object, len(ix.keys))
	for _, key := range ix.keys {
		vis := ix.latestVisibleLocked(key, snap)
		if vis == nil || vis.tombstonedAt(snap) {
			continue
		}
		out[key] = Object{
			Key:        key,
			Value:      append([]byte(nil), vis.value...),
			Generation: vis.gen,
		}
	}
	return snap, out
}

func (ix *Index) latestVisibleLocked(key string, snap uint64) *entry {
	list := ix.byKey[key]
	for j := len(list) - 1; j >= 0; j-- {
		if list[j].created <= snap {
			return list[j]
		}
	}
	return nil
}

func (ix *Index) minValidLocked() uint64 {
	if ix.version >= ix.retain {
		return ix.version - ix.retain + 1
	}
	return 0
}

func (ix *Index) compactLocked() {
	min := ix.minValidLocked()
	if min == 0 {
		return
	}
	for _, key := range ix.keys {
		list := ix.byKey[key]
		keep := list[:0]
		for i, e := range list {
			if i+1 < len(list) {
				if list[i+1].created > min {
					keep = append(keep, e)
				}
			} else if e.deleted == 0 || e.deleted > min {
				keep = append(keep, e)
			}
		}
		if len(keep) == 0 {
			delete(ix.byKey, key)
			ix.removeKey(key)
		} else {
			ix.byKey[key] = keep
		}
	}
}

func (ix *Index) insertKey(key string) {
	i := sort.SearchStrings(ix.keys, key)
	ix.keys = append(ix.keys, "")
	copy(ix.keys[i+1:], ix.keys[i:])
	ix.keys[i] = key
}

func (ix *Index) removeKey(key string) {
	i := sort.SearchStrings(ix.keys, key)
	if i < len(ix.keys) && ix.keys[i] == key {
		ix.keys = append(ix.keys[:i], ix.keys[i+1:]...)
	}
}
