package speckey

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"sort"
)

// ErrConflict is returned when an Add encounters a key that already
// holds different content and the set's strategy is ConflictError.
var ErrConflict = errors.New("speckey: key already exists with different content")

// ConflictStrategy determines how Add behaves when a key is already
// present with different content. There is no silent overwrite: every
// strategy makes the outcome explicit to the caller.
type ConflictStrategy int

const (
	// ConflictError rejects the new entry, keeps the original content
	// untouched, and returns ErrConflict.
	ConflictError ConflictStrategy = iota
	// ConflictIsolate keeps the original entry under the primary key
	// and stores the conflicting content under a derived isolation
	// key, so both contents remain addressable.
	ConflictIsolate
	// ConflictReplace explicitly overwrites the stored content. The
	// caller observes the replacement via the Replaced outcome.
	ConflictReplace
)

// Outcome describes the result of an Add call.
type Outcome int

const (
	// Added means the key was not present and the entry was stored.
	Added Outcome = iota
	// Duplicate means the key already held identical content; the
	// set is unchanged (deduplication).
	Duplicate
	// Isolated means the key held different content and the new
	// content was stored under a derived isolation key.
	Isolated
	// Replaced means the key held different content and was
	// explicitly overwritten per ConflictReplace.
	Replaced
	// Rejected means the add was refused per ConflictError; the set
	// is unchanged.
	Rejected
)

func (o Outcome) String() string {
	switch o {
	case Added:
		return "Added"
	case Duplicate:
		return "Duplicate"
	case Isolated:
		return "Isolated"
	case Replaced:
		return "Replaced"
	case Rejected:
		return "Rejected"
	default:
		return "Unknown"
	}
}

// Set is a deduplicating key/content store with an explicit conflict
// strategy. It is not safe for concurrent use.
type Set struct {
	strategy ConflictStrategy
	records  map[string]string // canonical (or isolation) key -> content
}

// NewSet creates an empty Set using the given conflict strategy.
func NewSet(strategy ConflictStrategy) *Set {
	return &Set{strategy: strategy, records: make(map[string]string)}
}

// Strategy returns the set's conflict strategy.
func (s *Set) Strategy() ConflictStrategy { return s.strategy }

// Len returns the total number of stored records, including any
// isolated conflict entries.
func (s *Set) Len() int { return len(s.records) }

// Add inserts (key, content) according to the set's conflict
// strategy. Identical key+content pairs deduplicate to a no-op.
func (s *Set) Add(key SpecKey, content string) (Outcome, error) {
	canonical := key.String()
	existing, ok := s.records[canonical]
	if !ok {
		s.records[canonical] = content
		return Added, nil
	}
	if existing == content {
		return Duplicate, nil
	}
	switch s.strategy {
	case ConflictIsolate:
		iso := isolationKey(canonical, content)
		if cur, exists := s.records[iso]; exists {
			if cur == content {
				return Duplicate, nil
			}
			// Only reachable on a SHA-256 collision.
			return Rejected, ErrConflict
		}
		s.records[iso] = content
		return Isolated, nil
	case ConflictReplace:
		s.records[canonical] = content
		return Replaced, nil
	default:
		return Rejected, ErrConflict
	}
}

// Get returns the primary content stored under key.
func (s *Set) Get(key SpecKey) (content string, ok bool) {
	content, ok = s.records[key.String()]
	return content, ok
}

// GetAll returns the primary content (if any) followed by all
// isolated contents derived from key, in deterministic order.
func (s *Set) GetAll(key SpecKey) []string {
	canonical := key.String()
	var out []string
	if c, ok := s.records[canonical]; ok {
		out = append(out, c)
	}
	prefix := canonical + isolationSep
	var isoKeys []string
	for k := range s.records {
		if len(k) > len(prefix) && k[:len(prefix)] == prefix {
			isoKeys = append(isoKeys, k)
		}
	}
	sort.Strings(isoKeys)
	for _, k := range isoKeys {
		out = append(out, s.records[k])
	}
	return out
}

const isolationSep = "#c:"

// isolationKey derives a deterministic, content-addressed key for a
// conflicting entry so it stays isolated from the primary record.
func isolationKey(canonical, content string) string {
	sum := sha256.Sum256([]byte(content))
	return canonical + isolationSep + hex.EncodeToString(sum[:])
}
