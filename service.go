package netsec

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"math"
	"sort"
	"sync"
	"time"
)

// Clock supplies the current time. Tests use a fixed clock for deterministic
// deadlines and token expiry.
type Clock interface {
	Now() time.Time
}

type realClock struct{}

func (realClock) Now() time.Time { return time.Now() }

// AlarmCode identifies a security condition that needs operator attention.
type AlarmCode string

const (
	AlarmSessionEpochExhausted     AlarmCode = "session_epoch_exhausted"
	AlarmCredentialEpochExhausted  AlarmCode = "credential_epoch_exhausted"
	AlarmFamilyGenerationExhausted AlarmCode = "refresh_generation_exhausted"
)

// Alarm is emitted after the store lock is released. Sinks must not call back
// into the service synchronously.
type Alarm struct {
	Code      AlarmCode
	AccountID AccountID
	ResetID   ResetID
	FamilyID  FamilyID
	Detail    string
	At        time.Time
}

// AlarmFunc receives security alarms. It may run concurrently with other
// callers; the service does not serialize it.
type AlarmFunc func(Alarm)

// Config wires a Service to its verifier, hasher, keys, clock, and store.
type Config struct {
	Store    *MemoryStore
	Verifier RecoveryVerifier
	Hasher   PasswordHasher
	Policy   PasswordPolicy
	Params   PasswordParams
	Signer   *TokenSigner
	Lifetime TokenLifetime
	Clock    Clock
	Rand     io.Reader
	OnAlarm  AlarmFunc

	ResetTTL time.Duration
	LoginTTL time.Duration

	// PasswordFingerprintKey should be stable across restarts so an idempotent
	// reset retry recognises the same target password. If nil, the store keeps
	// a random key for the lifetime of the store.
	PasswordFingerprintKey []byte
}

// Service is the authentication host's reset/login/token facade.
type Service struct {
	store       *MemoryStore
	verifier    RecoveryVerifier
	hasher      PasswordHasher
	policy      PasswordPolicy
	params      PasswordParams
	signer      *TokenSigner
	lifetime    TokenLifetime
	clock       Clock
	onAlarm     AlarmFunc
	random      io.Reader
	fingerprint []byte
	resetTTL    time.Duration
	loginTTL    time.Duration
}

// NewService validates configuration and constructs the service.
func NewService(cfg Config) (*Service, error) {
	if cfg.Verifier == nil {
		return nil, errors.New("netsec: recovery verifier is required")
	}
	store := cfg.Store
	if store == nil {
		store = NewMemoryStore()
	}
	hasher := cfg.Hasher
	if hasher == nil {
		hasher = PBKDF2Hasher{}
	}
	policy := cfg.Policy
	defaultPolicy := DefaultPasswordPolicy()
	if policy.MinIterations == 0 {
		policy.MinIterations = defaultPolicy.MinIterations
	}
	if policy.MinSaltBytes == 0 {
		policy.MinSaltBytes = defaultPolicy.MinSaltBytes
	}
	if policy.MinKeyBytes == 0 {
		policy.MinKeyBytes = defaultPolicy.MinKeyBytes
	}
	if policy.MinPasswordBytes == 0 {
		policy.MinPasswordBytes = defaultPolicy.MinPasswordBytes
	}
	params := cfg.Params
	if params == (PasswordParams{}) {
		params = DefaultPasswordParams(policy)
	}
	if params.Algorithm == "" {
		params.Algorithm = PBKDF2SHA256
	}
	if err := params.Validate(policy); err != nil {
		return nil, err
	}
	clock := cfg.Clock
	if clock == nil {
		clock = realClock{}
	}
	random := cfg.Rand
	if random == nil {
		random = rand.Reader
	}
	random = &lockedReader{reader: random}
	lifetime := cfg.Lifetime
	defaults := DefaultTokenLifetime()
	if lifetime.Access <= 0 {
		lifetime.Access = defaults.Access
	}
	if lifetime.Refresh <= 0 {
		lifetime.Refresh = defaults.Refresh
	}
	resetTTL := cfg.ResetTTL
	if resetTTL <= 0 {
		resetTTL = 10 * time.Minute
	}
	loginTTL := cfg.LoginTTL
	if loginTTL <= 0 {
		loginTTL = 5 * time.Minute
	}
	signer := cfg.Signer
	if signer == nil {
		key := make([]byte, 32)
		if _, err := io.ReadFull(random, key); err != nil {
			return nil, err
		}
		var err error
		signer, err = NewTokenSigner(key)
		if err != nil {
			return nil, err
		}
	}
	fingerprint := cfg.PasswordFingerprintKey
	if len(fingerprint) == 0 {
		store.mu.Lock()
		if !store.fingerprintSet {
			key := make([]byte, 32)
			if _, err := io.ReadFull(random, key); err != nil {
				store.mu.Unlock()
				return nil, err
			}
			copy(store.fingerprintKey[:], key)
			store.fingerprintSet = true
		}
		fingerprint = append([]byte(nil), store.fingerprintKey[:]...)
		store.mu.Unlock()
	} else {
		sum := sha256.Sum256(append([]byte("netsec/fingerprint-key/v1\x00"), fingerprint...))
		fingerprint = append([]byte(nil), sum[:]...)
		store.mu.Lock()
		if store.fingerprintSet && subtle.ConstantTimeCompare(store.fingerprintKey[:], fingerprint) != 1 {
			store.mu.Unlock()
			return nil, ErrFingerprintKeyMismatch
		}
		copy(store.fingerprintKey[:], fingerprint)
		store.fingerprintSet = true
		store.mu.Unlock()
	}
	return &Service{
		store:       store,
		verifier:    cfg.Verifier,
		hasher:      hasher,
		policy:      policy,
		params:      params,
		signer:      signer,
		lifetime:    lifetime,
		clock:       clock,
		onAlarm:     cfg.OnAlarm,
		random:      random,
		fingerprint: fingerprint,
		resetTTL:    resetTTL,
		loginTTL:    loginTTL,
	}, nil
}

