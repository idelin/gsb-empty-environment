package refresh

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"sync"
)

// Service-level sentinel errors. Callers can use errors.Is to distinguish a
// benign idempotent replay from an actual security rejection.
var (
	// ErrEmptyIdempotencyKey is returned when a refresh omits its idempotency key.
	ErrEmptyIdempotencyKey = errors.New("refresh: idempotency key is required")
	// ErrFamilyRevoked is returned when the token's family has already been
	// revoked, including the case where the current request triggers the
	// revocation as a reuse response.
	ErrFamilyRevoked = errors.New("refresh: token family revoked due to reuse")
	// ErrIdempotencyConflict is returned when an idempotency key that already
	// belongs to one old token is presented alongside a different old token.
	ErrIdempotencyConflict = errors.New("refresh: idempotency key bound to a different token")
)

// ReuseReason explains why a family was revoked.
const (
	ReasonConsumedReplay = "rotated-token-reuse"
	ReasonAdminRevoke    = "admin-revocation"
)

// IDGenerator produces opaque identifiers for families, tokens and rotations.
// The default uses crypto/rand; tests may inject a deterministic generator.
type IDGenerator func() (string, error)

func randomID() (string, error) {
	var buf [24]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf[:]), nil
}

// RefreshInput is one refresh request. The client must send the same
// IdempotencyKey when retrying a lost request.
type RefreshInput struct {
	// OldTokenID is the refresh token the client currently holds. In this
	// reference component the opaque token string is used directly as its row
	// identifier; a production deployment would store only its hash.
	OldTokenID string
	// IdempotencyKey binds one retry chain to exactly one rotation.
	IdempotencyKey string
}

// RefreshResult is the response persisted together with the rotation. Every
// retry with the same key receives an identical copy of this value.
type RefreshResult struct {
	FamilyID       string
	OldTokenID     string
	NewTokenID     string
	IdempotencyKey string
	RotationID     string
}

// Option customises a Service.
type Option func(*Service)

// WithIDGenerator overrides the random identifier generator.
func WithIDGenerator(gen IDGenerator) Option {
	return func(s *Service) { s.newID = gen }
}

// Service executes refresh-token transactions.
//
// Invariants enforced on every request:

//   - one (old token, idempotency key) pair produces at most one rotation, and
//     every retry with that pair returns the originally persisted result;
//   - presenting an already consumed token with any other key is token reuse
//     and revokes the whole family, so a rotated token cannot be "resurrected";
//   - the response is only returned after the transaction has durably
//     committed ("persist before return");
//   - a per-family mutex, together with the durable event log, gives refresh
//     and revocation actions one well-defined total order per family.
type Service struct {
	store   Store
	newID   IDGenerator
	locksMu sync.Mutex
	locks   map[string]*sync.Mutex
}

