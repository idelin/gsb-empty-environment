package netsec

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/binary"
	"errors"
	"math/rand"
	"time"
)

// CeremonyPurpose distinguishes the two WebAuthn ceremony types. Only an
// attestation ceremony may register a new public key; an assertion is an
// authentication response and must never be accepted as a registration.
type CeremonyPurpose string

const (
	// PurposeAttestation is the device-attestation (credential creation)
	// ceremony. It is the only purpose accepted by [Service.Register].
	PurposeAttestation CeremonyPurpose = "attestation"
	// PurposeAssertion is the device-assertion (credential request) ceremony.
	// Assertions cannot register a credential.
	PurposeAssertion CeremonyPurpose = "assertion"
)

// Sentinel errors returned by [Service.Register]. Errors from the host
// [Verifier] or [Store] are returned wrapped, alongside these where relevant.
var (
	// ErrInvalidRequest means a required RegisterRequest field is empty.
	ErrInvalidRequest = errors.New("netsec: invalid registration request")
	// ErrVerifierFailed is returned when the host Verifier rejects the
	// attestation response or returns an error.
	ErrVerifierFailed = errors.New("netsec: attestation verification failed")
	// ErrWrongCeremony means the verified response is not an attestation
	// ceremony, e.g. an assertion was replayed into a registration endpoint.
	ErrWrongCeremony = errors.New("netsec: response is not an attestation ceremony")
	// ErrOriginMismatch means the verified origin/source does not match the
	// expected Relying Party origin.
	ErrOriginMismatch = errors.New("netsec: attestation origin mismatch")
	// ErrRPMismatch means the verified relying party id does not match the
	// expected one.
	ErrRPMismatch = errors.New("netsec: relying party id mismatch")
	// ErrChallengeMismatch means the verified challenge does not match the
	// challenge the host issued for this registration.
	ErrChallengeMismatch = errors.New("netsec: challenge mismatch")
	// ErrChallengeConsumed means the one-time challenge was already used,
	// indicating a replayed attestation response.
	ErrChallengeConsumed = errors.New("netsec: challenge already consumed")
	// ErrCredentialAccountConflict means the credential id is already bound to
	// a different account.
	ErrCredentialAccountConflict = errors.New("netsec: credential id already bound to another account")
	// ErrCredentialKeyConflict means the credential id is bound to the same
	// account but to a different public key.
	ErrCredentialKeyConflict = errors.New("netsec: credential id already bound to a different public key")
	// ErrIdempotencyConflict means a stored idempotency key was reused with a
	// different request payload.
	ErrIdempotencyConflict = errors.New("netsec: idempotency key reused with a different request")
	// errCommitRace wraps a uniqueness violation detected while committing,
	// signalling that the transaction may be retried with a fresh read.
	errCommitRace = errors.New("netsec: commit lost a concurrency race")
)

// Verified is the structured output of a [Verifier]. The verifier owns the
// attestation cryptography; [Service] owns the policy and transaction.
type Verified struct {
	// Purpose must be PurposeAttestation for registration.
	Purpose CeremonyPurpose
	// Origin is the browser/source origin (scheme + host + port) the
	// authenticator attested.
	Origin string
	// RPID is the relying party id the credential was created for.
	RPID string
	// Challenge is the challenge echoed inside the attestation object.
	Challenge []byte
	// CredentialID is the unique handle of the new credential.
	CredentialID []byte
	// PublicKey is the credential public key to bind to the account.
	PublicKey []byte
}

// RegisterRequest is the host's registration input.
type RegisterRequest struct {
	// AccountID is the account that will own the new credential.
	AccountID string
	// ExpectedOrigin is the only origin accepted in the verified response.
	ExpectedOrigin string
	// ExpectedRPID is the only relying party id accepted in the response.
	ExpectedRPID string
	// ExpectedChallenge is the single-use challenge the host issued.
	ExpectedChallenge []byte
	// AttestationResponse is the opaque host-side attestation payload handed
	// to the Verifier (attestation object, client data, etc.).
	AttestationResponse []byte
	// IdempotencyKey makes a lost-response retry return the original result.
	// It may be empty, in which case only credential-id replay dedup applies.
	IdempotencyKey []byte
}

// Verifier is implemented by the host. It must verify the attestation
// signature chain, attestation statement and client-data JSON, then return the
// structured claims. It must be deterministic and side-effect free: the
// one-time challenge is consumed transactionally by [Service], so a retried
// request may invoke the verifier more than once.
type Verifier interface {
	VerifyAttestation(ctx context.Context, attestationResponse []byte) (Verified, error)
}

// Result is returned from [Service.Register].
type Result struct {
	// CredentialID is the occupied credential handle.
	CredentialID []byte
	// AccountID is the owning account.
	AccountID string
	// Replayed is true when an earlier identical registration was found and
	// no new row was written (idempotent replay or credential-id retry).
	Replayed bool
}

