package netsec

import (
	"bytes"
	"errors"
	"sync"
)

// Digest is a content identifier produced by a hash function.
type Digest []byte

// Equal reports whether two digests have identical bytes.
func (d Digest) Equal(other Digest) bool { return bytes.Equal(d, other) }

// Hash computes a digest over content. Implementations must be deterministic.
// A hash that collapses distinct contents to the same digest models the
// collision behaviour of the legacy digest algorithm.
type Hash func(content []byte) Digest

// Blob is one candidate object stored under an old digest.
//
// Two distinct blobs may share the same Old digest because the legacy hash
// collides; NewDigest is empty until the blob has been migrated.
type Blob struct {
	Old       Digest
	NewDigest Digest
	Content   []byte
	Refs      int
}

// Store is the storage-host contract consumed by the migrator.
//
// Implementations own persistence and locking. Every method is invoked with
// the migrator's per-old-digest lock held, so a host may serialize operations
// at the granularity of a single old index key.
type Store interface {
	// Candidates returns every blob indexed under oldDigest.
	Candidates(oldDigest Digest) ([]*Blob, error)
	// LookupNew returns the blob already carrying newDigest, or nil when the
	// new identity does not exist yet.
	LookupNew(newDigest Digest) (*Blob, error)
	// PutNew installs blob under its new digest and transfers the reference
	// count. It must not merge the blob with an existing distinct object.
	PutNew(blob *Blob) error
	// RangeOld calls fn once per distinct old digest. Iteration must be safe
	// against concurrent migration; fn returning an error stops the walk.
	RangeOld(fn func(oldDigest Digest) error) error
}

// AmbiguityError reports that an old digest maps to more than one distinct
// object. The host must surface it to readers instead of guessing an identity.
type AmbiguityError struct {
	OldDigest  Digest
	Candidates []Digest
}

func (e *AmbiguityError) Error() string {
	return "netsec: old digest is ambiguous: " + string(e.OldDigest)
}

// IntegrityError reports that re-read content fails digest verification.
type IntegrityError struct {
	OldDigest Digest
	Blob      *Blob
	field     string
}

func (e *IntegrityError) Error() string {
	return "netsec: integrity check failed for blob under " + string(e.OldDigest) + ": " + e.field
}

// ErrNotFound is returned when an old digest has no indexed object.
var ErrNotFound = errors.New("netsec: old digest not found")

// Migrator performs dual-digest identity migration. It is safe for concurrent
// use: migration and reads of the same old digest are serialized, while
// distinct old digests proceed in parallel.
type Migrator struct {
	store Store
	oldH  Hash
	newH  Hash

	locksMu sync.Mutex
	locks   map[string]*keyGate
}

type keyGate struct {
	mu sync.Mutex
}

// NewMigrator builds a migrator. oldH is the legacy (collision-capable) hash
// and newH is the stronger replacement hash.
func NewMigrator(store Store, oldH, newH Hash) *Migrator {
	return &Migrator{store: store, oldH: oldH, newH: newH, locks: make(map[string]*keyGate)}
}

func (m *Migrator) gate(oldDigest Digest) *keyGate {
	key := string(oldDigest)
	m.locksMu.Lock()
	g, ok := m.locks[key]
	if !ok {
		g = &keyGate{}
		m.locks[key] = g
	}
	m.locksMu.Unlock()
	return g
}

// Resolve maps an old digest to new identities. It migrates the key on first
// use and is idempotent afterwards.
//
// The returned digests contain one element per distinct object behind the old
// digest. A single element is the unambiguous new identity; multiple elements
// are returned together with an *AmbiguityError so the host can fail the read
// explicitly rather than merging colliding objects.
func (m *Migrator) Resolve(oldDigest Digest) ([]Digest, error) {
	g := m.gate(oldDigest)
	g.mu.Lock()
	defer g.mu.Unlock()

	candidates, err := m.store.Candidates(oldDigest)
	if err != nil {
		return nil, err
	}
	if len(candidates) == 0 {
		return nil, ErrNotFound
	}

	if err := m.migrateLocked(oldDigest, candidates); err != nil {
		return nil, err
	}

	newDigests := make([]Digest, 0, len(candidates))
	seen := make(map[string]struct{}, len(candidates))
	for _, blob := range candidates {
		id := string(blob.NewDigest)
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		newDigests = append(newDigests, blob.NewDigest)
	}
	if len(newDigests) > 1 {
		return newDigests, &AmbiguityError{OldDigest: append(Digest(nil), oldDigest...), Candidates: newDigests}
	}
	return newDigests, nil
}

// migrateLocked establishes new identities for every distinct candidate. The
// per-old-digest gate is held, so concurrent reads block until migration
// either completes or fails.
//
// Progress is measured against the new index, never against in-memory state:
// a blob whose PutNew failed is retried cleanly on the next Resolve. Blob
// reference counts sharing one new identity are aggregated into a single
// PutNew so the new index conserves the old reference count.
func (m *Migrator) migrateLocked(oldDigest Digest, candidates []*Blob) error {
	type group struct {
		blob    *Blob
		content []byte
		refs    int
	}
	groups := make(map[string]*group, len(candidates))
	order := make([]string, 0, len(candidates))

	for _, blob := range candidates {
		if got := m.oldH(blob.Content); !got.Equal(blob.Old) {
			return &IntegrityError{OldDigest: append(Digest(nil), oldDigest...), Blob: blob, field: "old digest mismatch"}
		}
		newDigest := m.newH(blob.Content)

		existing, err := m.store.LookupNew(newDigest)
		if err != nil {
			return err
		}
		if existing != nil && !bytes.Equal(existing.Content, blob.Content) {
			return &IntegrityError{OldDigest: append(Digest(nil), oldDigest...), Blob: blob, field: "new digest collision with distinct content"}
		}
		if existing != nil {
			blob.NewDigest = append(Digest(nil), newDigest...)
			continue
		}

		key := string(newDigest)
		g, ok := groups[key]
		if !ok {
			g = &group{blob: blob, content: blob.Content}
			groups[key] = g
			order = append(order, key)
		} else if !bytes.Equal(g.content, blob.Content) {
			return &IntegrityError{OldDigest: append(Digest(nil), oldDigest...), Blob: blob, field: "new digest collision with distinct content"}
		}
		g.refs += blob.Refs
		blob.NewDigest = append(Digest(nil), newDigest...)
	}

	for _, key := range order {
		g := groups[key]
		identity := &Blob{
			Old:       append(Digest(nil), g.blob.Old...),
			NewDigest: Digest(key),
			Content:   append([]byte(nil), g.content...),
			Refs:      g.refs,
		}
		if err := m.store.PutNew(identity); err != nil {
			return err
		}
	}
	return nil
}

// MigrateAll walks the whole old index and resolves every key. Ambiguities and
// per-key failures are collected instead of aborting the walk, so one bad key
// does not block migration of the rest.
func (m *Migrator) MigrateAll() error {
	var problems []error
	err := m.store.RangeOld(func(oldDigest Digest) error {
		if _, err := m.Resolve(oldDigest); err != nil {
			problems = append(problems, err)
		}
		return nil
	})
	if err != nil {
		return err
	}
	return errors.Join(problems...)
}