// Store exposes the reference store for read-only inspection and for restart
// tests. Mutations must go through Service so they keep the atomic contract.
func (s *Service) Store() *MemoryStore { return s.store }

// DefaultParams returns the service's normal password work factors.
func (s *Service) DefaultParams() PasswordParams { return s.params }

// RegisterRecoveryPolicy canonicalizes and registers a policy without changing
// any account's current digest.
func (s *Service) RegisterRecoveryPolicy(policy RecoveryPolicy) (PolicyDigest, error) {
	canonical, err := policy.Canonical()
	if err != nil {
		return PolicyDigest{}, err
	}
	digest, err := canonical.Digest()
	if err != nil {
		return PolicyDigest{}, err
	}
	s.store.mu.Lock()
	defer s.store.mu.Unlock()
	s.store.policies[digest] = canonical
	return digest, nil
}

// SetRecoveryPolicy installs a stricter policy as the account's *current*
// policy. Pending resets are re-evaluated against it at commit time.
func (s *Service) SetRecoveryPolicy(accountID AccountID, policy RecoveryPolicy) (PolicyDigest, error) {
	canonical, err := policy.Canonical()
	if err != nil {
		return PolicyDigest{}, err
	}
	digest, err := canonical.Digest()
	if err != nil {
		return PolicyDigest{}, err
	}
	s.store.mu.Lock()
	defer s.store.mu.Unlock()
	account, ok := s.store.accounts[accountID]
	if !ok {
		return PolicyDigest{}, ErrAccountNotFound
	}
	s.store.policies[digest] = canonical
	account.RecoveryPolicyDigest = digest
	account.UpdatedAt = s.now()
	return digest, nil
}

// CreateAccount hashes the initial password outside the store lock and then
// inserts the account and policy atomically.
func (s *Service) CreateAccount(ctx context.Context, id AccountID, password string, params PasswordParams, policy RecoveryPolicy) error {
	if id == "" {
		return errors.New("netsec: empty account id")
	}
	canonical, err := policy.Canonical()
	if err != nil {
		return err
	}
	digest, err := canonical.Digest()
	if err != nil {
		return err
	}
	resolved := s.resolveParams(params)
	prepared, err := s.PreparePassword(ctx, password, resolved)
	if err != nil {
		return err
	}
	now := s.now()
	s.store.mu.Lock()
	defer s.store.mu.Unlock()
	if _, exists := s.store.accounts[id]; exists {
		return ErrAccountExists
	}
	account := &Account{
		ID:                   id,
		CredentialEpoch:      0,
		SessionEpoch:         0,
		PasswordHash:         prepared.hash.Clone(),
		RecoveryPolicyDigest: digest,
		Status:               AccountActive,
		CreatedAt:            now,
		UpdatedAt:            now,
	}
	s.store.accounts[id] = account
	s.store.policies[digest] = canonical
	return nil
}

