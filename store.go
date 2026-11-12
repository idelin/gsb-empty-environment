package netsec

import (
	"fmt"
	"sort"
	"strings"
	"sync"
)

// Options controls verification policy for a single operation.
type Options struct {
	// TrustSignerChange accepts a new signing identity and replaces the
	// package's currently trusted identity. It has no effect on checksum
	// mismatches or on signed-to-unsigned downgrades, which are always
	// rejected.
	TrustSignerChange bool
}

// Record is the trusted fingerprint and signing identity for one version.
// An empty Signer means the version was first seen unsigned.
type Record struct {
	Key      string `json:"key"`
	Scope    string `json:"scope"`
	Name     string `json:"name"`
	Version  string `json:"version"`
	Checksum string `json:"checksum"`
	Signer   string `json:"signer,omitempty"`
}

// Signed reports whether the recorded version had a signing identity.
func (r Record) Signed() bool {
	return r.Signer != ""
}

// Store is a concurrency-safe trusted-fingerprint store. Its zero value is
// ready for use.
type Store struct {
	mu       sync.Mutex
	packages map[string]*packageState
}

type packageState struct {
	// signer is the package's currently trusted signing identity. An empty
	// value means no version has established a signing identity yet.
	signer   string
	versions map[string]Record
}

// NewStore returns an empty trusted-fingerprint store.
func NewStore() *Store {
	return &Store{packages: make(map[string]*packageState)}
}

// Verify applies first-use trust to artifact or checks it against an existing
// record. A first occurrence records the checksum and signer. For known
// versions, a checksum change is rejected. For new versions, a different
// signing identity is rejected unless the caller explicitly trusts it.
// Signed-to-unsigned transitions are always rejected.
func (s *Store) Verify(artifact Artifact) (Record, error) {
	return s.VerifyWithOptions(artifact, Options{})
}

// VerifyWithOptions is Verify with explicit policy options.
func (s *Store) VerifyWithOptions(artifact Artifact, opts Options) (Record, error) {
	key, err := artifact.Key()
	if err != nil {
		return Record{}, err
	}
	if artifact.Checksum == "" || strings.TrimSpace(artifact.Checksum) == "" {
		return Record{}, fmt.Errorf("%w: %s", ErrEmptyChecksum, key)
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.packages == nil {
		s.packages = make(map[string]*packageState)
	}

	canonical := key.String()
	state := s.packages[packageID(key)]
	if state == nil {
		state = &packageState{versions: make(map[string]Record)}
		s.packages[packageID(key)] = state
	}

	record := Record{
		Key:      canonical,
		Scope:    key.Scope,
		Name:     key.Name,
		Version:  key.Version,
		Checksum: artifact.Checksum,
		Signer:   artifact.Signer,
	}

	existing, known := state.versions[key.Version]
	if known {
		return s.verifyKnownVersion(state, existing, record, opts)
	}
	return s.recordNewVersion(state, record, opts)
}

func (s *Store) verifyKnownVersion(
	state *packageState,
	existing, presented Record,
	opts Options,
) (Record, error) {
	if existing.Checksum != presented.Checksum {
		return Record{}, fmt.Errorf(
			"%w: %s recorded %q, presented %q",
			ErrChecksumMismatch, existing.Key, existing.Checksum, presented.Checksum,
		)
	}

	// Once a package has a signing identity, no unsigned artifact is
	// acceptable, including a previously recorded unsigned version.
	if presented.Signer == "" && state.signer != "" {
		return Record{}, fmt.Errorf(
			"%w: %s trusted signer %q",
			ErrUnsignedDowngrade, existing.Key, state.signer,
		)
	}

	if existing.Signer == presented.Signer {
		return existing, nil
	}

	// Any signing-identity change, including gaining a signature where the
	// version was previously unsigned, needs explicit trust.
	if !opts.TrustSignerChange {
		return Record{}, fmt.Errorf(
			"%w: %s trusted signer %q, presented %q",
			ErrSignerChanged, existing.Key, existing.Signer, presented.Signer,
		)
	}

	existing.Signer = presented.Signer
	state.signer = presented.Signer
	state.versions[existing.Version] = existing
	return existing, nil
}

func (s *Store) recordNewVersion(
	state *packageState,
	presented Record,
	opts Options,
) (Record, error) {
	if presented.Signer == "" && state.signer != "" {
		return Record{}, fmt.Errorf(
			"%w: %s trusted signer %q",
			ErrUnsignedDowngrade, presented.Key, state.signer,
		)
	}

	firstUse := len(state.versions) == 0
	if !firstUse && presented.Signer != state.signer && !opts.TrustSignerChange {
		// Any signing-identity change, including gaining a signature
		// after an unsigned history, needs explicit trust.
		return Record{}, fmt.Errorf(
			"%w: %s trusted signer %q, presented %q",
			ErrSignerChanged, presented.Key, state.signer, presented.Signer,
		)
	}

	state.signer = presented.Signer
	state.versions[presented.Version] = presented
	return presented, nil
}

// VerifyKey verifies an artifact identified by a "scope.name@version" key.
func (s *Store) VerifyKey(key, checksum, signer string) (Record, error) {
	return s.VerifyKeyWithOptions(key, checksum, signer, Options{})
}

// VerifyKeyWithOptions verifies a key-addressed artifact with explicit policy
// options.
func (s *Store) VerifyKeyWithOptions(
	key, checksum, signer string,
	opts Options,
) (Record, error) {
	parsed, err := ParseKey(key)
	if err != nil {
		return Record{}, err
	}
	return s.VerifyWithOptions(Artifact{
		Scope:    parsed.Scope,
		Name:     parsed.Name,
		Version:  parsed.Version,
		Checksum: checksum,
		Signer:   signer,
	}, opts)
}

// VerifyTrustingSignerChange verifies an artifact and explicitly accepts a
// change of signed identity. Checksum changes and unsigned downgrades remain
// rejected.
func (s *Store) VerifyTrustingSignerChange(artifact Artifact) (Record, error) {
	return s.VerifyWithOptions(artifact, Options{TrustSignerChange: true})
}

// VerifyKeyTrustingSignerChange is the key-addressed form of
// VerifyTrustingSignerChange.
func (s *Store) VerifyKeyTrustingSignerChange(key, checksum, signer string) (Record, error) {
	return s.VerifyKeyWithOptions(key, checksum, signer, Options{TrustSignerChange: true})
}

// Lookup returns the record for a canonical or case-variant key. The boolean
// is false when the key is valid but has never been recorded.
func (s *Store) Lookup(key string) (Record, bool, error) {
	parsed, err := ParseKey(key)
	if err != nil {
		return Record{}, false, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	state := s.packages[packageID(parsed)]
	if state == nil {
		return Record{}, false, nil
	}
	record, ok := state.versions[parsed.Version]
	return record, ok, nil
}

// Records returns a sorted snapshot of all trusted version records.
func (s *Store) Records() []Record {
	s.mu.Lock()
	defer s.mu.Unlock()

	records := make([]Record, 0, s.len())
	for _, state := range s.packages {
		for _, record := range state.versions {
			records = append(records, record)
		}
	}
	sort.Slice(records, func(i, j int) bool {
		return records[i].Key < records[j].Key
	})
	return records
}

func packageID(key PackageKey) string {
	return key.Scope + "." + key.Name
}

// Len returns the number of recorded package versions.
func (s *Store) Len() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.len()
}

func (s *Store) len() int {
	total := 0
	for _, state := range s.packages {
		total += len(state.versions)
	}
	return total
}
