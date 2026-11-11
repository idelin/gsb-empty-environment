package fedid

import "sync"

// Tx is a unit of work over the identity store.
type Tx interface {
	GetAccount(id string) (Account, bool)
	GetIdentity(key IdentityKey) (Identity, bool)
	PutAccount(a Account)
	PutIdentity(id Identity)
	// ConsumeProof atomically marks the token consumed. It returns
	// ErrProofInvalid if the token is unknown or already consumed.
	ConsumeProof(token string) (Proof, error)
	Commit() error
	Rollback()
}

// Store begins transactions.
type Store interface {
	Begin() Tx
}

// MemStore is an in-memory transaction double ("事务替身") used in tests
// and as a reference implementation. Proof consumption is applied
// immediately under a store-wide lock so concurrent transactions cannot
// consume the same proof twice; rollback restores consumed proofs.
type MemStore struct {
	mu         sync.Mutex
	accounts   map[string]Account
	identities map[IdentityKey]Identity
	proofs     map[string]Proof

	// FailCommit simulates a migration/commit failure for tests.
	FailCommit bool
}

func NewMemStore() *MemStore {
	return &MemStore{
		accounts:   map[string]Account{},
		identities: map[IdentityKey]Identity{},
		proofs:     map[string]Proof{},
	}
}

// SeedProof registers a one-time proof token.
func (s *MemStore) SeedProof(p Proof) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.proofs[p.Token] = p
}

// ProofState reports whether a token exists and is consumed (test helper).
func (s *MemStore) ProofState(token string) (consumed, ok bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, ok := s.proofs[token]
	return p.Consumed, ok
}

func (s *MemStore) Begin() Tx {
	return &memTx{
		store:      s,
		accounts:   map[string]Account{},
		identities: map[IdentityKey]Identity{},
	}
}

type memTx struct {
	store      *MemStore
	accounts   map[string]Account
	identities map[IdentityKey]Identity
	consumed   []string
	done       bool
}

func (t *memTx) GetAccount(id string) (Account, bool) {
	if a, ok := t.accounts[id]; ok {
		return a, true
	}
	t.store.mu.Lock()
	defer t.store.mu.Unlock()
	a, ok := t.store.accounts[id]
	return a, ok
}

func (t *memTx) GetIdentity(key IdentityKey) (Identity, bool) {
	if id, ok := t.identities[key]; ok {
		return id, true
	}
	t.store.mu.Lock()
	defer t.store.mu.Unlock()
	id, ok := t.store.identities[key]
	return id, ok
}

func (t *memTx) PutAccount(a Account)    { t.accounts[a.ID] = a }
func (t *memTx) PutIdentity(id Identity) { t.identities[id.Key] = id }

func (t *memTx) ConsumeProof(token string) (Proof, error) {
	t.store.mu.Lock()
	defer t.store.mu.Unlock()
	p, ok := t.store.proofs[token]
	if !ok || p.Consumed {
		return Proof{}, ErrProofInvalid
	}
	p.Consumed = true
	t.store.proofs[token] = p
	t.consumed = append(t.consumed, token)
	return p, nil
}

func (t *memTx) Commit() error {
	t.store.mu.Lock()
	defer t.store.mu.Unlock()
	if t.store.FailCommit {
		return ErrCommitFailed
	}
	// Commit-time uniqueness guard: two concurrent transactions may both
	// stage the same identity key; only the first commit wins.
	for key, id := range t.identities {
		if existing, ok := t.store.identities[key]; ok && existing.AccountID != id.AccountID {
			return ErrIdentityBound
		}
	}
	for key, id := range t.identities {
		t.store.identities[key] = id
	}
	for id, a := range t.accounts {
		t.store.accounts[id] = a
	}
	t.done = true
	return nil
}

func (t *memTx) Rollback() {
	t.store.mu.Lock()
	defer t.store.mu.Unlock()
	for _, token := range t.consumed {
		if p, ok := t.store.proofs[token]; ok {
			p.Consumed = false
			t.store.proofs[token] = p
		}
	}
	t.consumed = nil
	t.done = true
}

// Direct read helpers for assertions in tests.
func (s *MemStore) Account(id string) (Account, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	a, ok := s.accounts[id]
	return a, ok
}

func (s *MemStore) Identity(key IdentityKey) (Identity, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	id, ok := s.identities[key]
	return id, ok
}
