package netsec

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"sort"
	"sync"
	"sync/atomic"
)

var (
	// ErrCapabilityDenied deliberately does not distinguish unknown,
	// cross-tenant, stale-generation, and revoked capabilities. Callers must
	// not turn this API into a capability oracle.
	ErrCapabilityDenied = errors.New("netsec: capability denied")
	ErrInvalidPrincipal = errors.New("netsec: invalid principal")
	ErrInvalidToken     = errors.New("netsec: invalid capability token")
	ErrDuplicateToken   = errors.New("netsec: capability token already registered")
	ErrInvalidGrant     = errors.New("netsec: invalid capability grant")
)

// Principal describes the tenant and generation under which a graph is being
// decoded. Generation matching is exact: a token from an older generation is
// denied even when the tenant is otherwise correct.
type Principal struct {
	Tenant     string
	Generation uint64
}

// Capability is the usable capability restored from the graph. It never
// contains the bearer token. Operations are sorted and de-duplicated.
type Capability struct {
	Tenant     string
	Generation uint64
	Operations []string
}

// Allows reports whether the capability includes operation.
func (c *Capability) Allows(operation string) bool {
	if c == nil {
		return false
	}
	i := sort.SearchStrings(c.Operations, operation)
	return i < len(c.Operations) && c.Operations[i] == operation
}

type capabilityRecord struct {
	tenant     string
	generation uint64
	operations []string
	revoked    bool
	active     int
}

// Registry is the trusted capability registrar. Tokens are bearer secrets and
// must be generated with at least 128 bits of entropy, for example via
// NewCapabilityToken.
type Registry struct {
	mu      sync.Mutex
	records map[string]*capabilityRecord
}

// NewRegistry creates an empty capability registry.
func NewRegistry() *Registry {
	return &Registry{records: make(map[string]*capabilityRecord)}
}

// NewCapabilityToken returns a URL-safe, 256-bit random bearer token.
func NewCapabilityToken() (string, error) {
	var raw [32]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(raw[:]), nil
}