// Tx is one registration transaction. All reads observe prior writes made in
// the same transaction, and writes only become visible on Commit. A
// SQL-backed implementation wraps a single *sql.Tx; MemoryStore provides an
// in-memory implementation.
type Tx interface {
	// LookupIdempotency returns the stored fingerprint and result for a key.
	// A nil result means the key is unused.
	LookupIdempotency(ctx context.Context, key []byte) (fingerprint []byte, result *Result, err error)
	// SaveIdempotency stores the key, fingerprint and result together.
	SaveIdempotency(ctx context.Context, key, fingerprint []byte, result Result) error
	// LookupCredential returns the stored binding for a credential id, or nil
	// if the id is unoccupied.
	LookupCredential(ctx context.Context, credentialID []byte) (accountID string, publicKey []byte, err error)
	// SaveCredential atomically occupies the credential id. It must fail if the
	// id was inserted concurrently (unique constraint), in which case the
	// transaction is retried by the store or returns the driver's unique-
	// violation error for the caller to re-read.
	SaveCredential(ctx context.Context, credentialID []byte, accountID string, publicKey []byte) error
	// ConsumeChallenge deletes the single-use challenge. It returns
	// ErrChallengeConsumed when the challenge is absent (unknown or already
	// consumed).
	ConsumeChallenge(ctx context.Context, challenge []byte) error
	// Commit makes every write in the transaction durable and visible. It
	// reports whether this transaction created the credential: false means an
	// identical binding already existed and the call resolved as a replay.
	Commit() (created bool, err error)
	// Rollback discards every write in the transaction.
	Rollback() error
}

// Store is the persistence boundary for [Service].
type Store interface {
	// Begin opens a serializable transaction for one registration.
	Begin(ctx context.Context) (Tx, error)
}

// Service orchestrates verification and transactional credential occupation.
type Service struct {
	verifier Verifier
	store    Store
	// execLocks serializes registrations targeting the same credential id for
	// stores whose Begin does not itself take a credential row lock. A SQL
	// store that uses SELECT ... FOR UPDATE on credential_id does not need it;
	// the in-memory store relies on it to make the read-then-commit verdict
	// race-free.
	execLocks keyedMutex
}

// NewService builds a registration service over the given verifier and store.
func NewService(verifier Verifier, store Store) *Service {
	return &Service{verifier: verifier, store: store}
}

// Register verifies the attestation response and, when every structured claim
// matches the expected registration parameters, occupies the credential id in
// a single transaction. Retrying the same request returns the original result
// with Replayed set; a credential id already bound to another account or a
// different key is rejected.
func (s *Service) Register(ctx context.Context, req RegisterRequest) (Result, error) {
	if err := validateRequest(req); err != nil {
		return Result{}, err
	}

	verified, err := s.verifier.VerifyAttestation(ctx, req.AttestationResponse)
	if err != nil {
		return Result{}, errors.Join(ErrVerifierFailed, err)
	}
	if err := validateVerified(verified); err != nil {
		return Result{}, err
	}

	if verified.Purpose != PurposeAttestation {
		return Result{}, ErrWrongCeremony
	}
	if verified.Origin != req.ExpectedOrigin {
		return Result{}, ErrOriginMismatch
	}
	if verified.RPID != req.ExpectedRPID {
		return Result{}, ErrRPMismatch
	}
	if !constantTimeEqual(verified.Challenge, req.ExpectedChallenge) {
		return Result{}, ErrChallengeMismatch
	}

	fingerprint := fingerprintRequest(req, verified)
	unlock := s.execLocks.lock(verified.CredentialID)
	defer unlock()

	// Retry on commit-time races: the loser rolls back, opens a fresh
	// transaction and re-reads the winner's binding, which resolves as an
	// idempotent replay or a definitive conflict. SQL stores surface the same
	// condition as a unique-constraint error on credential_id.
	const maxAttempts = 8
	for attempt := 0; attempt < maxAttempts; attempt++ {
		result, err := s.runTx(ctx, req, verified, fingerprint)
		if err == nil || !errors.Is(err, errCommitRace) || attempt == maxAttempts-1 {
			return result, err
		}
		// Back off before re-reading the winner's transaction so a thundering
		// herd of identical retries converges quickly. Jitter keeps contenders
		// from retrying in lockstep.
		wait := time.Duration(attempt+1)*time.Millisecond + time.Duration(rand.Int63n(int64(2*time.Millisecond)))
		select {
		case <-time.After(wait):
		case <-ctx.Done():
			return Result{}, ctx.Err()
		}
	}
	return Result{}, ErrChallengeConsumed
}