// PreparePassword performs the expensive, isolated hash step. It never holds
// the store lock and never touches account state.
func (s *Service) PreparePassword(ctx context.Context, password string, params PasswordParams) (*PreparedPassword, error) {
	resolved := s.resolveParams(params)
	if err := resolved.Validate(s.policy); err != nil {
		return nil, err
	}
	passwordBytes := []byte(password)
	defer zero(passwordBytes)
	if len(passwordBytes) < s.policy.MinPasswordBytes {
		return nil, fmt.Errorf("%w: got %d bytes, need %d", ErrWeakPassword, len(passwordBytes), s.policy.MinPasswordBytes)
	}
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}
	hash, err := s.hasher.Hash(ctx, passwordBytes, resolved)
	if err != nil {
		return nil, err
	}
	if hash.Algorithm != resolved.Algorithm || hash.Params != resolved {
		return nil, errors.New("netsec: hasher returned mismatched parameters")
	}
	if err := hash.Valid(s.policy); err != nil {
		return nil, err
	}
	return &PreparedPassword{
		hash:        hash.Clone(),
		params:      resolved,
		fingerprint: passwordFingerprint(s.fingerprint, passwordBytes),
	}, nil
}

// BeginResetRequest starts a reset transaction. Proof is verified by the
// configured callback. The account epochs are captured *before* verification so
// a reset that linearizes during proof checking makes this transaction stale.
type BeginResetRequest struct {
	AccountID            AccountID
	ResetID              ResetID
	TargetPasswordParams PasswordParams
	Proof                []byte
	Deadline             time.Time
}

// BeginReset binds a verified proof to the account's current epochs and policy.
// The proof's methods are evaluated against the account's current policy only at
// commit, never against the policy snapshot captured here.
func (s *Service) BeginReset(ctx context.Context, request BeginResetRequest) (*ResetTransaction, error) {
	if request.AccountID == "" {
		return nil, errors.New("netsec: empty account id")
	}
	params := s.resolveParams(request.TargetPasswordParams)
	if err := params.Validate(s.policy); err != nil {
		return nil, err
	}
	now := s.now()
	deadline := request.Deadline
	if deadline.IsZero() {
		deadline = now.Add(s.resetTTL)
	}
	if !deadline.After(now) {
		return nil, ErrDeadlineExceeded
	}
	s.store.mu.Lock()
	account, ok := s.store.accounts[request.AccountID]
	if !ok {
		s.store.mu.Unlock()
		return nil, ErrAccountNotFound
	}
	if account.Status != AccountActive {
		s.store.mu.Unlock()
		return nil, ErrAccountInactive
	}
	startCredential := account.CredentialEpoch
	startSession := account.SessionEpoch
	startPolicy := account.RecoveryPolicyDigest
	s.store.mu.Unlock()

	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}
	proof := append([]byte(nil), request.Proof...)
	defer zero(proof)
	verified, err := s.verifier(ctx, request.AccountID, proof)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrRecoveryProofRejected, err)
	}
	methods := canonicalMethods(verified.Methods)
	if len(methods) == 0 {
		return nil, fmt.Errorf("%w: verifier returned no methods", ErrRecoveryProofRejected)
	}
	evidence := verified.EvidenceDigest
	if evidence == ([32]byte{}) {
		evidence = sha256.Sum256(proof)
	}
	digest := proofDigest(request.AccountID, methods, evidence)

	s.store.mu.Lock()
	defer s.store.mu.Unlock()
	if _, ok := s.store.accounts[request.AccountID]; !ok {
		return nil, ErrAccountNotFound
	}
	resetID := request.ResetID
	if resetID == "" {
		generated, err := s.newID("rst_", 16)
		if err != nil {
			return nil, err
		}
		resetID = ResetID(generated)
	}
	if existing, ok := s.store.resets[resetID]; ok {
		transaction := existing.transaction
		deadlineMatches := request.Deadline.IsZero() || transaction.Deadline.Equal(deadline)
		if transaction.AccountID != request.AccountID ||
			transaction.TargetPasswordParams != params ||
			!deadlineMatches ||
			transaction.RecoveryProofDigest != digest {
			return nil, ErrResetIDReuse
		}
		switch existing.state {
		case ResetCommitted, ResetPending:
			transaction.Methods = append([]RecoveryMethod(nil), transaction.Methods...)
			return &transaction, nil
		case ResetExpired:
			return nil, ErrDeadlineExceeded
		default:
			return nil, ErrResetSuperseded
		}
	}
	record := &resetRecord{
		transaction: ResetTransaction{
			AccountID:                 request.AccountID,
			ResetID:                   resetID,
			StartCredentialEpoch:      startCredential,
			StartSessionEpoch:         startSession,
			StartRecoveryPolicyDigest: startPolicy,
			RecoveryProofDigest:       digest,
			EvidenceDigest:            evidence,
			TargetPasswordParams:      params,
			Deadline:                  deadline,
			Methods:                   append([]RecoveryMethod(nil), methods...),
		},
		state:     ResetPending,
		createdAt: now,
	}
	s.store.resets[resetID] = record
	transaction := record.transaction
	transaction.Methods = append([]RecoveryMethod(nil), transaction.Methods...)
	return &transaction, nil
}