// NewService creates a refresh service backed by the given store.
func NewService(store Store, opts ...Option) *Service {
	s := &Service{
		store: store,
		newID: randomID,
		locks: map[string]*sync.Mutex{},
	}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

// familyLock returns the serializing mutex for one token family. All reads
// and writes that decide the fate of a family happen while holding it, so
// concurrent refreshes against the same family linearize in lock order and
// the event log mirrors that order exactly.
func (s *Service) familyLock(familyID string) *sync.Mutex {
	s.locksMu.Lock()
	defer s.locksMu.Unlock()
	mu, ok := s.locks[familyID]
	if !ok {
		mu = &sync.Mutex{}
		s.locks[familyID] = mu
	}
	return mu
}

// IssueFamily creates a fresh token family with its first unused token. It is
// the entry point used by the primary login flow.
func (s *Service) IssueFamily() (RefreshResult, error) {
	familyID, err := s.newID()
	if err != nil {
		return RefreshResult{}, err
	}
	tokenID, err := s.newID()
	if err != nil {
		return RefreshResult{}, err
	}
	err = s.store.Apply(func(tx *Tx) error {
		if _, exists := tx.family(familyID); exists {
			return errors.New("refresh: generated family id collided")
		}
		tx.putFamily(familyRecord{id: familyID, revoked: false, version: 1})
		tx.putToken(tokenRecord{id: tokenID, familyID: familyID, status: TokenUnused})
		tx.appendEvent(EventIssued, familyID, "", tokenID, "", "")
		return nil
	})
	if err != nil {
		return RefreshResult{}, err
	}
	// The result is returned only after the commit above succeeded.
	return RefreshResult{
		FamilyID:   familyID,
		NewTokenID: tokenID,
	}, nil
}

// Refresh performs one idempotent, reuse-detecting token rotation.
func (s *Service) Refresh(in RefreshInput) (RefreshResult, error) {
	if in.IdempotencyKey == "" {
		return RefreshResult{}, ErrEmptyIdempotencyKey
	}
	if in.OldTokenID == "" {
		return RefreshResult{}, ErrTokenNotFound
	}

	// One consistent read to locate the family. The authoritative decision is
	// re-made inside the transaction while holding the family lock.
	_, family, err := s.store.LoadToken(in.OldTokenID)
	if err != nil {
		return RefreshResult{}, err
	}

	mu := s.familyLock(family.ID)
	mu.Lock()
	defer mu.Unlock()

	var result RefreshResult
	var revoked bool
	var reject error
	commitErr := s.store.Apply(func(tx *Tx) error {
		// 1. Idempotent fast path: the (token, key) pair already rotated.
		if rotation, ok := tx.rotationByBinding(in.OldTokenID, in.IdempotencyKey); ok {
			result = rotation.result
			return nil
		}

		// A key may only ever belong to one old token. Reusing it elsewhere is
		// a client bug or an attempt to splice two retry chains.
		if owner, ok := tx.keyOwner(in.IdempotencyKey); ok && owner != in.OldTokenID {
			return ErrIdempotencyConflict
		}

		tok, ok := tx.token(in.OldTokenID)
		if !ok {
			return ErrTokenNotFound
		}
		fam, ok := tx.family(tok.familyID)
		if !ok {
			return ErrFamilyNotFound
		}

		// 2. The family was already revoked (by an earlier reuse or by admin).
		// No new rotation may ever be minted under it.
		if fam.revoked {
			revoked = true
			reject = ErrFamilyRevoked
			return nil
		}

		switch tok.status {
		case TokenUnused:
			// 3. First presentation: perform the single allowed rotation.
			newTokenID, err := s.newID()
			if err != nil {
				return err
			}
			rotationID, err := s.newID()
			if err != nil {
				return err
			}
			tok.status = TokenConsumed
			tok.rotationID = rotationID
			tx.putToken(tok)
			tx.putToken(tokenRecord{
				id:       newTokenID,
				familyID: fam.id,
				status:   TokenUnused,
			})
			fam.version++
			tx.putFamily(fam)
			tx.appendEvent(EventRotation, fam.id, tok.id, newTokenID, in.IdempotencyKey, "")
			seq := tx.data.seq

			res := RefreshResult{
				FamilyID:       fam.id,
				OldTokenID:     tok.id,
				NewTokenID:     newTokenID,
				IdempotencyKey: in.IdempotencyKey,
				RotationID:     rotationID,
			}
			tx.putRotation(rotationRecord{
				id:             rotationID,
				familyID:       fam.id,
				oldTokenID:     tok.id,
				idempotencyKey: in.IdempotencyKey,
				newTokenID:     newTokenID,
				result:         res,
				seq:            seq,
			})
			result = res
			return nil

		case TokenConsumed:
			// 4. A rotated token came back with a different key: this is token
			// reuse (lost-phone / stolen-token pattern). Revoke the whole
			// family, including the new tokens already issued, in the same
			// transaction, then reject the request. The revocation is part of
			// the committed transaction; the rejection is delivered separately
			// so a rolled-back security action can never happen.
			tx.revokeFamily(fam.id, ReasonConsumedReplay)
			revoked = true
			reject = ErrFamilyRevoked
			tx.RejectAfterCommit(ErrFamilyRevoked)
			return nil

		default: // TokenRevoked: family-level revocation already applied.
			revoked = true
			reject = ErrFamilyRevoked
			return nil
		}
	})
	if commitErr != nil {
		return RefreshResult{}, commitErr
	}
	if revoked {
		if result == (RefreshResult{}) {
			return RefreshResult{}, reject
		}
		// The idempotent response still wins over a later revocation, which is
		// exactly the "retry must not be mistaken for theft" guarantee.
		return result, nil
	}
	// Commit is durable at this point; only now is the response returned.
	return result, nil
}

// RevokeFamily administratively revokes a whole family. It takes the same
// per-family lock as Refresh, so the revocation is ordered relative to every
// rotation instead of racing them.
func (s *Service) RevokeFamily(familyID string) error {
	mu := s.familyLock(familyID)
	mu.Lock()
	defer mu.Unlock()

	return s.store.Apply(func(tx *Tx) error {
		if _, ok := tx.family(familyID); !ok {
			return ErrFamilyNotFound
		}
		tx.revokeFamily(familyID, ReasonAdminRevoke)
		return nil
	})
}
