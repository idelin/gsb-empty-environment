// Package dedup implements a content-addressed block store with
// deduplication. Blocks are bucketed by a cheap weak digest, then
// confirmed by strong digest, length and a full content comparison.
//
// If two different contents ever produce the same strong digest (a
// hash-collision fault), the store isolates them as separate variants
// of the same digest and keeps both copies; nothing is overwritten.
//
// Reference counts are derived from named object roots and are mutated
// only by committing a transaction. A failed commit applies nothing.
// RebuildRefCounts recomputes every count from the roots alone, so
// drift caused by bugs or corruption can be detected and repaired.
package dedup

import (
	"bytes"
	"errors"
	"fmt"
	"sort"
	"sync"
)

// Sentinel errors. Because this package builds with Go < 1.13 they are
// carried inside *Error; compare with AsError(err).Err == ErrX.
var (
	ErrTxClosed         = errors.New("dedup: transaction already closed")
	ErrUnknownBlock     = errors.New("dedup: unknown block")
	ErrNegativeRefCount = errors.New("dedup: reference count would go negative")
)

// Error describes a failed store operation.
type Error struct {
	Op  string
	Key BlockKey
	Err error
}

func (e *Error) Error() string {
	return fmt.Sprintf("%s: block %s: %v", e.Op, e.Key, e.Err)
}

// AsError unwraps err into *Error, or returns nil.
func AsError(err error) *Error {
	if e, ok := err.(*Error); ok {
		return e
	}
	return nil
}

// BlockKey identifies one immutable block variant. Variant disambiguates
// contents that share a strong digest (collision fault isolation).
type BlockKey struct {
	Digest  StrongDigest
	Variant int
}

func (k BlockKey) String() string {
	return fmt.Sprintf("%x#%d", k.Digest[:8], k.Variant)
}

// Options allows injecting hash functions, mainly to inject faults
// (e.g. a strong hash that always collides) in tests.
type Options struct {
	WeakHash   func([]byte) WeakDigest
	StrongHash func([]byte) StrongDigest
}

type variant struct {
	data     []byte
	weak     WeakDigest
	refCount int
}

// family holds every variant stored under one strong digest. The slice
// is append-only so that a BlockKey stays valid for the life of its
// block; garbage-collected slots become nil holes.
type family struct {
	variants []*variant
}

// Store is a concurrency-safe deduplicating block store.
type Store struct {
	mu         sync.Mutex
	weakHash   func([]byte) WeakDigest
	strongHash func([]byte) StrongDigest
	buckets    map[WeakDigest]map[StrongDigest]struct{}
	families   map[StrongDigest]*family
	objects    map[string]map[BlockKey]int // root name -> referenced keys (multiset)
}

// NewStore creates an empty store. opts may be nil for defaults.
func NewStore(opts *Options) *Store {
	s := &Store{
		weakHash:   DefaultWeakHash,
		strongHash: DefaultStrongHash,
		buckets:    make(map[WeakDigest]map[StrongDigest]struct{}),
		families:   make(map[StrongDigest]*family),
		objects:    make(map[string]map[BlockKey]int),
	}
	if opts != nil {
		if opts.WeakHash != nil {
			s.weakHash = opts.WeakHash
		}
		if opts.StrongHash != nil {
			s.strongHash = opts.StrongHash
		}
	}
	return s
}

func (s *Store) registerWeak(weak WeakDigest, digest StrongDigest) {
	set := s.buckets[weak]
	if set == nil {
		set = make(map[StrongDigest]struct{})
		s.buckets[weak] = set
	}
	set[digest] = struct{}{}
}

func (s *Store) unregisterWeak(weak WeakDigest, digest StrongDigest) {
	set := s.buckets[weak]
	if set == nil {
		return
	}
	delete(set, digest)
	if len(set) == 0 {
		delete(s.buckets, weak)
	}
}

