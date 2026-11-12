package netsec

import (
	"context"
	"fmt"
	"sync"
)

// MemoryCommit is an in-memory Commit implementation used as a test double and
// reference implementation. Signer is empty for unsigned commits; otherwise it
// holds the identity a successful verification should report.
type MemoryCommit struct {
	OID    OID
	Parent []OID
	Bytes  int64
	Signer string
	// BadSignature marks a commit that is signed but whose signature fails
	// verification. MemoryVerifier reports it as an invalid signature.
	BadSignature bool
}

// ID implements Commit.
func (c *MemoryCommit) ID() OID { return c.OID }

// Parents implements Commit, returning all parents (including merge parents).
func (c *MemoryCommit) Parents() []OID { return c.Parent }

// Size implements Commit.
func (c *MemoryCommit) Size() int64 { return c.Bytes }

// MemoryRepository is an in-memory object database implementing Repository for
// tests and examples.
type MemoryRepository struct {
	mu      sync.RWMutex
	Commits map[OID]*MemoryCommit
}

// NewMemoryRepository creates an empty MemoryRepository.
func NewMemoryRepository() *MemoryRepository {
	return &MemoryRepository{Commits: make(map[OID]*MemoryCommit)}
}

// Add stores c and returns it for fluent test construction.
func (r *MemoryRepository) Add(c *MemoryCommit) *MemoryCommit {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.Commits[c.OID] = c
	return c
}

// LookupCommit implements Repository.
func (r *MemoryRepository) LookupCommit(_ context.Context, id OID) (Commit, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	c, ok := r.Commits[id]
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrObjectNotFound, id)
	}
	return c, nil
}

// MemoryVerifier is a VerifySignature callback for tests. Unsigned commits
// yield ErrUnsignedCommit; badly signed commits report a verification error;
// well-formed commits report their signer identity.
func MemoryVerifier(_ context.Context, c Commit) (string, error) {
	mc, ok := c.(*MemoryCommit)
	if !ok {
		return "", fmt.Errorf("netsec: MemoryVerifier requires *MemoryCommit, got %T", c)
	}
	if mc.BadSignature {
		return "", fmt.Errorf("signature on commit %s failed verification", mc.OID)
	}
	if mc.Signer == "" {
		return "", ErrUnsignedCommit
	}
	return mc.Signer, nil
}
