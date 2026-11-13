package netsec

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"math/big"
	"sync"
)

// NonceState is the durable lifecycle state of one signer nonce.
type NonceState string

const (
	// NoncePrepared means the nonce and its request binding were durably
	// stored before the nonce commitment was released.
	NoncePrepared NonceState = "prepared"
	// NonceChallenged means the signer durably bound the nonce to one
	// aggregate nonce (and therefore one challenge) before computing z.
	NonceChallenged NonceState = "challenged"
	// NonceConsumed means the nonce is spent and the partial signature was
	// stored in the same atomic transition.
	NonceConsumed NonceState = "consumed"
	// NonceAborted means the nonce is permanently unusable.
	NonceAborted NonceState = "aborted"
)

var (
	// ErrNonceNotFound is returned by a NonceStore when no record exists.
	ErrNonceNotFound = errors.New("netsec: nonce record not found")
	// ErrNonceExists is returned by NonceStore.Create when a record already
	// exists for the key.
	ErrNonceExists = errors.New("netsec: nonce record already exists")
	// ErrNonceVersionConflict is returned when a compare-and-swap update sees
	// a different record version.
	ErrNonceVersionConflict = errors.New("netsec: nonce record version conflict")
	// ErrNonceAborted is returned when a signer tries to use an aborted or
	// permanently discarded nonce.
	ErrNonceAborted = errors.New("netsec: nonce has been aborted")
	// ErrRequestMismatch is returned when a retry changes the session,
	// message, or roster bound to an existing nonce.
	ErrRequestMismatch = errors.New("netsec: request does not match persisted nonce binding")
	// ErrNonceChallengeMismatch is returned when a signer is asked to sign a
	// different aggregate nonce or challenge with the same nonce.
	ErrNonceChallengeMismatch = errors.New("netsec: nonce is bound to a different challenge")
	// ErrNonceConsumedConflict is returned when a consumed nonce is retried
	// with a different aggregate nonce or challenge.
	ErrNonceConsumedConflict = errors.New("netsec: consumed nonce cannot sign a different challenge")
	// ErrNonceCorrupt is returned when a persisted nonce record is malformed
	// or internally inconsistent.
	ErrNonceCorrupt = errors.New("netsec: corrupt nonce record")
	// ErrConcurrentNonceUpdate is returned if the signer cannot make progress
	// after repeated optimistic-concurrency retries.
	ErrConcurrentNonceUpdate = errors.New("netsec: too many concurrent nonce record updates")
)

// NonceRecord is the durable signer record. It intentionally contains the
// secret nonce; hosts should treat the store as sensitive key material.
//
// Version is an optimistic-concurrency token. Create stores version 1.
// Update atomically replaces the record only when the stored version equals
// expectedVersion, and stores the supplied record under version
// expectedVersion+1.
type NonceRecord struct {
	Version uint64
	State   NonceState

	SessionID []byte
	Message   []byte
	Roster    []byte
	PublicKey []byte

	Nonce      []byte
	Commitment []byte
	NoncePoint []byte

	AggregateNonce []byte
	Challenge      []byte
	Partial        []byte
}

// Clone returns a deep copy of the record.
func (r NonceRecord) Clone() NonceRecord {
	return NonceRecord{
		Version:        r.Version,
		State:          r.State,
		SessionID:      cloneBytes(r.SessionID),
		Message:        cloneBytes(r.Message),
		Roster:         cloneBytes(r.Roster),
		PublicKey:      cloneBytes(r.PublicKey),
		Nonce:          cloneBytes(r.Nonce),
		Commitment:     cloneBytes(r.Commitment),
		NoncePoint:     cloneBytes(r.NoncePoint),
		AggregateNonce: cloneBytes(r.AggregateNonce),
		Challenge:      cloneBytes(r.Challenge),
		Partial:        cloneBytes(r.Partial),
	}
}

func cloneBytes(in []byte) []byte {
	if in == nil {
		return nil
	}
	return append([]byte(nil), in...)
}

