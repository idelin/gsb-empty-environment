package netsec

import (
	"bytes"
	"crypto/ed25519"
	"encoding/binary"
	"fmt"
	"math"
	"sync"
)

// Role is the duty assigned to an administrator credential.
type Role string

const (
	RoleSecurityOfficer Role = "security-officer"
	RoleKeyCustodian    Role = "key-custodian"
	RoleAdministrator   Role = "administrator"
)

// Policy controls which distinct people may approve a ceremony and which
// duties must be represented. An empty AllowedRoles list defaults to the
// SecurityOfficer and KeyCustodian roles.
type Policy struct {
	Version        uint64
	Threshold      int
	ShareThreshold int
	AllowedRoles   []Role
	RequiredRoles  []Role
}

func defaultPolicy(version uint64) Policy {
	return Policy{
		Version:        version,
		Threshold:      2,
		ShareThreshold: 2,
		AllowedRoles:   []Role{RoleSecurityOfficer, RoleKeyCustodian},
	}
}

type policySnapshot struct {
	version        uint64
	threshold      int
	shareThreshold int
	allowedRoles   map[Role]struct{}
	requiredRoles  map[Role]struct{}
}

func normalizePolicy(p Policy) (policySnapshot, error) {
	allowed := make(map[Role]struct{}, len(p.AllowedRoles))
	if len(p.AllowedRoles) == 0 {
		allowed[RoleSecurityOfficer] = struct{}{}
		allowed[RoleKeyCustodian] = struct{}{}
	} else {
		for _, role := range p.AllowedRoles {
			if role == "" {
				return policySnapshot{}, fmt.Errorf("%w: empty allowed role", ErrInvalidPolicy)
			}
			if role == RoleAdministrator {
				return policySnapshot{}, fmt.Errorf("%w: administrator role cannot approve imports", ErrInvalidPolicy)
			}
			if role != RoleSecurityOfficer && role != RoleKeyCustodian {
				return policySnapshot{}, fmt.Errorf("%w: unknown allowed role %q", ErrInvalidPolicy, role)
			}
			allowed[role] = struct{}{}
		}
	}

	required := make(map[Role]struct{}, len(p.RequiredRoles))
	for _, role := range p.RequiredRoles {
		if role == "" {
			return policySnapshot{}, fmt.Errorf("%w: empty required role", ErrInvalidPolicy)
		}
		if role == RoleAdministrator {
			return policySnapshot{}, fmt.Errorf("%w: administrator role cannot approve imports", ErrInvalidPolicy)
		}
		if role != RoleSecurityOfficer && role != RoleKeyCustodian {
			return policySnapshot{}, fmt.Errorf("%w: unknown required role %q", ErrInvalidPolicy, role)
		}
		if _, ok := allowed[role]; !ok {
			return policySnapshot{}, fmt.Errorf("%w: required role %q is not allowed", ErrInvalidPolicy, role)
		}
		required[role] = struct{}{}
	}

	shareThreshold := p.ShareThreshold
	if shareThreshold == 0 {
		shareThreshold = p.Threshold
	}
	if p.Threshold < 2 || shareThreshold < 2 {
		return policySnapshot{}, ErrWeakPolicy
	}

	return policySnapshot{
		version:        p.Version,
		threshold:      p.Threshold,
		shareThreshold: shareThreshold,
		allowedRoles:   allowed,
		requiredRoles:  required,
	}, nil
}

func (p policySnapshot) public() Policy {
	allowed := make([]Role, 0, len(p.allowedRoles))
	for role := range p.allowedRoles {
		allowed = append(allowed, role)
	}
	required := make([]Role, 0, len(p.requiredRoles))
	for role := range p.requiredRoles {
		required = append(required, role)
	}
	return Policy{
		Version:        p.version,
		Threshold:      p.threshold,
		ShareThreshold: p.shareThreshold,
		AllowedRoles:   allowed,
		RequiredRoles:  required,
	}
}

// Credential is the immutable administrator identity that the HR root
// attests. A person may own several credentials, but they all carry the same
// PersonID and together have at most one approval vote per person.
type Credential struct {
	CredentialID         string
	PersonID             string
	PublicKey            ed25519.PublicKey
	Role                 Role
	RevocationGeneration uint64
}

func (c Credential) clone() Credential {
	c.PublicKey = append(ed25519.PublicKey(nil), c.PublicKey...)
	return c
}