// LookupReset reloads a transaction after a process restart.
func (s *Service) LookupReset(id ResetID) (*ResetTransaction, ResetState, error) {
	s.store.mu.Lock()
	defer s.store.mu.Unlock()
	record, ok := s.store.resets[id]
	if !ok {
		return nil, ResetPending, ErrUnknownReset
	}
	transaction := record.transaction
	transaction.Methods = append([]RecoveryMethod(nil), transaction.Methods...)
	return &transaction, record.state, nil
}

// LookupReceipt returns the original durable receipt for a committed reset.
// This is the recovery path when the commit response was lost and the recovery
// proof has already been consumed: no proof is re-verified and no epoch moves.
func (s *Service) LookupReceipt(id ResetID) (*ResetReceipt, bool) {
	s.store.mu.Lock()
	defer s.store.mu.Unlock()
	record, ok := s.store.resets[id]
	if !ok || record.state != ResetCommitted || record.receipt == nil {
		return nil, false
	}
	return record.receipt.clone(), true
}

// CommitResetByID reloads a reset transaction from durable state and commits
// it. A committed reset follows the idempotent replay path.
func (s *Service) CommitResetByID(ctx context.Context, id ResetID, prepared *PreparedPassword) (ResetReceipt, error) {
	transaction, _, err := s.LookupReset(id)
	if err != nil {
		return ResetReceipt{}, err
	}
	return s.CommitReset(ctx, transaction, prepared)
}

// CommitReset is the single linearizable reset commit.
//
// On success it writes the prepared password hash, increments both epochs by
// exactly one, consumes the reset ID, revokes every refresh-token family, and
// cancels every other pending login and reset transaction for the account.
// Any error leaves all authentication state unchanged. A retry of an already
// committed reset with the same target password returns the original receipt
// and does not increment any epoch.
func (s *Service) CommitReset(ctx context.Context, transaction *ResetTransaction, prepared *PreparedPassword) (ResetReceipt, error) {
	if transaction == nil || transaction.ResetID == "" {
		return ResetReceipt{}, errors.New("netsec: nil or incomplete reset transaction")
	}
	s.store.mu.Lock()
	receipt, alarm, err := s.commitResetLocked(ctx, transaction, prepared)
	s.store.mu.Unlock()
	if alarm != nil {
		s.raise(*alarm)
	}
	return receipt, err
}