// NonceStore is the host persistence interface for signer nonce state.
//
// Implementations must:
//   - make Create atomic and return ErrNonceExists if the key is present;
//   - make Update an atomic compare-and-swap on Version;
//   - persist all byte slices before returning success; and
//   - return ErrNonceNotFound for a missing key.
type NonceStore interface {
	Get(ctx context.Context, key string) (NonceRecord, error)
	Create(ctx context.Context, key string, record NonceRecord) error
	Update(ctx context.Context, key string, expectedVersion uint64, record NonceRecord) error
}

// MemoryNonceStore is an in-memory NonceStore used by tests and lightweight
// hosts. Its operations are guarded by a mutex and are atomic with respect to
// other calls.
type MemoryNonceStore struct {
	mu      sync.Mutex
	records map[string]NonceRecord
}

// NewMemoryNonceStore returns an empty in-memory nonce store.
func NewMemoryNonceStore() *MemoryNonceStore {
	return &MemoryNonceStore{records: make(map[string]NonceRecord)}
}

// Get implements NonceStore.
func (s *MemoryNonceStore) Get(ctx context.Context, key string) (NonceRecord, error) {
	if err := ctx.Err(); err != nil {
		return NonceRecord{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	record, ok := s.records[key]
	if !ok {
		return NonceRecord{}, ErrNonceNotFound
	}
	return record.Clone(), nil
}

// Create implements NonceStore.
func (s *MemoryNonceStore) Create(ctx context.Context, key string, record NonceRecord) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.records == nil {
		s.records = make(map[string]NonceRecord)
	}
	if _, ok := s.records[key]; ok {
		return ErrNonceExists
	}
	if record.Version == 0 {
		record.Version = 1
	}
	s.records[key] = record.Clone()
	return nil
}

// Update implements NonceStore.
func (s *MemoryNonceStore) Update(ctx context.Context, key string, expectedVersion uint64, record NonceRecord) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	current, ok := s.records[key]
	if !ok {
		return ErrNonceNotFound
	}
	if current.Version != expectedVersion {
		return ErrNonceVersionConflict
	}
	record.Version = expectedVersion + 1
	s.records[key] = record.Clone()
	return nil
}

// SignRequest binds a signer nonce to a session, message, and roster.
type SignRequest struct {
	SessionID []byte
	Message   []byte
	Roster    *Roster
}

type signerBinding struct {
	sessionID []byte
	message   []byte
	roster    []byte
	publicKey []byte
}

// SignerOption configures a Signer.
type SignerOption func(*Signer) error

// WithRandomSource replaces crypto/rand for nonce generation. It is intended
// for deterministic tests and must still produce unpredictable nonces in
// production.
func WithRandomSource(random io.Reader) SignerOption {
	return func(s *Signer) error {
		if random == nil {
			return errors.New("netsec: nil random source")
		}
		s.random = random
		return nil
	}
}

// Signer is one member's signing host. All nonce lifecycle changes go through
// the supplied NonceStore.
type Signer struct {
	privateKey *ecdsa.PrivateKey
	publicKey  []byte
	store      NonceStore
	random     io.Reader
}

// NewSigner creates a signer backed by a durable nonce store.
func NewSigner(privateKey *ecdsa.PrivateKey, store NonceStore, options ...SignerOption) (*Signer, error) {
	if err := validatePrivateKey(privateKey); err != nil {
		return nil, err
	}
	if store == nil {
		return nil, errors.New("netsec: nil nonce store")
	}
	publicKey, err := CompressedPublicKey(&privateKey.PublicKey)
	if err != nil {
		return nil, err
	}
	signer := &Signer{
		privateKey: &ecdsa.PrivateKey{
			PublicKey: ecdsa.PublicKey{
				Curve: p256Curve,
				X:     new(big.Int).Set(privateKey.X),
				Y:     new(big.Int).Set(privateKey.Y),
			},
			D: new(big.Int).Set(privateKey.D),
		},
		publicKey: publicKey,
		store:     store,
		random:    rand.Reader,
	}
	for _, option := range options {
		if option == nil {
			continue
		}
		if err := option(signer); err != nil {
			return nil, err
		}
	}
	return signer, nil
}

// PublicKey returns the signer's compressed P-256 public key.
func (s *Signer) PublicKey() []byte {
	return cloneBytes(s.publicKey)
}