// SignedCredential is an HR-root-signed registry entry.
type SignedCredential struct {
	Credential
	Signature []byte
}

// CredentialSigningPayload is the exact domain-separated byte string signed
// by the HR root when enrolling an administrator credential.
func CredentialSigningPayload(c Credential) []byte {
	payload := canonicalField(nil, []byte("netsec.hr-credential.v1"))
	payload = canonicalString(payload, c.CredentialID)
	payload = canonicalString(payload, c.PersonID)
	payload = canonicalBytes(payload, c.PublicKey)
	payload = canonicalString(payload, string(c.Role))
	payload = canonicalUint64(payload, c.RevocationGeneration)
	return payload
}

// SignCredential produces an HR-root approval for a registry entry.
func SignCredential(root ed25519.PrivateKey, c Credential) (SignedCredential, error) {
	if len(root) != ed25519.PrivateKeySize {
		return SignedCredential{}, ErrInvalidArgument
	}
	if err := validateCredential(c, false); err != nil {
		return SignedCredential{}, err
	}
	return SignedCredential{
		Credential: c.clone(),
		Signature:  ed25519.Sign(root, CredentialSigningPayload(c)),
	}, nil
}

func validateCredential(c Credential, verifySignature bool) error {
	if c.CredentialID == "" || c.PersonID == "" || c.Role == "" {
		return fmt.Errorf("%w: empty credential identity or role", ErrInvalidRegistration)
	}
	if len(c.PublicKey) != ed25519.PublicKeySize {
		return fmt.Errorf("%w: administrator public key must be Ed25519", ErrInvalidRegistration)
	}
	if verifySignature && c.RevocationGeneration == math.MaxUint64 {
		return fmt.Errorf("%w: revocation generation exhausted", ErrInvalidRegistration)
	}
	return nil
}

// CredentialState reports the current registry view of a credential.
type CredentialState struct {
	Credential
	Revoked bool
}

type credentialEntry struct {
	signed  SignedCredential
	revoked bool
}

// Registry is an HR-root-authenticated administrator registry. All public
// methods are safe for concurrent use.
type Registry struct {
	mu           sync.RWMutex
	hrRoot       ed25519.PublicKey
	credentials  map[string]*credentialEntry
	policy       policySnapshot
	observers    map[uint64]func()
	nextObserver uint64
}

// NewRegistry creates a registry whose entries must be signed by hrRoot.
func NewRegistry(hrRoot ed25519.PublicKey, policy Policy) (*Registry, error) {
	if len(hrRoot) != ed25519.PublicKeySize {
		return nil, ErrInvalidArgument
	}
	normalized, err := normalizePolicy(policy)
	if err != nil {
		return nil, err
	}
	return &Registry{
		hrRoot:      append(ed25519.PublicKey(nil), hrRoot...),
		credentials: make(map[string]*credentialEntry),
		policy:      normalized,
		observers:   make(map[uint64]func()),
	}, nil
}

// DefaultPolicy returns the baseline two-person policy with both required
// approval roles and a separate minimum share count.
func DefaultPolicy(version uint64) Policy {
	p := defaultPolicy(version)
	p.RequiredRoles = []Role{RoleSecurityOfficer, RoleKeyCustodian}
	return p
}

// Register verifies the HR root signature before adding a credential. A
// credential ID can never be rebound to another person or silently mutated.
func (r *Registry) Register(signed SignedCredential) error {
	if err := validateCredential(signed.Credential, true); err != nil {
		return err
	}
	if !ed25519.Verify(r.hrRoot, CredentialSigningPayload(signed.Credential), signed.Signature) {
		return ErrInvalidRegistration
	}
	entry := &credentialEntry{signed: SignedCredential{
		Credential: signed.Credential.clone(),
		Signature:  append([]byte(nil), signed.Signature...),
	}}

	r.mu.Lock()
	defer r.mu.Unlock()

	if existing, ok := r.credentials[signed.CredentialID]; ok {
		if existing.signed.PersonID != signed.PersonID {
			return ErrPersonIDImmutable
		}
		if !credentialsEqual(existing.signed.Credential, signed.Credential) ||
			!bytes.Equal(existing.signed.Signature, signed.Signature) {
			return ErrCredentialImmutable
		}
		return nil
	}

	for _, existing := range r.credentials {
		if bytes.Equal(existing.signed.PublicKey, signed.PublicKey) &&
			existing.signed.PersonID != signed.PersonID {
			return ErrPublicKeyConflict
		}
	}

	r.credentials[signed.CredentialID] = entry
	return nil
}

