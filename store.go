package canonkey

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"sort"
)

// ConflictPolicy decides what happens when a key is added whose content
// differs from the content already stored under that key. There is no
// "overwrite" policy: existing content is never silently replaced.
type ConflictPolicy int

const (
	// ConflictReject returns ErrConflict and leaves the store untouched.
	ConflictReject ConflictPolicy = iota
	// ConflictIsolate keeps the original entry and quarantines the
	// conflicting content in a per-key isolation bucket, retrievable via
	// Isolated.
	ConflictIsolate
)

// ErrConflict is returned by Add when the key exists with different content
// and the store's policy is ConflictReject.
var ErrConflict = errors.New("canonkey: key conflict, content differs")

// Outcome describes what Add did with one (key, content) pair.
type Outcome int

const (
	// Inserted means the key was new and the content was stored.
	Inserted Outcome = iota
	// Duplicate means the key existed with identical content; nothing
	// changed (dedup hit).
	Duplicate
	// Isolated means the key existed with different content and the new
	// content was quarantined per ConflictIsolate.
	Isolated
)

func (o Outcome) String() string {
	switch o {
	case Inserted:
		return "inserted"
	case Duplicate:
		return "duplicate"
	case Isolated:
		return "isolated"
	default:
		return "unknown"
	}
}

type entry struct {
	hash    [sha256.Size]byte
	content []byte
}

// Store is a deduplicating key/content registry with explicit conflict
// handling. It is not safe for concurrent use.
type Store struct {
	policy ConflictPolicy
	// entries holds exactly one canonical content per key; it is never
	// overwritten in place.
	entries map[Key]entry
	// isolated holds conflicting contents per key, in arrival order.
	isolated map[Key][][]byte
}

// NewStore creates an empty store using the given conflict policy.
func NewStore(policy ConflictPolicy) *Store {
	return &Store{
		policy:   policy,
		entries:  make(map[Key]entry),
		isolated: make(map[Key][][]byte),
	}
}

// Policy returns the store's conflict policy.
func (s *Store) Policy() ConflictPolicy { return s.policy }

// Add registers content under key.
//
//   - new key            -> Inserted
//   - same key+content   -> Duplicate (no-op dedup)
//   - same key, new content -> rejected (ErrConflict) or Isolated,
//     depending on policy; the stored content is never overwritten.
func (s *Store) Add(key Key, content []byte) (Outcome, error) {
	sum := sha256.Sum256(content)
	if cur, ok := s.entries[key]; ok {
		if cur.hash == sum {
			return Duplicate, nil
		}
		switch s.policy {
		case ConflictReject:
			return Isolated, ErrConflict
		case ConflictIsolate:
			cp := make([]byte, len(content))
			copy(cp, content)
			s.isolated[key] = append(s.isolated[key], cp)
			return Isolated, nil
		default:
			panic("canonkey: unknown conflict policy")
		}
	}
	cp := make([]byte, len(content))
	copy(cp, content)
	s.entries[key] = entry{hash: sum, content: cp}
	return Inserted, nil
}

// Get returns the canonical content stored under key.
func (s *Store) Get(key Key) ([]byte, bool) {
	e, ok := s.entries[key]
	if !ok {
		return nil, false
	}
	cp := make([]byte, len(e.content))
	copy(cp, e.content)
	return cp, true
}

// Isolated returns the quarantined conflicting contents for key, in the
// order they were rejected from the canonical slot.
func (s *Store) Isolated(key Key) [][]byte {
	list := s.isolated[key]
	out := make([][]byte, len(list))
	for i, c := range list {
		cp := make([]byte, len(c))
		copy(cp, c)
		out[i] = cp
	}
	return out
}

// Len returns the number of canonical (deduplicated) entries.
func (s *Store) Len() int { return len(s.entries) }

// IsolatedLen returns the total number of quarantined conflicting contents.
func (s *Store) IsolatedLen() int {
	n := 0
	for _, list := range s.isolated {
		n += len(list)
	}
	return n
}

// Keys returns the canonical keys in sorted order, for deterministic
// iteration and checkpointing.
func (s *Store) Keys() []Key {
	keys := make([]Key, 0, len(s.entries))
	for k := range s.entries {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i] < keys[j] })
	return keys
}

// contentHash is the hex digest used in checkpoints and tests.
func contentHash(content []byte) string {
	sum := sha256.Sum256(content)
	return hex.EncodeToString(sum[:])
}