// StoreKey returns the opaque key used for this request's nonce record.
func (s *Signer) StoreKey(request SignRequest) (string, error) {
	binding, err := s.binding(request)
	if err != nil {
		return "", err
	}
	return nonceRecordKey(binding.sessionID, binding.publicKey), nil
}

func nonceRecordKey(sessionID, publicKey []byte) string {
	digest := digestFields([]byte(DomainTagNonceRecord), sessionID, publicKey)
	return hex.EncodeToString(digest)
}

func (s *Signer) binding(request SignRequest) (signerBinding, error) {
	if request.Roster == nil {
		return signerBinding{}, errors.New("netsec: nil roster")
	}
	if request.Roster.aggregate.isInfinity() {
		return signerBinding{}, ErrZeroAggregatePublicKey
	}
	if !request.Roster.Contains(s.publicKey) {
		return signerBinding{}, ErrUnknownSigner
	}
	return signerBinding{
		sessionID: cloneBytes(request.SessionID),
		message:   cloneBytes(request.Message),
		roster:    request.Roster.Bytes(),
		publicKey: cloneBytes(s.publicKey),
	}, nil
}

func (s *Signer) loadRecord(ctx context.Context, key string, binding signerBinding) (NonceRecord, error) {
	record, err := s.store.Get(ctx, key)
	if err != nil {
		return NonceRecord{}, err
	}
	if !bytes.Equal(record.PublicKey, binding.publicKey) ||
		!bytes.Equal(record.SessionID, binding.sessionID) ||
		!bytes.Equal(record.Message, binding.message) ||
		!bytes.Equal(record.Roster, binding.roster) {
		return NonceRecord{}, ErrRequestMismatch
	}
	if record.Version == 0 {
		return NonceRecord{}, ErrNonceCorrupt
	}
	if err := validateNonceRecord(record); err != nil {
		return NonceRecord{}, err
	}
	if record.State != NonceAborted {
		nonce := new(big.Int).SetBytes(record.Nonce)
		if nonce.Sign() == 0 || nonce.Cmp(groupN) >= 0 {
			return NonceRecord{}, ErrNonceCorrupt
		}
		noncePoint := scalarBaseMult(nonce)
		if noncePoint.isInfinity() || !bytes.Equal(noncePoint.compressed(), record.NoncePoint) {
			return NonceRecord{}, ErrNonceCorrupt
		}
		expectedCommitment := CommitmentHash(binding.sessionID, binding.publicKey, record.NoncePoint)
		if !bytes.Equal(expectedCommitment, record.Commitment) {
			return NonceRecord{}, ErrNonceCorrupt
		}
	}
	return record, nil
}

func validateNonceRecord(record NonceRecord) error {
	switch record.State {
	case NoncePrepared, NonceChallenged, NonceConsumed:
		if len(record.Nonce) != ScalarSize ||
			len(record.Commitment) != 32 ||
			len(record.NoncePoint) != CompressedPointSize {
			return ErrNonceCorrupt
		}
	case NonceAborted:
		return nil
	default:
		return ErrNonceCorrupt
	}
	if record.State == NonceChallenged || record.State == NonceConsumed {
		if len(record.AggregateNonce) != CompressedPointSize || len(record.Challenge) != ScalarSize {
			return ErrNonceCorrupt
		}
	}
	if record.State == NonceConsumed && len(record.Partial) != ScalarSize {
		return ErrNonceCorrupt
	}
	return nil
}

// Commit returns the signer's nonce commitment. The nonce, message, and roster
// binding are durably stored before this method returns.
func (s *Signer) Commit(ctx context.Context, request SignRequest) ([]byte, error) {
	binding, err := s.binding(request)
	if err != nil {
		return nil, err
	}
	key := nonceRecordKey(binding.sessionID, binding.publicKey)

	for attempt := 0; attempt < 16; attempt++ {
		record, err := s.loadRecord(ctx, key, binding)
		if err == nil {
			if record.State == NonceAborted {
				return nil, ErrNonceAborted
			}
			return cloneBytes(record.Commitment), nil
		}
		if !errors.Is(err, ErrNonceNotFound) {
			return nil, err
		}

		record, err = s.newPreparedRecord(binding)
		if err != nil {
			return nil, err
		}
		err = s.store.Create(ctx, key, record)
		if errors.Is(err, ErrNonceExists) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("netsec: persist nonce before commitment: %w", err)
		}
		return cloneBytes(record.Commitment), nil
	}
	return nil, ErrConcurrentNonceUpdate
}

