package netsec

import (
	"sync"
	"time"
)

// ResetID is a high-entropy identifier chosen by the reset originator. It is
// consumed exactly once and is the idempotency key for the commit receipt.
type ResetID string

// LoginID identifies one in-flight login transaction.
type LoginID string

// FamilyID identifies a refresh-token family. A family has a monotonically
// increasing generation; every refresh token is single-use for its generation.
type FamilyID string

// ResetState is the lifecycle state of a reset transaction.
type ResetState uint8

const (
	ResetPending ResetState = iota
	ResetCommitted
	ResetCanceled
	ResetExpired
)

func (s ResetState) String() string {
	switch s {
	case ResetCommitted:
		return "committed"
	case ResetCanceled:
		return "canceled"
	case ResetExpired:
		return "expired"
	default:
		return "pending"
	}
}

// LoginState is the lifecycle state of a login transaction.
type LoginState uint8

const (
	LoginPending LoginState = iota
	LoginCompleted
	LoginCanceled
	LoginExpired
)

func (s LoginState) String() string {
	switch s {
	case LoginCompleted:
		return "completed"
	case LoginCanceled:
		return "canceled"
	case LoginExpired:
		return "expired"
	default:
		return "pending"
	}
}

// ResetTransaction is the caller-visible binding for a pending reset. The
// service treats the stored record, not caller-supplied copies, as authoritative
// at commit time.
type ResetTransaction struct {
	AccountID                 AccountID
	ResetID                   ResetID
	StartCredentialEpoch      uint64
	StartSessionEpoch         uint64
	StartRecoveryPolicyDigest PolicyDigest
	RecoveryProofDigest       [32]byte
	EvidenceDigest            [32]byte
	TargetPasswordParams      PasswordParams
	Deadline                  time.Time
	Methods                   []RecoveryMethod
}

type resetRecord struct {
	transaction ResetTransaction
	state       ResetState
	receipt     *ResetReceipt
	createdAt   time.Time
	committedAt time.Time
}

// ResetInfo is a read-only snapshot used by hosts and tests.
type ResetInfo struct {
	AccountID                 AccountID
	ResetID                   ResetID
	StartCredentialEpoch      uint64
	StartSessionEpoch         uint64
	StartRecoveryPolicyDigest PolicyDigest
	RecoveryProofDigest       [32]byte
	EvidenceDigest            [32]byte
	TargetPasswordParams      PasswordParams
	Deadline                  time.Time
	Methods                   []RecoveryMethod
	State                     ResetState
	Receipt                   *ResetReceipt
	CreatedAt                 time.Time
	CommittedAt               time.Time
}

// ResetReceipt is the durable proof that a reset committed exactly once. A
// retry with the same reset ID and same target password returns this receipt
// without rotating epochs again.
type ResetReceipt struct {
	AccountID            AccountID
	ResetID              ResetID
	CredentialEpoch      uint64
	SessionEpoch         uint64
	PolicyDigest         PolicyDigest
	PasswordFingerprint  [32]byte
	TargetPasswordParams PasswordParams
	CommittedAt          time.Time
	RevokedFamilyIDs     []FamilyID
	CanceledLoginIDs     []LoginID
	CanceledResetIDs     []ResetID
	Replayed             bool
}

func (r *ResetReceipt) clone() *ResetReceipt {
	if r == nil {
		return nil
	}
	out := *r
	out.RevokedFamilyIDs = append([]FamilyID(nil), r.RevokedFamilyIDs...)
	out.CanceledLoginIDs = append([]LoginID(nil), r.CanceledLoginIDs...)
	out.CanceledResetIDs = append([]ResetID(nil), r.CanceledResetIDs...)
	return &out
}

// LoginTransaction binds a completed credential check to the epochs observed
// when the check began.
type LoginTransaction struct {
	ID                   LoginID
	AccountID            AccountID
	StartCredentialEpoch uint64
	StartSessionEpoch    uint64
	PasswordHashDigest   [32]byte
	Deadline             time.Time
	CreatedAt            time.Time
}

type loginRecord struct {
	transaction LoginTransaction
	state       LoginState
	familyID    FamilyID
	generation  uint64
	completedAt time.Time
}

// LoginInfo is a read-only snapshot of a login transaction.
type LoginInfo struct {
	ID                   LoginID
	AccountID            AccountID
	StartCredentialEpoch uint64
	StartSessionEpoch    uint64
	Deadline             time.Time
	State                LoginState
	FamilyID             FamilyID
	Generation           uint64
	CreatedAt            time.Time
	CompletedAt          time.Time
}

// RefreshFamily is the durable state of one token family.
type RefreshFamily struct {
	ID          FamilyID
	AccountID   AccountID
	Generation  uint64
	Revoked     bool
	RevokedAt   time.Time
	RevokedWhy  string
	CreatedAt   time.Time
	LastRotated time.Time
}