func credentialsEqual(a, b Credential) bool {
	return a.CredentialID == b.CredentialID &&
		a.PersonID == b.PersonID &&
		a.Role == b.Role &&
		a.RevocationGeneration == b.RevocationGeneration &&
		bytes.Equal(a.PublicKey, b.PublicKey)
}

// RevokeCredential invalidates a credential and advances its revocation
// generation. The next registry check rejects any approval made at the old
// generation.
func (r *Registry) RevokeCredential(credentialID string) error {
	r.mu.Lock()
	entry, ok := r.credentials[credentialID]
	if !ok {
		r.mu.Unlock()
		return ErrCredentialNotFound
	}
	if entry.revoked {
		r.mu.Unlock()
		return ErrCredentialRevoked
	}
	if entry.signed.RevocationGeneration == math.MaxUint64 {
		r.mu.Unlock()
		return fmt.Errorf("%w: revocation generation exhausted", ErrInvalidRegistration)
	}
	entry.revoked = true
	entry.signed.RevocationGeneration++
	observers := r.observersSnapshot()
	r.mu.Unlock()
	notifyObservers(observers)
	return nil
}

// Credential returns the current state of a credential.
func (r *Registry) Credential(credentialID string) (CredentialState, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	entry, ok := r.credentials[credentialID]
	if !ok {
		return CredentialState{}, false
	}
	return CredentialState{
		Credential: entry.signed.Credential.clone(),
		Revoked:    entry.revoked,
	}, true
}

// Policy returns the current policy generation.
func (r *Registry) Policy() Policy {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.policy.public()
}

func (r *Registry) policySnapshot() policySnapshot {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.policy
}

// SetPolicy advances the policy generation. Existing ceremonies bind the old
// version and are terminated when this change is observed.
func (r *Registry) SetPolicy(next Policy) error {
	normalized, err := normalizePolicy(next)
	if err != nil {
		return err
	}

	r.mu.Lock()
	if normalized.version <= r.policy.version {
		r.mu.Unlock()
		return fmt.Errorf("%w: policy version must increase", ErrInvalidPolicy)
	}
	r.policy = normalized
	observers := r.observersSnapshot()
	r.mu.Unlock()
	notifyObservers(observers)
	return nil
}

func (r *Registry) subscribe(fn func()) func() {
	r.mu.Lock()
	r.nextObserver++
	id := r.nextObserver
	r.observers[id] = fn
	r.mu.Unlock()
	return func() {
		r.mu.Lock()
		delete(r.observers, id)
		r.mu.Unlock()
	}
}

func (r *Registry) observersSnapshot() []func() {
	observers := make([]func(), 0, len(r.observers))
	for _, fn := range r.observers {
		observers = append(observers, fn)
	}
	return observers
}

func notifyObservers(observers []func()) {
	for _, fn := range observers {
		fn()
	}
}

func canonicalField(dst []byte, value []byte) []byte {
	var length [8]byte
	binary.BigEndian.PutUint64(length[:], uint64(len(value)))
	dst = append(dst, length[:]...)
	dst = append(dst, value...)
	return dst
}

func canonicalString(dst []byte, value string) []byte {
	return canonicalField(dst, []byte(value))
}

func canonicalBytes(dst []byte, value []byte) []byte {
	return canonicalField(dst, value)
}

func canonicalUint64(dst []byte, value uint64) []byte {
	var encoded [8]byte
	binary.BigEndian.PutUint64(encoded[:], value)
	return append(dst, encoded[:]...)
}

func canonicalInt64(dst []byte, value int64) []byte {
	var encoded [8]byte
	binary.BigEndian.PutUint64(encoded[:], uint64(value))
	return append(dst, encoded[:]...)
}

func canonicalDigest(dst []byte, value [32]byte) []byte {
	return canonicalBytes(dst, value[:])
}

func isPolicyRoleAllowed(policy policySnapshot, role Role) bool {
	_, ok := policy.allowedRoles[role]
	return ok
}

func missingRole(policy policySnapshot, seen map[Role]struct{}) bool {
	for role := range policy.requiredRoles {
		if _, ok := seen[role]; !ok {
			return true
		}
	}
	return false
}