func (s *Service) commitResetLocked(ctx context.Context, transaction *ResetTransaction, prepared *PreparedPassword) (ResetReceipt, *Alarm, error) {
	record, ok := s.store.resets[transaction.ResetID]
	if !ok {
		return ResetReceipt{}, nil, ErrUnknownReset
	}
	stored := record.transaction
	if stored.AccountID != transaction.AccountID ||
		stored.StartCredentialEpoch != transaction.StartCredentialEpoch ||
		stored.StartSessionEpoch != transaction.StartSessionEpoch ||
		stored.RecoveryProofDigest != transaction.RecoveryProofDigest ||
		stored.TargetPasswordParams != transaction.TargetPasswordParams ||
		!stored.Deadline.Equal(transaction.Deadline) {
		return ResetReceipt{}, nil, ErrResetConflict
	}
	switch record.state {
	case ResetCommitted:
		if prepared == nil {
			return ResetReceipt{}, nil, ErrResetIDReuse
		}
		fingerprint := prepared.Fingerprint()
		if record.receipt == nil ||
			subtle.ConstantTimeCompare(record.receipt.PasswordFingerprint[:], fingerprint[:]) != 1 ||
			record.receipt.TargetPasswordParams != prepared.Params() {
			return ResetReceipt{}, nil, ErrResetIDReuse
		}
		replayed := record.receipt.clone()
		replayed.Replayed = true
		return *replayed, nil, nil
	case ResetCanceled:
		return ResetReceipt{}, nil, ErrResetSuperseded
	case ResetExpired:
		return ResetReceipt{}, nil, ErrDeadlineExceeded
	}
	if err := prepared.valid(s.policy); err != nil {
		return ResetReceipt{}, nil, err
	}
	fingerprint := prepared.Fingerprint()
	if prepared.params != stored.TargetPasswordParams {
		return ResetReceipt{}, nil, ErrPasswordParamsMismatch
	}

	now := s.now()
	if !now.Before(stored.Deadline) {
		return ResetReceipt{}, nil, ErrDeadlineExceeded
	}
	account, ok := s.store.accounts[stored.AccountID]
	if !ok {
		return ResetReceipt{}, nil, ErrAccountNotFound
	}
	if account.Status != AccountActive {
		return ResetReceipt{}, nil, ErrAccountInactive
	}
	if account.SessionEpoch == math.MaxUint64 {
		return ResetReceipt{}, &Alarm{
			Code:      AlarmSessionEpochExhausted,
			AccountID: account.ID,
			ResetID:   stored.ResetID,
			Detail:    "sessionEpoch is at uint64 max; reset refused without wraparound",
			At:        now,
		}, ErrEpochExhausted
	}
	if account.CredentialEpoch == math.MaxUint64 {
		return ResetReceipt{}, &Alarm{
			Code:      AlarmCredentialEpochExhausted,
			AccountID: account.ID,
			ResetID:   stored.ResetID,
			Detail:    "credentialEpoch is at uint64 max; reset refused without wraparound",
			At:        now,
		}, ErrEpochExhausted
	}
	if account.CredentialEpoch != stored.StartCredentialEpoch || account.SessionEpoch != stored.StartSessionEpoch {
		return ResetReceipt{}, nil, ErrEpochConflict
	}
	if proofDigest(stored.AccountID, stored.Methods, stored.EvidenceDigest) != stored.RecoveryProofDigest {
		return ResetReceipt{}, nil, ErrResetConflict
	}
	policy, ok := s.store.policies[account.RecoveryPolicyDigest]
	if !ok {
		return ResetReceipt{}, nil, ErrPolicyUnavailable
	}
	if !policy.SatisfiedBy(stored.Methods) {
		return ResetReceipt{}, nil, ErrRecoveryPolicyUnsatisfied
	}
	select {
	case <-ctx.Done():
		return ResetReceipt{}, nil, ctx.Err()
	default:
	}

	revokedFamilies := make([]FamilyID, 0)
	for id, family := range s.store.families {
		if family.AccountID == account.ID && !family.Revoked {
			revokedFamilies = append(revokedFamilies, id)
		}
	}
	sort.Slice(revokedFamilies, func(i, j int) bool { return revokedFamilies[i] < revokedFamilies[j] })
	canceledLogins := make([]LoginID, 0)
	for id, login := range s.store.logins {
		if login.transaction.AccountID == account.ID && login.state == LoginPending {
			canceledLogins = append(canceledLogins, id)
		}
	}
	sort.Slice(canceledLogins, func(i, j int) bool { return canceledLogins[i] < canceledLogins[j] })
	canceledResets := make([]ResetID, 0)
	for id, other := range s.store.resets {
		if id != stored.ResetID && other.transaction.AccountID == account.ID && other.state == ResetPending {
			canceledResets = append(canceledResets, id)
		}
	}
	sort.Slice(canceledResets, func(i, j int) bool { return canceledResets[i] < canceledResets[j] })

	receipt := ResetReceipt{
		AccountID:            account.ID,
		ResetID:              stored.ResetID,
		CredentialEpoch:      account.CredentialEpoch + 1,
		SessionEpoch:         account.SessionEpoch + 1,
		PolicyDigest:         account.RecoveryPolicyDigest,
		PasswordFingerprint:  fingerprint,
		TargetPasswordParams: prepared.params,
		CommittedAt:          now,
		RevokedFamilyIDs:     revokedFamilies,
		CanceledLoginIDs:     canceledLogins,
		CanceledResetIDs:     canceledResets,
	}

	account.PasswordHash = prepared.hash.Clone()
	account.CredentialEpoch++
	account.SessionEpoch++
	account.UpdatedAt = now
	record.state = ResetCommitted
	record.committedAt = now
	record.receipt = receipt.clone()
	for _, id := range revokedFamilies {
		family := s.store.families[id]
		family.Revoked = true
		family.RevokedAt = now
		family.RevokedWhy = "password_reset"
	}
	for _, id := range canceledLogins {
		s.store.logins[id].state = LoginCanceled
	}
	for _, id := range canceledResets {
		s.store.resets[id].state = ResetCanceled
	}
	return *receipt.clone(), nil, nil
}

// BeginLoginRequest starts a login transaction after credential verification.
type BeginLoginRequest struct {
	AccountID AccountID
	Password  string
	LoginID   LoginID
	Deadline  time.Time
}