// Register adds a token to the trusted registry. A token is immutable once
// registered. Registering a token again, even after revocation, fails.
func (r *Registry) Register(token, tenant string, generation uint64, operations ...string) error {
	if r == nil {
		return ErrInvalidToken
	}
	if token == "" {
		return ErrInvalidToken
	}
	if tenant == "" {
		return ErrInvalidPrincipal
	}
	ops, err := normalizeOperations(operations)
	if err != nil {
		return err
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	if r.records == nil {
		r.records = make(map[string]*capabilityRecord)
	}
	if _, exists := r.records[token]; exists {
		return ErrDuplicateToken
	}
	r.records[token] = &capabilityRecord{
		tenant:     tenant,
		generation: generation,
		operations: ops,
	}
	return nil
}

// Revoke makes a capability unavailable to new leases. It returns false only
// when the token is unknown. Revoking an already revoked token is safe.
func (r *Registry) Revoke(token string) bool {
	if r == nil || token == "" {
		return false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	record, ok := r.records[token]
	if !ok {
		return false
	}
	record.revoked = true
	return true
}

// IsRevoked reports whether token is registered and revoked.
func (r *Registry) IsRevoked(token string) bool {
	if r == nil || token == "" {
		return false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	record, ok := r.records[token]
	return ok && record.revoked
}

// ActiveLeaseCount reports how many distinct live leases include token. It is
// primarily useful for diagnostics and tests.
func (r *Registry) ActiveLeaseCount(token string) int {
	if r == nil || token == "" {
		return 0
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	record, ok := r.records[token]
	if !ok {
		return 0
	}
	return record.active
}

// AcquireAll atomically acquires one lease for every distinct token. Before
// any lease is created, all tokens are checked against the principal and
// revocation state under a single lock. Consequently a concurrent Revoke
// either happens entirely before this call (all tokens are denied) or after
// it (all leases are valid and remain usable until released).
//
// AcquireAll is part of LeaseIssuer. Applications should normally let the
// decoder call it rather than acquiring directly.
func (r *Registry) AcquireAll(principal Principal, tokens []string) (*LeaseSet, error) {
	if r == nil || principal.Tenant == "" {
		return nil, ErrInvalidPrincipal
	}
	unique, err := normalizeTokens(tokens)
	if err != nil {
		return nil, err
	}

	lease := &LeaseSet{
		registry:     r,
		tokens:       unique,
		tokenCaps:    make(map[string]*Capability, len(unique)),
		capabilities: make([]*Capability, 0, len(unique)),
	}
	lease.active.Store(true)

	r.mu.Lock()
	defer r.mu.Unlock()

	for _, token := range unique {
		record, ok := r.records[token]
		if !ok ||
			record.revoked ||
			record.tenant != principal.Tenant ||
			record.generation != principal.Generation {
			return nil, ErrCapabilityDenied
		}
	}

	for _, token := range unique {
		record := r.records[token]
		record.active++

		capability := &Capability{
			Tenant:     record.tenant,
			Generation: record.generation,
			Operations: append([]string(nil), record.operations...),
		}
		lease.tokenCaps[token] = capability
		lease.capabilities = append(lease.capabilities, capability)
	}
	return lease, nil
}

// LeaseIssuer acquires all requested leases as one atomic operation. The
// trusted Registry implements this contract. A custom implementation used in
// tests must preserve the same all-or-nothing behavior.
type LeaseIssuer interface {
	AcquireAll(principal Principal, tokens []string) (*LeaseSet, error)
}

var _ LeaseIssuer = (*Registry)(nil)

// LeaseSet owns the leases acquired for one decoded graph. It is safe for
// concurrent use. Revocation does not invalidate an already acquired lease.
type LeaseSet struct {
	once         sync.Once
	registry     *Registry
	tokens       []string
	tokenCaps    map[string]*Capability
	capabilities []*Capability
	active       atomic.Bool
}

// Release releases every lease in the set. It is idempotent and safe to call
// concurrently. A revoked capability can still be released normally.
func (l *LeaseSet) Release() {
	if l == nil {
		return
	}
	l.once.Do(func() {
		if l.registry != nil {
			l.registry.release(l.tokens)
		}
		l.active.Store(false)
	})
}

// Close is an alias for Release.
func (l *LeaseSet) Close() {
	l.Release()
}

// Active reports whether the set has not yet been released. Revocation of the
// underlying token does not change this result.
func (l *LeaseSet) Active() bool {
	if l == nil {
		return false
	}
	return l.active.Load()
}

// Len returns the number of distinct capability leases in the set.
func (l *LeaseSet) Len() int {
	if l == nil {
		return 0
	}
	return len(l.tokens)
}

// Capabilities returns the distinct restored capabilities in deterministic
// token order. The returned slice is a copy; the capability pointers are the
// same values embedded in the decoded graph.
func (l *LeaseSet) Capabilities() []*Capability {
	if l == nil {
		return nil
	}
	return append([]*Capability(nil), l.capabilities...)
}

// String redacts the underlying bearer tokens from diagnostic output.
func (l *LeaseSet) String() string {
	if l == nil {
		return "<nil>"
	}
	return fmt.Sprintf("netsec.LeaseSet{leases:%d, active:%t}", len(l.tokens), l.active.Load())
}

// GoString redacts the underlying bearer tokens for %#v formatting.
func (l *LeaseSet) GoString() string {
	return l.String()
}

func (l *LeaseSet) capabilityFor(token string) (*Capability, bool) {
	if l == nil {
		return nil, false
	}
	capability, ok := l.tokenCaps[token]
	return capability, ok
}

func (r *Registry) release(tokens []string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, token := range tokens {
		record, ok := r.records[token]
		if !ok || record.active == 0 {
			continue
		}
		record.active--
	}
}

func normalizeOperations(operations []string) ([]string, error) {
	seen := make(map[string]struct{}, len(operations))
	result := make([]string, 0, len(operations))
	for _, operation := range operations {
		if operation == "" {
			return nil, ErrInvalidGrant
		}
		if _, exists := seen[operation]; exists {
			continue
		}
		seen[operation] = struct{}{}
		result = append(result, operation)
	}
	sort.Strings(result)
	return result, nil
}

func normalizeTokens(tokens []string) ([]string, error) {
	if len(tokens) == 0 {
		return nil, nil
	}
	seen := make(map[string]struct{}, len(tokens))
	result := make([]string, 0, len(tokens))
	for _, token := range tokens {
		if token == "" {
			return nil, ErrInvalidToken
		}
		if _, exists := seen[token]; exists {
			continue
		}
		seen[token] = struct{}{}
		result = append(result, token)
	}
	sort.Strings(result)
	return result, nil
}