func (s *Signer) newPreparedRecord(binding signerBinding) (NonceRecord, error) {
	nonce, err := randomNonzeroScalar(s.random)
	if err != nil {
		return NonceRecord{}, err
	}
	noncePoint := scalarBaseMult(nonce)
	if noncePoint.isInfinity() {
		return NonceRecord{}, errors.New("netsec: generated invalid nonce")
	}
	noncePointBytes := noncePoint.compressed()
	commitment := CommitmentHash(binding.sessionID, binding.publicKey, noncePointBytes)
	return NonceRecord{
		Version:    1,
		State:      NoncePrepared,
		SessionID:  cloneBytes(binding.sessionID),
		Message:    cloneBytes(binding.message),
		Roster:     cloneBytes(binding.roster),
		PublicKey:  cloneBytes(binding.publicKey),
		Nonce:      scalarBytes(nonce),
		Commitment: commitment,
		NoncePoint: noncePointBytes,
	}, nil
}

func randomNonzeroScalar(random io.Reader) (*big.Int, error) {
	for {
		value, err := rand.Int(random, groupN)
		if err != nil {
			return nil, fmt.Errorf("netsec: generate nonce: %w", err)
		}
		if value.Sign() != 0 {
			return value, nil
		}
	}
}

// Reveal returns R_i = r_i G. It is only useful after all commitments have
// been fixed; the coordinator enforces that ordering.
func (s *Signer) Reveal(ctx context.Context, request SignRequest) ([]byte, error) {
	binding, err := s.binding(request)
	if err != nil {
		return nil, err
	}
	key := nonceRecordKey(binding.sessionID, binding.publicKey)
	record, err := s.loadRecord(ctx, key, binding)
	if err != nil {
		return nil, err
	}
	if record.State == NonceAborted {
		return nil, ErrNonceAborted
	}
	return cloneBytes(record.NoncePoint), nil
}

// Partial computes z_i = r_i + e*a_i*x_i after binding the persisted nonce to
// the aggregate nonce R. It returns a partial signature only after the
// consumed state and z_i have been atomically persisted.
func (s *Signer) Partial(ctx context.Context, request SignRequest, aggregateNonce []byte) ([]byte, error) {
	binding, err := s.binding(request)
	if err != nil {
		return nil, err
	}
	rPoint, err := parseCompressedPoint(aggregateNonce)
	if err != nil {
		return nil, err
	}
	if rPoint.isInfinity() {
		return nil, ErrZeroAggregateNonce
	}
	roster := request.Roster
	coefficient, ok := roster.Coefficient(s.publicKey)
	if !ok {
		return nil, ErrUnknownSigner
	}
	challengeBytes, err := ComputeChallenge(binding.sessionID, binding.message, roster, rPoint.compressed())
	if err != nil {
		return nil, err
	}
	challenge := new(big.Int).SetBytes(challengeBytes)
	key := nonceRecordKey(binding.sessionID, binding.publicKey)
	canonicalAggregateNonce := rPoint.compressed()

	for attempt := 0; attempt < 16; attempt++ {
		record, err := s.loadRecord(ctx, key, binding)
		if err != nil {
			return nil, err
		}
		if record.State == NonceAborted {
			return nil, ErrNonceAborted
		}

		switch record.State {
		case NonceConsumed:
			if !bytes.Equal(record.AggregateNonce, canonicalAggregateNonce) ||
				!bytes.Equal(record.Challenge, challengeBytes) {
				return nil, ErrNonceConsumedConflict
			}
			return cloneBytes(record.Partial), nil

		case NonceChallenged:
			if !bytes.Equal(record.AggregateNonce, canonicalAggregateNonce) ||
				!bytes.Equal(record.Challenge, challengeBytes) {
				return nil, ErrNonceChallengeMismatch
			}
			partial, err := s.computePartial(record, challenge, coefficient)
			if err != nil {
				return nil, err
			}
			next := record.Clone()
			next.Version = record.Version + 1
			next.State = NonceConsumed
			next.Partial = partial
			err = s.store.Update(ctx, key, record.Version, next)
			if errors.Is(err, ErrNonceVersionConflict) {
				continue
			}
			if err != nil {
				return nil, fmt.Errorf("netsec: persist consumed nonce and partial signature: %w", err)
			}
			return cloneBytes(partial), nil

		case NoncePrepared:
			next := record.Clone()
			next.Version = record.Version + 1
			next.State = NonceChallenged
			next.AggregateNonce = cloneBytes(canonicalAggregateNonce)
			next.Challenge = cloneBytes(challengeBytes)
			err = s.store.Update(ctx, key, record.Version, next)
			if errors.Is(err, ErrNonceVersionConflict) {
				continue
			}
			if err != nil {
				return nil, fmt.Errorf("netsec: persist challenge binding: %w", err)
			}
			continue

		default:
			return nil, ErrNonceCorrupt
		}
	}
	return nil, ErrConcurrentNonceUpdate
}