// BeginLogin verifies the password outside the store lock, then binds the
// transaction to the epochs and hash it verified. A reset that commits during
// this call makes the transaction stale.
func (s *Service) BeginLogin(ctx context.Context, request BeginLoginRequest) (*LoginTransaction, error) {
	if request.AccountID == "" {
		return nil, errors.New("netsec: empty account id")
	}
	s.store.mu.Lock()
	account, ok := s.store.accounts[request.AccountID]
	if !ok {
		s.store.mu.Unlock()
		return nil, ErrAccountNotFound
	}
	if account.Status != AccountActive {
		s.store.mu.Unlock()
		return nil, ErrAccountInactive
	}
	snapshot := account.clone()
	s.store.mu.Unlock()

	passwordBytes := []byte(request.Password)
	defer zero(passwordBytes)
	valid, err := s.hasher.Verify(ctx, passwordBytes, snapshot.PasswordHash)
	if err != nil {
		return nil, err
	}
	if !valid {
		return nil, ErrInvalidCredentials
	}
	hashDigest := passwordHashDigest(snapshot.PasswordHash)
	now := s.now()
	deadline := request.Deadline
	if deadline.IsZero() {
		deadline = now.Add(s.loginTTL)
	}
	if !deadline.After(now) {
		return nil, ErrDeadlineExceeded
	}
	s.store.mu.Lock()
	defer s.store.mu.Unlock()
	current, ok := s.store.accounts[request.AccountID]
	if !ok {
		return nil, ErrAccountNotFound
	}
	if current.Status != AccountActive {
		return nil, ErrAccountInactive
	}
	if current.CredentialEpoch != snapshot.CredentialEpoch ||
		current.SessionEpoch != snapshot.SessionEpoch ||
		passwordHashDigest(current.PasswordHash) != hashDigest {
		return nil, ErrLoginStale
	}
	loginID := request.LoginID
	if loginID == "" {
		generated, err := s.newID("lgn_", 16)
		if err != nil {
			return nil, err
		}
		loginID = LoginID(generated)
	}
	if _, exists := s.store.logins[loginID]; exists {
		return nil, ErrLoginIDReuse
	}
	record := &loginRecord{
		transaction: LoginTransaction{
			ID:                   loginID,
			AccountID:            request.AccountID,
			StartCredentialEpoch: snapshot.CredentialEpoch,
			StartSessionEpoch:    snapshot.SessionEpoch,
			PasswordHashDigest:   hashDigest,
			Deadline:             deadline,
			CreatedAt:            now,
		},
		state: LoginPending,
	}
	s.store.logins[loginID] = record
	transaction := record.transaction
	return &transaction, nil
}

// CompleteLogin issues tokens only if the transaction is still pending, the
// account epochs and hash are unchanged, and no reset canceled it.
func (s *Service) CompleteLogin(ctx context.Context, id LoginID) (TokenPair, error) {
	if id == "" {
		return TokenPair{}, ErrUnknownLogin
	}
	s.store.mu.Lock()
	defer s.store.mu.Unlock()
	record, ok := s.store.logins[id]
	if !ok {
		return TokenPair{}, ErrUnknownLogin
	}
	switch record.state {
	case LoginCanceled:
		return TokenPair{}, ErrLoginCanceled
	case LoginCompleted:
		return TokenPair{}, ErrLoginAlreadyCompleted
	}
	now := s.now()
	if !now.Before(record.transaction.Deadline) {
		return TokenPair{}, ErrDeadlineExceeded
	}
	account, ok := s.store.accounts[record.transaction.AccountID]
	if !ok {
		return TokenPair{}, ErrAccountNotFound
	}
	if account.Status != AccountActive {
		return TokenPair{}, ErrAccountInactive
	}
	if account.CredentialEpoch != record.transaction.StartCredentialEpoch ||
		account.SessionEpoch != record.transaction.StartSessionEpoch ||
		passwordHashDigest(account.PasswordHash) != record.transaction.PasswordHashDigest {
		record.state = LoginCanceled
		return TokenPair{}, ErrLoginCanceled
	}
	select {
	case <-ctx.Done():
		return TokenPair{}, ctx.Err()
	default:
	}
	familyGenerated, err := s.newID("fam_", 16)
	if err != nil {
		return TokenPair{}, err
	}
	familyID := FamilyID(familyGenerated)
	if _, exists := s.store.families[familyID]; exists {
		return TokenPair{}, ErrFamilyIDReuse
	}
	accountSnapshot := account.clone()
	pair, err := s.signer.issuePair(now, s.lifetime, accountSnapshot, familyID, 1, s.newTokenID)
	if err != nil {
		return TokenPair{}, err
	}
	family := &RefreshFamily{
		ID:          familyID,
		AccountID:   account.ID,
		Generation:  1,
		CreatedAt:   now,
		LastRotated: now,
	}
	s.store.families[familyID] = family
	record.state = LoginCompleted
	record.familyID = familyID
	record.generation = 1
	record.completedAt = now
	return pair, nil
}