// MemoryStore is a linearizable reference store. One mutex guards every
// account, policy, reset, login, and token-family record. The service performs
// all reset, login-completion, and refresh mutations while holding it, which
// gives each operation a single linearization point.
type MemoryStore struct {
	mu             sync.Mutex
	accounts       map[AccountID]*Account
	policies       map[PolicyDigest]RecoveryPolicy
	resets         map[ResetID]*resetRecord
	logins         map[LoginID]*loginRecord
	families       map[FamilyID]*RefreshFamily
	fingerprintKey [32]byte
	fingerprintSet bool
}

// NewMemoryStore creates an empty store.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		accounts: make(map[AccountID]*Account),
		policies: make(map[PolicyDigest]RecoveryPolicy),
		resets:   make(map[ResetID]*resetRecord),
		logins:   make(map[LoginID]*loginRecord),
		families: make(map[FamilyID]*RefreshFamily),
	}
}

// GetAccount returns a deep copy of the account record.
func (s *MemoryStore) GetAccount(id AccountID) (Account, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	account, ok := s.accounts[id]
	if !ok {
		return Account{}, false
	}
	return account.clone(), true
}

// GetPolicy returns the policy registered for a digest.
func (s *MemoryStore) GetPolicy(digest PolicyDigest) (RecoveryPolicy, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	policy, ok := s.policies[digest]
	if !ok {
		return RecoveryPolicy{}, false
	}
	policy.Required = append([]RecoveryMethod(nil), policy.Required...)
	return policy, true
}

// GetReset returns a snapshot of a reset record.
func (s *MemoryStore) GetReset(id ResetID) (ResetInfo, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.getResetLocked(id)
}

func (s *MemoryStore) getResetLocked(id ResetID) (ResetInfo, bool) {
	record, ok := s.resets[id]
	if !ok {
		return ResetInfo{}, false
	}
	return resetInfo(record), true
}

func resetInfo(record *resetRecord) ResetInfo {
	return ResetInfo{
		AccountID:                 record.transaction.AccountID,
		ResetID:                   record.transaction.ResetID,
		StartCredentialEpoch:      record.transaction.StartCredentialEpoch,
		StartSessionEpoch:         record.transaction.StartSessionEpoch,
		StartRecoveryPolicyDigest: record.transaction.StartRecoveryPolicyDigest,
		RecoveryProofDigest:       record.transaction.RecoveryProofDigest,
		EvidenceDigest:            record.transaction.EvidenceDigest,
		TargetPasswordParams:      record.transaction.TargetPasswordParams,
		Deadline:                  record.transaction.Deadline,
		Methods:                   append([]RecoveryMethod(nil), record.transaction.Methods...),
		State:                     record.state,
		Receipt:                   record.receipt.clone(),
		CreatedAt:                 record.createdAt,
		CommittedAt:               record.committedAt,
	}
}

// GetLogin returns a snapshot of a login transaction.
func (s *MemoryStore) GetLogin(id LoginID) (LoginInfo, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	record, ok := s.logins[id]
	if !ok {
		return LoginInfo{}, false
	}
	return LoginInfo{
		ID:                   record.transaction.ID,
		AccountID:            record.transaction.AccountID,
		StartCredentialEpoch: record.transaction.StartCredentialEpoch,
		StartSessionEpoch:    record.transaction.StartSessionEpoch,
		Deadline:             record.transaction.Deadline,
		State:                record.state,
		FamilyID:             record.familyID,
		Generation:           record.generation,
		CreatedAt:            record.transaction.CreatedAt,
		CompletedAt:          record.completedAt,
	}, true
}

// GetRefreshFamily returns a snapshot of a refresh-token family.
func (s *MemoryStore) GetRefreshFamily(id FamilyID) (RefreshFamily, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	family, ok := s.families[id]
	if !ok {
		return RefreshFamily{}, false
	}
	return *family, true
}

// clone makes a complete deep copy. It is used by deterministic model tests.
func (s *MemoryStore) clone() *MemoryStore {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := NewMemoryStore()
	for id, account := range s.accounts {
		cloneAccount := account.clone()
		out.accounts[id] = &cloneAccount
	}
	for digest, policy := range s.policies {
		policy.Required = append([]RecoveryMethod(nil), policy.Required...)
		out.policies[digest] = policy
	}
	for id, record := range s.resets {
		cloneRecord := *record
		cloneRecord.transaction.Methods = append([]RecoveryMethod(nil), record.transaction.Methods...)
		cloneRecord.receipt = record.receipt.clone()
		out.resets[id] = &cloneRecord
	}
	for id, record := range s.logins {
		cloneRecord := *record
		out.logins[id] = &cloneRecord
	}
	for id, family := range s.families {
		cloneFamily := *family
		out.families[id] = &cloneFamily
	}
	out.fingerprintKey = s.fingerprintKey
	out.fingerprintSet = s.fingerprintSet
	return out
}