func (s *Service) runTx(ctx context.Context, req RegisterRequest, verified Verified, fingerprint []byte) (Result, error) {
	tx, err := s.store.Begin(ctx)
	if err != nil {
		return Result{}, err
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	if len(req.IdempotencyKey) > 0 {
		storedFingerprint, storedResult, err := tx.LookupIdempotency(ctx, req.IdempotencyKey)
		if err != nil {
			return Result{}, err
		}
		if storedResult != nil {
			if !constantTimeEqual(storedFingerprint, fingerprint) {
				return Result{}, ErrIdempotencyConflict
			}
			replayed := *storedResult
			replayed.Replayed = true
			if _, err := tx.Commit(); err != nil {
				return Result{}, err
			}
			committed = true
			return replayed, nil
		}
	}

	existingAccount, existingKey, err := tx.LookupCredential(ctx, verified.CredentialID)
	if err != nil {
		return Result{}, err
	}
	if existingAccount != "" {
		replayed, conflict := classifyExisting(existingAccount, existingKey, req, verified)
		if conflict != nil {
			return Result{}, conflict
		}
		if err := persistIdempotency(ctx, tx, req, fingerprint, replayed); err != nil {
			return Result{}, err
		}
		if _, err := tx.Commit(); err != nil {
			return Result{}, err
		}
		committed = true
		return replayed, nil
	}

	// The challenge is consumed inside the same transaction that occupies the
	// credential id, so a replayed response can never register twice. The
	// credential lookup above already handles retries without touching the
	// one-time challenge.
	if err := tx.SaveCredential(ctx, verified.CredentialID, req.AccountID, verified.PublicKey); err != nil {
		return Result{}, err
	}
	if err := tx.ConsumeChallenge(ctx, req.ExpectedChallenge); err != nil {
		return Result{}, err
	}

	created := Result{
		CredentialID: append([]byte(nil), verified.CredentialID...),
		AccountID:    req.AccountID,
	}
	if err := persistIdempotency(ctx, tx, req, fingerprint, created); err != nil {
		return Result{}, err
	}
	didCreate, err := tx.Commit()
	if err != nil {
		return Result{}, err
	}
	committed = true
	if !didCreate {
		created.Replayed = true
	}
	return created, nil
}

func validateRequest(req RegisterRequest) error {
	if req.AccountID == "" || req.ExpectedOrigin == "" || req.ExpectedRPID == "" ||
		len(req.ExpectedChallenge) == 0 || len(req.AttestationResponse) == 0 {
		return ErrInvalidRequest
	}
	return nil
}

func validateVerified(v Verified) error {
	if v.Origin == "" || v.RPID == "" || len(v.Challenge) == 0 ||
		len(v.CredentialID) == 0 || len(v.PublicKey) == 0 {
		return ErrVerifierFailed
	}
	return nil
}

func classifyExisting(existingAccount string, existingKey []byte, req RegisterRequest, verified Verified) (Result, error) {
	if existingAccount != req.AccountID {
		return Result{}, ErrCredentialAccountConflict
	}
	if !constantTimeEqual(existingKey, verified.PublicKey) {
		return Result{}, ErrCredentialKeyConflict
	}
	return Result{
		CredentialID: append([]byte(nil), verified.CredentialID...),
		AccountID:    req.AccountID,
		Replayed:     true,
	}, nil
}

func persistIdempotency(ctx context.Context, tx Tx, req RegisterRequest, fingerprint []byte, result Result) error {
	if len(req.IdempotencyKey) == 0 {
		return nil
	}
	return tx.SaveIdempotency(ctx, req.IdempotencyKey, fingerprint, result)
}

func constantTimeEqual(a, b []byte) bool {
	return subtle.ConstantTimeCompare(a, b) == 1
}

// fingerprintRequest binds an idempotency key to the exact request and
// verified claims it was first used with, so a reused key with a different
// payload is rejected rather than returning an unrelated result.
func fingerprintRequest(req RegisterRequest, v Verified) []byte {
	h := sha256.New()
	writeString := func(s string) {
		length := make([]byte, binary.MaxVarintLen64)
		n := binary.PutUvarint(length, uint64(len(s)))
		_, _ = h.Write(length[:n])
		_, _ = h.Write([]byte(s))
	}
	writeBytes := func(b []byte) {
		length := make([]byte, binary.MaxVarintLen64)
		n := binary.PutUvarint(length, uint64(len(b)))
		_, _ = h.Write(length[:n])
		_, _ = h.Write(b)
	}

	writeString(string(v.Purpose))
	writeString(v.Origin)
	writeString(v.RPID)
	writeBytes(v.Challenge)
	writeBytes(v.CredentialID)
	writeBytes(v.PublicKey)
	writeString(req.AccountID)
	writeString(req.ExpectedOrigin)
	writeString(req.ExpectedRPID)
	writeBytes(req.ExpectedChallenge)
	writeBytes(req.AttestationResponse)
	return h.Sum(nil)
}