// findLocked returns the key of an existing committed block whose
// content equals data, confirmed by strong digest, length and bytes.
func (s *Store) findLocked(weak WeakDigest, strong StrongDigest, data []byte) (BlockKey, bool) {
	if _, ok := s.buckets[weak][strong]; !ok {
		return BlockKey{}, false
	}
	fam := s.families[strong]
	if fam == nil {
		return BlockKey{}, false
	}
	for i, v := range fam.variants {
		if v != nil && len(v.data) == len(data) && bytes.Equal(v.data, data) {
			return BlockKey{Digest: strong, Variant: i}, true
		}
	}
	return BlockKey{}, false
}

// Get returns a copy of the block content.
func (s *Store) Get(key BlockKey) ([]byte, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	v := s.variantLocked(key)
	if v == nil {
		return nil, false
	}
	out := make([]byte, len(v.data))
	copy(out, v.data)
	return out, true
}

// RefCount reports the current reference count of a block.
func (s *Store) RefCount(key BlockKey) (int, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	v := s.variantLocked(key)
	if v == nil {
		return 0, false
	}
	return v.refCount, true
}

// Object returns the keys referenced by a root, sorted for determinism.
func (s *Store) Object(name string) ([]BlockKey, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	keys, ok := s.objects[name]
	if !ok {
		return nil, false
	}
	out := make([]BlockKey, 0, len(keys))
	for k, n := range keys {
		for i := 0; i < n; i++ {
			out = append(out, k)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Digest != out[j].Digest {
			return bytes.Compare(out[i].Digest[:], out[j].Digest[:]) < 0
		}
		return out[i].Variant < out[j].Variant
	})
	return out, true
}

func (s *Store) variantLocked(key BlockKey) *variant {
	fam := s.families[key.Digest]
	if fam == nil || key.Variant < 0 || key.Variant >= len(fam.variants) {
		return nil
	}
	return fam.variants[key.Variant]
}

// GC deletes every block whose reference count is zero and returns the
// number of blocks removed. It never touches referenced blocks, so a
// concurrent Put that re-references the same content always wins.
func (s *Store) GC() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	removed := 0
	for digest, fam := range s.families {
		for i, v := range fam.variants {
			if v == nil || v.refCount != 0 {
				continue
			}
			shared := false
			for j, u := range fam.variants {
				if j != i && u != nil && u.weak == v.weak {
					shared = true
					break
				}
			}
			if !shared {
				s.unregisterWeak(v.weak, digest)
			}
			fam.variants[i] = nil
			removed++
		}
		empty := true
		for _, v := range fam.variants {
			if v != nil {
				empty = false
				break
			}
		}
		if empty {
			delete(s.families, digest)
		}
	}
	return removed
}

// Correction describes one reference count repaired by RebuildRefCounts.
// OldCount is -1 when a root references a block that does not exist;
// such dangling references are reported but cannot be repaired.
type Correction struct {
	Key      BlockKey
	OldCount int
	NewCount int
}

// RebuildRefCounts independently recomputes every reference count from
// the object roots, repairs any drift, and reports the corrections.
// An empty result means the stored counts already matched the roots.
func (s *Store) RebuildRefCounts() []Correction {
	s.mu.Lock()
	defer s.mu.Unlock()
	want := make(map[BlockKey]int)
	for _, keys := range s.objects {
		for k, n := range keys {
			want[k] += n
		}
	}
	var out []Correction
	for digest, fam := range s.families {
		for i, v := range fam.variants {
			if v == nil {
				continue
			}
			key := BlockKey{Digest: digest, Variant: i}
			w := want[key]
			delete(want, key)
			if v.refCount != w {
				out = append(out, Correction{Key: key, OldCount: v.refCount, NewCount: w})
				v.refCount = w
			}
		}
	}
	for k, w := range want {
		out = append(out, Correction{Key: k, OldCount: -1, NewCount: w})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Key.Digest != out[j].Key.Digest {
			return bytes.Compare(out[i].Key.Digest[:], out[j].Key.Digest[:]) < 0
		}
		return out[i].Key.Variant < out[j].Key.Variant
	})
	return out
}
