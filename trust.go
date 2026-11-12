package taskpkg

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"sync"
)

// Fingerprint is the stable identity of a delegation public key. Trust
// decisions are always made on fingerprints, never on version numbers.
type Fingerprint string

// FingerprintOf returns the SHA-256 fingerprint of an Ed25519 public key.
func FingerprintOf(pub ed25519.PublicKey) Fingerprint {
	sum := sha256.Sum256(pub)
	return Fingerprint(hex.EncodeToString(sum[:]))
}

// TrustAnchor pins a query to one authoritative zone and one trust-anchor
// version. The pin is immutable for the lifetime of the query.
type TrustAnchor struct {
	Zone    string
	Version uint64
}

// Declaration is one trust-generation statement for a zone. Retained is the
// explicit allow-list of key fingerprints at this generation. A key that is
// absent from Retained is revoked, regardless of any version ordering.
type Declaration struct {
	Zone     string
	Version  uint64
	Retained []Fingerprint
	keys     map[Fingerprint]ed25519.PublicKey
}

// Allows reports whether fp is on the retained fingerprint allow-list.
func (d *Declaration) Allows(fp Fingerprint) bool {
	_, ok := d.keys[fp]
	return ok
}

// PublicKey returns the retained public key for fp, if any.
func (d *Declaration) PublicKey(fp Fingerprint) (ed25519.PublicKey, bool) {
	pub, ok := d.keys[fp]
	return pub, ok
}

// Retention records whether one fingerprint was retained at one generation.
type Retention struct {
	Version  uint64
	Retained bool
}

var (
	// ErrUnknownZone is returned when no declaration exists for a zone.
	ErrUnknownZone = errors.New("taskpkg: unknown zone")
	// ErrUnknownVersion is returned when no declaration exists at a version.
	ErrUnknownVersion = errors.New("taskpkg: unknown trust-anchor version")
	// ErrVersionOutOfOrder is returned when a declaration version is not
	// strictly greater than the current one, or reuses an old version.
	ErrVersionOutOfOrder = errors.New("taskpkg: declaration version must be strictly greater than the current version")
	// ErrBadFingerprint is returned when a key does not match its fingerprint.
	ErrBadFingerprint = errors.New("taskpkg: fingerprint does not match public key")
	// ErrEmptyDeclaration is returned when a declaration retains no keys.
	ErrEmptyDeclaration = errors.New("taskpkg: declaration must retain at least one key")
)

// TrustStore keeps the full declaration history of every authoritative zone.
// History is retained so late answers can still be verified against the
// generation a query was anchored to, while authorization is always decided
// against the current generation.
type TrustStore struct {
	mu    sync.RWMutex
	zones map[string]*zoneState
}

type zoneState struct {
	decls []*Declaration
	byVer map[uint64]*Declaration
}

// NewTrustStore creates an empty trust store.
func NewTrustStore() *TrustStore {
	return &TrustStore{zones: make(map[string]*zoneState)}
}

// Bootstrap installs the first declaration of a zone.
func (s *TrustStore) Bootstrap(zone string, version uint64, keys map[Fingerprint]ed25519.PublicKey) (*Declaration, error) {
	if zone == "" {
		return nil, fmt.Errorf("%w: empty zone", ErrUnknownZone)
	}
	decl, err := newDeclaration(zone, version, keys)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.zones[zone]; ok {
		return nil, fmt.Errorf("%w: zone %q already bootstrapped", ErrVersionOutOfOrder, zone)
	}
	s.zones[zone] = &zoneState{
		decls: []*Declaration{decl},
		byVer: map[uint64]*Declaration{version: decl},
	}
	return decl, nil
}

// Update installs a new generation. The version must be strictly greater than
// the current one; the retained fingerprint set defines which keys survive.
func (s *TrustStore) Update(zone string, version uint64, keys map[Fingerprint]ed25519.PublicKey) (*Declaration, error) {
	decl, err := newDeclaration(zone, version, keys)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	zs, ok := s.zones[zone]
	if !ok {
		return nil, fmt.Errorf("%w: %q", ErrUnknownZone, zone)
	}
	current := zs.decls[len(zs.decls)-1]
	if version <= current.Version {
		return nil, fmt.Errorf("%w: zone %q current=%d got=%d", ErrVersionOutOfOrder, zone, current.Version, version)
	}
	if _, dup := zs.byVer[version]; dup {
		return nil, fmt.Errorf("%w: zone %q version %d already exists", ErrVersionOutOfOrder, zone, version)
	}
	zs.decls = append(zs.decls, decl)
	zs.byVer[version] = decl
	return decl, nil
}

// Current returns the active declaration of a zone.
func (s *TrustStore) Current(zone string) (*Declaration, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	zs, ok := s.zones[zone]
	if !ok {
		return nil, fmt.Errorf("%w: %q", ErrUnknownZone, zone)
	}
	return zs.decls[len(zs.decls)-1], nil
}

// At returns the declaration of a zone at an exact version.
func (s *TrustStore) At(zone string, version uint64) (*Declaration, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	zs, ok := s.zones[zone]
	if !ok {
		return nil, fmt.Errorf("%w: %q", ErrUnknownZone, zone)
	}
	decl, ok := zs.byVer[version]
	if !ok {
		return nil, fmt.Errorf("%w: zone %q version %d", ErrUnknownVersion, zone, version)
	}
	return decl, nil
}

// IsKeyActive reports whether fp is retained by the current declaration of
// the zone. This is the revocation check applied at cache-publish time.
func (s *TrustStore) IsKeyActive(zone string, fp Fingerprint) bool {
	cur, err := s.Current(zone)
	if err != nil {
		return false
	}
	return cur.Allows(fp)
}

// RetentionWalk walks every declaration generation of a zone and reports
// whether fp was retained at each generation, in version order.
func (s *TrustStore) RetentionWalk(zone string, fp Fingerprint) ([]Retention, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	zs, ok := s.zones[zone]
	if !ok {
		return nil, fmt.Errorf("%w: %q", ErrUnknownZone, zone)
	}
	walk := make([]Retention, 0, len(zs.decls))
	for _, decl := range zs.decls {
		walk = append(walk, Retention{Version: decl.Version, Retained: decl.Allows(fp)})
	}
	return walk, nil
}

func newDeclaration(zone string, version uint64, keys map[Fingerprint]ed25519.PublicKey) (*Declaration, error) {
	if len(keys) == 0 {
		return nil, fmt.Errorf("%w: zone %q version %d", ErrEmptyDeclaration, zone, version)
	}
	retained := make([]Fingerprint, 0, len(keys))
	owned := make(map[Fingerprint]ed25519.PublicKey, len(keys))
	for fp, pub := range keys {
		if FingerprintOf(pub) != fp {
			return nil, fmt.Errorf("%w: zone %q key %s", ErrBadFingerprint, zone, fp)
		}
		retained = append(retained, fp)
		owned[fp] = pub
	}
	sort.Slice(retained, func(i, j int) bool { return retained[i] < retained[j] })
	return &Declaration{
		Zone:     zone,
		Version:  version,
		Retained: retained,
		keys:     owned,
	}, nil
}