// VerifyAccessToken verifies the signature and then, under the account lock,
// compares the full 64-bit session epoch and credential epoch with the account.
// A valid signature from before a reset is therefore rejected.
func (s *Service) VerifyAccessToken(ctx context.Context, token string) (TokenClaims, error) {
	claims, err := s.signer.verify(token)
	if err != nil {
		return TokenClaims{}, err
	}
	if claims.Kind != TokenKindAccess {
		return TokenClaims{}, ErrTokenKind
	}
	if claims.FamilyID == "" {
		return TokenClaims{}, ErrInvalidToken
	}
	now := s.now()
	if now.UnixNano() >= claims.ExpiresAtNS {
		return TokenClaims{}, ErrTokenExpired
	}
	s.store.mu.Lock()
	defer s.store.mu.Unlock()
	account, ok := s.store.accounts[claims.AccountID]
	if !ok {
		return TokenClaims{}, ErrInvalidToken
	}
	if account.Status != AccountActive {
		return TokenClaims{}, ErrAccountInactive
	}
	if claims.CredentialEpoch != account.CredentialEpoch || claims.SessionEpoch != account.SessionEpoch {
		return TokenClaims{}, ErrTokenEpochStale
	}
	family, ok := s.store.families[claims.FamilyID]
	if !ok || family.AccountID != account.ID || family.Revoked {
		return TokenClaims{}, ErrTokenRevoked
	}
	return claims, nil
}

// VerifyAccess is an alias for VerifyAccessToken.
func (s *Service) VerifyAccess(ctx context.Context, token string) (TokenClaims, error) {
	return s.VerifyAccessToken(ctx, token)
}

// RefreshTokens performs a single-use rotation. Epoch checks, family-generation
// comparison, and the generation increment happen under one lock.
func (s *Service) RefreshTokens(ctx context.Context, token string) (TokenPair, error) {
	claims, err := s.signer.verify(token)
	if err != nil {
		return TokenPair{}, err
	}
	if claims.Kind != TokenKindRefresh {
		return TokenPair{}, ErrTokenKind
	}
	if claims.FamilyID == "" {
		return TokenPair{}, ErrInvalidToken
	}
	now := s.now()
	if now.UnixNano() >= claims.ExpiresAtNS {
		return TokenPair{}, ErrTokenExpired
	}
	s.store.mu.Lock()
	pair, alarm, err := s.refreshLocked(ctx, now, claims)
	s.store.mu.Unlock()
	if alarm != nil {
		s.raise(*alarm)
	}
	return pair, err
}

// Refresh is an alias for RefreshTokens.
func (s *Service) Refresh(ctx context.Context, token string) (TokenPair, error) {
	return s.RefreshTokens(ctx, token)
}

func (s *Service) refreshLocked(ctx context.Context, now time.Time, claims TokenClaims) (TokenPair, *Alarm, error) {
	account, ok := s.store.accounts[claims.AccountID]
	if !ok {
		return TokenPair{}, nil, ErrInvalidToken
	}
	if account.Status != AccountActive {
		return TokenPair{}, nil, ErrAccountInactive
	}
	if claims.CredentialEpoch != account.CredentialEpoch || claims.SessionEpoch != account.SessionEpoch {
		return TokenPair{}, nil, ErrTokenEpochStale
	}
	family, ok := s.store.families[claims.FamilyID]
	if !ok || family.AccountID != account.ID || family.Revoked {
		return TokenPair{}, nil, ErrTokenRevoked
	}
	if claims.Generation != family.Generation {
		return TokenPair{}, nil, ErrTokenReused
	}
	if family.Generation == math.MaxUint64 {
		family.Revoked = true
		family.RevokedAt = now
		family.RevokedWhy = "generation_exhausted"
		return TokenPair{}, &Alarm{
			Code:      AlarmFamilyGenerationExhausted,
			AccountID: account.ID,
			FamilyID:  family.ID,
			Detail:    "refresh family generation is at uint64 max; family revoked without wraparound",
			At:        now,
		}, ErrFamilyGenerationExhausted
	}
	select {
	case <-ctx.Done():
		return TokenPair{}, nil, ctx.Err()
	default:
	}
	nextGeneration := family.Generation + 1
	accountSnapshot := account.clone()
	pair, err := s.signer.issuePair(now, s.lifetime, accountSnapshot, family.ID, nextGeneration, s.newTokenID)
	if err != nil {
		return TokenPair{}, nil, err
	}
	family.Generation = nextGeneration
	family.LastRotated = now
	return pair, nil, nil
}