func (s *Signer) computePartial(record NonceRecord, challenge, coefficient *big.Int) ([]byte, error) {
	if len(record.Nonce) != ScalarSize {
		return nil, ErrNonceCorrupt
	}
	nonce := new(big.Int).SetBytes(record.Nonce)
	if nonce.Sign() == 0 || nonce.Cmp(groupN) >= 0 {
		return nil, ErrNonceCorrupt
	}

	// z = r + e*a*x mod N
	partial := new(big.Int).Mul(challenge, coefficient)
	partial.Mul(partial, s.privateKey.D)
	partial.Add(partial, nonce)
	partial.Mod(partial, groupN)

	// Defend against a corrupted record or accidental implementation error.
	noncePoint := scalarBaseMult(nonce)
	publicKey := &ecdsa.PublicKey{Curve: p256Curve, X: s.privateKey.X, Y: s.privateKey.Y}
	expectedRight := addPoints(
		noncePoint,
		scalarMultPoint(point{x: publicKey.X, y: publicKey.Y}, new(big.Int).Mul(challenge, coefficient)),
	)
	if !scalarBaseMult(partial).equal(expectedRight) {
		return nil, errors.New("netsec: internal partial signature check failed")
	}
	return scalarBytes(partial), nil
}

// Abort permanently discards the nonce. If no record exists yet, an abort
// tombstone is created so the same session/public-key binding cannot later be
// committed.
func (s *Signer) Abort(ctx context.Context, request SignRequest) error {
	binding, err := s.binding(request)
	if err != nil {
		return err
	}
	key := nonceRecordKey(binding.sessionID, binding.publicKey)

	for attempt := 0; attempt < 16; attempt++ {
		record, err := s.loadRecord(ctx, key, binding)
		if errors.Is(err, ErrNonceNotFound) {
			tombstone := NonceRecord{
				Version:   1,
				State:     NonceAborted,
				SessionID: cloneBytes(binding.sessionID),
				Message:   cloneBytes(binding.message),
				Roster:    cloneBytes(binding.roster),
				PublicKey: cloneBytes(binding.publicKey),
			}
			err = s.store.Create(ctx, key, tombstone)
			if errors.Is(err, ErrNonceExists) {
				continue
			}
			if err != nil {
				return fmt.Errorf("netsec: persist nonce abort: %w", err)
			}
			return nil
		}
		if err != nil {
			return err
		}
		if record.State == NonceAborted {
			return nil
		}

		next := record.Clone()
		next.Version = record.Version + 1
		next.State = NonceAborted
		next.Nonce = nil
		next.Partial = nil
		err = s.store.Update(ctx, key, record.Version, next)
		if errors.Is(err, ErrNonceVersionConflict) {
			continue
		}
		if err != nil {
			return fmt.Errorf("netsec: persist nonce abort: %w", err)
		}
		return nil
	}
	return ErrConcurrentNonceUpdate
}
