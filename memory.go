package netsec

import (
	"context"
	"errors"
	"sync"
)

// idemRecord is one stored idempotency entry.
type idemRecord struct {
	fingerprint []byte
	result      Result
}

// memoryTx buffers one registration's writes. Reads fall through to the store
// snapshot; uniqueness is re-checked atomically at Commit so overlapping
// registrations race for the credential id instead of being serialized.
type memoryTx struct {
	store        *MemoryStore
	ctx          context.Context
	done         bool
	outcomeState commitOutcome

	idemLocal map[string]idemRecord
	credLocal map[string]credBinding
	challTomb map[string]struct{}
}

type credBinding struct {
	accountID string
	publicKey []byte
}

// MemoryStore is an in-memory [Store] for hosts and tests. A SQL deployment
// implements the same interface over a serializable *sql.Tx with unique
// constraints on credential_id and idempotency_key.
type MemoryStore struct {
	mu          sync.RWMutex
	commitMu    sync.Mutex
	challenges  map[string]struct{}
	credentials map[string]credBinding
	idempotency map[string]idemRecord
}

// NewMemoryStore creates an empty store.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		challenges:  make(map[string]struct{}),
		credentials: make(map[string]credBinding),
		idempotency: make(map[string]idemRecord),
	}
}

// SaveChallenge records a single-use registration challenge issued by the
// host. Saving the same challenge twice is a no-op.
func (m *MemoryStore) SaveChallenge(challenge []byte) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.challenges[string(challenge)] = struct{}{}
}

// CredentialCount returns the number of occupied credential ids. It is
// intended for tests.
func (m *MemoryStore) CredentialCount() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.credentials)
}

// LookupAccount returns the account bound to a credential id, or "" when the
// id is unoccupied. It is intended for tests.
func (m *MemoryStore) LookupAccount(credentialID []byte) string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.credentials[string(credentialID)].accountID
}

// Begin opens an optimistic transaction.
func (m *MemoryStore) Begin(ctx context.Context) (Tx, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return &memoryTx{
		store:     m,
		ctx:       ctx,
		idemLocal: make(map[string]idemRecord),
		credLocal: make(map[string]credBinding),
		challTomb: make(map[string]struct{}),
	}, nil
}

func (t *memoryTx) LookupIdempotency(_ context.Context, key []byte) ([]byte, *Result, error) {
	if rec, ok := t.idemLocal[string(key)]; ok {
		result := rec.result
		return append([]byte(nil), rec.fingerprint...), &result, nil
	}
	t.store.mu.RLock()
	defer t.store.mu.RUnlock()
	rec, ok := t.store.idempotency[string(key)]
	if !ok {
		return nil, nil, nil
	}
	result := rec.result
	return append([]byte(nil), rec.fingerprint...), &result, nil
}

func (t *memoryTx) SaveIdempotency(_ context.Context, key, fingerprint []byte, result Result) error {
	k := string(key)
	if _, exists := t.idemLocal[k]; exists {
		return ErrIdempotencyConflict
	}
	t.store.mu.RLock()
	_, exists := t.store.idempotency[k]
	t.store.mu.RUnlock()
	if exists {
		return ErrIdempotencyConflict
	}
	t.idemLocal[k] = idemRecord{
		fingerprint: append([]byte(nil), fingerprint...),
		result:      result,
	}
	return nil
}

func (t *memoryTx) LookupCredential(_ context.Context, credentialID []byte) (string, []byte, error) {
	if binding, ok := t.credLocal[string(credentialID)]; ok {
		return binding.accountID, append([]byte(nil), binding.publicKey...), nil
	}
	t.store.mu.RLock()
	defer t.store.mu.RUnlock()
	binding, ok := t.store.credentials[string(credentialID)]
	if !ok {
		return "", nil, nil
	}
	return binding.accountID, append([]byte(nil), binding.publicKey...), nil
}

func (t *memoryTx) SaveCredential(_ context.Context, credentialID []byte, accountID string, publicKey []byte) error {
	k := string(credentialID)
	if _, exists := t.credLocal[k]; exists {
		return ErrCredentialAccountConflict
	}
	t.store.mu.RLock()
	_, exists := t.store.credentials[k]
	t.store.mu.RUnlock()
	if exists {
		return ErrCredentialAccountConflict
	}
	t.credLocal[k] = credBinding{
		accountID: accountID,
		publicKey: append([]byte(nil), publicKey...),
	}
	return nil
}

func (t *memoryTx) ConsumeChallenge(_ context.Context, challenge []byte) error {
	k := string(challenge)
	if _, consumed := t.challTomb[k]; consumed {
		return ErrChallengeConsumed
	}
	t.store.mu.RLock()
	_, available := t.store.challenges[k]
	t.store.mu.RUnlock()
	if !available {
		return ErrChallengeConsumed
	}
	t.challTomb[k] = struct{}{}
	return nil
}

// commitOutcome reports how a transaction's buffered credential write was
// resolved against the live store.
type commitOutcome int

const (
	commitCreated commitOutcome = iota
	commitReplayed
)

// Commit applies all buffered writes in one critical section. The credential
// id is adjudicated atomically: an unoccupied id is occupied, an identical
// existing binding resolves as an idempotent replay, and any other binding is
// a definitive conflict. A SQL deployment obtains the same guarantee from row
// locks plus a unique index on credential_id.
func (t *memoryTx) Commit() (bool, error) {
	if t.done {
		return false, nil
	}
	if err := t.ctx.Err(); err != nil {
		return false, err
	}
	store := t.store
	// Serialize commits so the uniqueness verdict for a credential id is made
	// by exactly one transaction at a time. A SQL deployment obtains the same
	// guarantee from row locks plus a unique index on credential_id instead of
	// this mutex.
	store.commitMu.Lock()
	defer store.commitMu.Unlock()

	store.mu.Lock()
	defer store.mu.Unlock()

	for key := range t.idemLocal {
		if _, exists := store.idempotency[key]; exists {
			return false, errors.Join(errCommitRace, ErrIdempotencyConflict)
		}
	}

	// Adjudicate every buffered credential against the live binding.
	for id, binding := range t.credLocal {
		if winner, exists := store.credentials[id]; exists {
			if winner.accountID != binding.accountID {
				return false, ErrCredentialAccountConflict
			}
			if !constantTimeEqual(winner.publicKey, binding.publicKey) {
				return false, ErrCredentialKeyConflict
			}
			// Identical binding already owned: replay, nothing to occupy.
			t.outcomeState = commitReplayed
			delete(t.credLocal, id)
			continue
		}
		// A genuinely new credential requires its one-time challenge.
		for challenge := range t.challTomb {
			if _, available := store.challenges[challenge]; !available {
				return false, errors.Join(errCommitRace, ErrChallengeConsumed)
			}
		}
	}

	for challenge := range t.challTomb {
		delete(store.challenges, challenge)
	}
	for id, binding := range t.credLocal {
		store.credentials[id] = binding
	}
	for key, rec := range t.idemLocal {
		store.idempotency[key] = rec
	}
	t.done = true
	return t.outcomeState == commitCreated, nil
}

func (t *memoryTx) Rollback() error {
	t.done = true
	return nil
}