// SetAccountStatus updates status. Deactivating an account also revokes all
// refresh families and advances the session epoch (unless already at max) so
// already-issued access tokens fail closed.
func (s *Service) SetAccountStatus(id AccountID, status AccountStatus) error {
	s.store.mu.Lock()
	defer s.store.mu.Unlock()
	account, ok := s.store.accounts[id]
	if !ok {
		return ErrAccountNotFound
	}
	if status != AccountActive && account.Status == AccountActive {
		for _, family := range s.store.families {
			if family.AccountID == id && !family.Revoked {
				family.Revoked = true
				family.RevokedAt = s.now()
				family.RevokedWhy = "status_change"
			}
		}
		if account.SessionEpoch != math.MaxUint64 {
			account.SessionEpoch++
		}
	}
	account.Status = status
	account.UpdatedAt = s.now()
	return nil
}

func (s *Service) resolveParams(params PasswordParams) PasswordParams {
	if params == (PasswordParams{}) {
		return s.params
	}
	if params.Algorithm == "" {
		params.Algorithm = PBKDF2SHA256
	}
	return params
}

func (s *Service) now() time.Time { return s.clock.Now().UTC() }

func (s *Service) newID(prefix string, size int) (string, error) {
	buffer := make([]byte, size)
	if _, err := io.ReadFull(s.random, buffer); err != nil {
		return "", err
	}
	return prefix + hex.EncodeToString(buffer), nil
}

func (s *Service) newTokenID() (string, error) { return s.newID("tok_", 16) }

func (s *Service) raise(alarm Alarm) {
	if s.onAlarm != nil {
		s.onAlarm(alarm)
	}
}

func passwordHashDigest(hash PasswordHash) [32]byte {
	h := sha256.New()
	_, _ = h.Write([]byte("netsec/password-hash/v1\x00"))
	writeString(h, hash.Algorithm)
	var numbers [24]byte
	binary.BigEndian.PutUint32(numbers[0:4], hash.Params.Iterations)
	binary.BigEndian.PutUint32(numbers[4:8], hash.Params.SaltBytes)
	binary.BigEndian.PutUint32(numbers[8:12], hash.Params.KeyBytes)
	_, _ = h.Write(numbers[:12])
	_, _ = h.Write(hash.Salt)
	_, _ = h.Write(hash.Digest)
	var out [32]byte
	copy(out[:], h.Sum(nil))
	return out
}

func zero(buffer []byte) {
	for i := range buffer {
		buffer[i] = 0
	}
}

type lockedReader struct {
	mu     sync.Mutex
	reader io.Reader
}

func (r *lockedReader) Read(p []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return io.ReadFull(r.reader, p)
}

// Errors returned by reset, login, and token operations.
var (
	ErrAccountNotFound           = errors.New("netsec: account not found")
	ErrAccountExists             = errors.New("netsec: account already exists")
	ErrAccountInactive           = errors.New("netsec: account is not active")
	ErrEpochConflict             = errors.New("netsec: account epoch changed")
	ErrEpochExhausted            = errors.New("netsec: epoch exhausted; reset refused")
	ErrUnknownReset              = errors.New("netsec: unknown reset transaction")
	ErrResetIDReuse              = errors.New("netsec: reset ID reused with different binding")
	ErrResetConflict             = errors.New("netsec: reset transaction binding mismatch")
	ErrResetSuperseded           = errors.New("netsec: reset transaction superseded")
	ErrPolicyUnavailable         = errors.New("netsec: current recovery policy unavailable")
	ErrRecoveryPolicyUnsatisfied = errors.New("netsec: verified recovery methods do not satisfy current policy")
	ErrPasswordParamsMismatch    = errors.New("netsec: prepared password does not match target parameters")
	ErrDeadlineExceeded          = errors.New("netsec: transaction deadline exceeded")
	ErrInvalidCredentials        = errors.New("netsec: invalid credentials")
	ErrLoginStale                = errors.New("netsec: login transaction became stale")
	ErrUnknownLogin              = errors.New("netsec: unknown login transaction")
	ErrLoginIDReuse              = errors.New("netsec: login ID already exists")
	ErrLoginCanceled             = errors.New("netsec: login transaction canceled")
	ErrLoginAlreadyCompleted     = errors.New("netsec: login transaction already completed")
	ErrFamilyIDReuse             = errors.New("netsec: refresh family ID collision")
	ErrFingerprintKeyMismatch    = errors.New("netsec: password fingerprint key does not match store")
)
