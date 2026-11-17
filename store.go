package netsec

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"sync"
)

var (
	// ErrInvalidKey is returned when an object key is empty.
	ErrInvalidKey = errors.New("netsec: object key must not be empty")

	// ErrMissingLabel is returned when a release has no security label. It is
	// returned on publish and, defensively, on read. Missing labels are never
	// treated as an implicit public classification.
	ErrMissingLabel = errors.New("netsec: security label is missing")

	// ErrNotFound is returned when an object has never been published.
	ErrNotFound = errors.New("netsec: object not found")

	// ErrDowngrade is returned when a publication lowers the security level of
	// an existing object without a matching approval version.
	ErrDowngrade = errors.New("netsec: security label downgrade requires the current approved version")

	// ErrVersionOverflow is returned when an object's version counter cannot be
	// advanced safely.
	ErrVersionOverflow = errors.New("netsec: version counter overflow")
)

// Digest is the SHA-256 digest of a release's content.
type Digest [sha256.Size]byte

// String returns the lower-case hexadecimal representation of d.
func (d Digest) String() string {
	return hex.EncodeToString(d[:])
}

// Commit describes the complete, immutable state of a release being committed.
// Content is a defensive copy, so a CommitHook cannot modify the data that
// will become visible through a Handle.
type Commit struct {
	Key     string
	Version uint64
	Digest  Digest
	Label   Label
	Content []byte
}

// CommitHook is the durability barrier for a publication. It is invoked while
// the store is serializing writers and before the new release becomes visible
// to readers. Returning an error aborts the publication; no part of the
// candidate content, digest, label, or version becomes visible.
//
// A CommitHook must not call back into the Store.
type CommitHook func(Commit) error

// Option configures a Store.
type Option func(*Store)

// WithCommitHook installs a commit barrier. It is primarily useful for
// integrating durable storage and for fault-injection tests.
func WithCommitHook(h CommitHook) Option {
	return func(s *Store) {
		s.commit = h
	}
}

// Store is an in-memory, versioned object store. Each key has exactly one
// current release. Writers are serialized; readers acquire an immutable Handle
// that binds the release's content digest and security label.
type Store struct {
	mu      sync.RWMutex
	objects map[string]*version
	commit  CommitHook
}

type version struct {
	number  uint64
	digest  Digest
	label   Label
	content []byte
}

// NewStore creates an empty Store.
func NewStore(opts ...Option) *Store {
	s := &Store{objects: make(map[string]*version)}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

// New is an alias for NewStore.
func New(opts ...Option) *Store {
	return NewStore(opts...)
}

type publishConfig struct {
	approvedVersion *uint64
}

// PublishOption configures a single publication.
type PublishOption func(*publishConfig)

// ApproveDowngrade declares that the caller has reviewed version and approves
// a downgrade from it. The option is only consulted when the new label has a
// lower Level than the current label. The approved version must equal the
// object's current version at commit time; a stale approval is rejected.
func ApproveDowngrade(version uint64) PublishOption {
	return func(c *publishConfig) {
		v := version
		c.approvedVersion = &v
	}
}

// Publish atomically creates a new release of key. The content digest, label,
// and version counter are staged together and become visible in one swap. If
// the commit barrier fails, the previous release (or the absence of the
// object) remains unchanged.
//
// Publishing a label with a lower Level than the current label requires an
// ApproveDowngrade option naming the exact current version.
func (s *Store) Publish(key string, content []byte, label Label, opts ...PublishOption) (*Handle, error) {
	if key == "" {
		return nil, ErrInvalidKey
	}
	if !label.Valid() {
		return nil, ErrMissingLabel
	}

	var cfg publishConfig
	for _, opt := range opts {
		opt(&cfg)
	}

	data := append([]byte(nil), content...)
	digest := Digest(sha256.Sum256(data))

	s.mu.Lock()
	defer s.mu.Unlock()

	var nextNumber uint64 = 1
	if current := s.objects[key]; current != nil {
		if label.Level < current.label.Level {
			if cfg.approvedVersion == nil {
				return nil, fmt.Errorf("%w: current version is %d with label %s", ErrDowngrade, current.number, current.label)
			}
			if *cfg.approvedVersion != current.number {
				return nil, fmt.Errorf("%w: approved version %d does not match current version %d", ErrDowngrade, *cfg.approvedVersion, current.number)
			}
		}
		if current.number == math.MaxUint64 {
			return nil, ErrVersionOverflow
		}
		nextNumber = current.number + 1
	}

	next := &version{
		number:  nextNumber,
		digest:  digest,
		label:   label,
		content: data,
	}

	if s.commit != nil {
		commitContent := append([]byte(nil), data...)
		if err := s.commit(Commit{
			Key:     key,
			Version: nextNumber,
			Digest:  digest,
			Label:   label,
			Content: commitContent,
		}); err != nil {
			return nil, fmt.Errorf("netsec: commit failed: %w", err)
		}
	}

	s.objects[key] = next
	return next.handle(key), nil
}

// Get returns an immutable Handle for the current release of key. The Handle
// contains the exact content, digest, and label that were committed together.
// A release whose label is missing is denied.
func (s *Store) Get(key string) (*Handle, error) {
	s.mu.RLock()
	current := s.objects[key]
	s.mu.RUnlock()

	if current == nil {
		return nil, ErrNotFound
	}
	if !current.label.Valid() {
		return nil, fmt.Errorf("%w: %q version %d", ErrMissingLabel, key, current.number)
	}
	return current.handle(key), nil
}

// Acquire is an alias for Get that emphasizes the caller receives an immutable
// release Handle.
func (s *Store) Acquire(key string) (*Handle, error) {
	return s.Get(key)
}

func (v *version) handle(key string) *Handle {
	return &Handle{
		key:     key,
		version: v.number,
		digest:  v.digest,
		label:   v.label,
		content: v.content,
	}
}
