package netsec

import (
	"context"
	"fmt"
	"sync"
)

// AuthRequest is the complete immutable operation presented to the
// authorizer.  The authorizer must check the caller's *current* credential
// generation and current policy.  Host does not consult the authorizer during
// recovery.
type AuthRequest = TxIdentity

// Authorization is the result of a current credential/policy check.
type Authorization struct {
	Allowed      bool
	PolicyDigest Digest

	// HasCredentialGeneration should be set when the authorizer resolved
	// the principal's current generation.  Host then requires it to equal
	// the generation asserted in the request.
	HasCredentialGeneration bool
	CredentialGeneration    uint64
}

// Authorizer performs an online-only authorization check.
type Authorizer interface {
	Authorize(ctx context.Context, req AuthRequest) (Authorization, error)
}

// AuthorizerFunc adapts a function to Authorizer.
type AuthorizerFunc func(ctx context.Context, req AuthRequest) (Authorization, error)

func (f AuthorizerFunc) Authorize(ctx context.Context, req AuthRequest) (Authorization, error) {
	return f(ctx, req)
}

// PrincipalState is the current credential generation and permissions for a
// principal.  A principal name is only meaningful in the online path; it is
// never resolved during recovery.
type PrincipalState struct {
	Generation uint64
	Revoked    bool
	Allowed    map[Operation]bool
}

// Authority is a small in-memory reference policy and credential store.  It
// is useful for embedding and for tests; production hosts may implement
// Authorizer against their own policy service.
type Authority struct {
	mu         sync.RWMutex
	policy     []byte
	policyHash Digest
	principals map[PrincipalID]PrincipalState
}

func NewAuthority(policy []byte) *Authority {
	a := &Authority{principals: make(map[PrincipalID]PrincipalState)}
	a.SetPolicy(policy)
	return a
}

func (a *Authority) SetPolicy(policy []byte) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.policy = append([]byte(nil), policy...)
	a.policyHash = DigestBytes(a.policy)
}

func (a *Authority) PolicyDigest() Digest {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.policyHash
}

// Grant sets a principal's current credential generation and permitted
// operations.  Granting a new generation immediately invalidates the old one.
func (a *Authority) Grant(id PrincipalID, generation uint64, ops ...Operation) {
	a.mu.Lock()
	defer a.mu.Unlock()
	allowed := make(map[Operation]bool, len(ops))
	for _, op := range ops {
		allowed[op] = true
	}
	a.principals[id] = PrincipalState{
		Generation: generation,
		Allowed:    allowed,
	}
}

func (a *Authority) GrantAll(id PrincipalID, generation uint64) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.principals[id] = PrincipalState{
		Generation: generation,
		Allowed:    map[Operation]bool{"*": true},
	}
}

// Revoke marks a principal as revoked without changing its credential
// generation.  Any uncommitted transaction is still rolled back by recovery.
func (a *Authority) Revoke(id PrincipalID) {
	a.mu.Lock()
	defer a.mu.Unlock()
	p := a.principals[id]
	p.Revoked = true
	a.principals[id] = p
}

// Rotate increments a principal's credential generation and returns it.  A
// request asserting the old generation is denied.
func (a *Authority) Rotate(id PrincipalID) uint64 {
	a.mu.Lock()
	defer a.mu.Unlock()
	p := a.principals[id]
	p.Generation++
	p.Revoked = false
	a.principals[id] = p
	return p.Generation
}

// Credentials returns the current generation and revocation state.
func (a *Authority) Credentials(id PrincipalID) (generation uint64, revoked bool, ok bool) {
	a.mu.RLock()
	defer a.mu.RUnlock()
	p, ok := a.principals[id]
	return p.Generation, p.Revoked, ok
}

func (a *Authority) Authorize(_ context.Context, req AuthRequest) (Authorization, error) {
	a.mu.RLock()
	defer a.mu.RUnlock()
	p, ok := a.principals[req.PrincipalID]
	if !ok || p.Revoked {
		return Authorization{}, nil
	}
	if p.Generation != req.CredentialGeneration {
		return Authorization{
			Allowed:                 false,
			PolicyDigest:            a.policyHash,
			HasCredentialGeneration: true,
			CredentialGeneration:    p.Generation,
		}, nil
	}
	allowed := p.Allowed[req.Operation] || p.Allowed["*"]
	return Authorization{
		Allowed:                 allowed,
		PolicyDigest:            a.policyHash,
		HasCredentialGeneration: true,
		CredentialGeneration:    p.Generation,
	}, nil
}

// GenerationMismatchError is returned by helper code that wants to surface a
// stale credential generation explicitly.
func GenerationMismatchError(principal PrincipalID, requested, current uint64) error {
	return fmt.Errorf("netsec: principal %q credential generation %d is stale (current %d)", principal, requested, current)
}
