// Package admission implements atomic batch admission of RSA public keys for
// a certificate issuance service.
//
// A key is admitted only when its RSA modulus is accompanied by a complete
// prime factorization. Every prime factor is indexed once it is published, so
// any later key whose modulus shares a prime factor with an already published
// key is rejected. This blocks silent modulus reuse (including the "same
// modulus, different public exponent" case) while still allowing idempotent
// retransmission of an identical public key under the same caller ID.
//
// ImportBatch validates the whole batch before publishing anything: either
// every admissible item is committed together or the published index is left
// untouched. All operations are guarded by a mutex, making concurrent batches
// fully serialized and thus free of check-then-act races.
package admission

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"math/big"
	"sync"
)

// Per-item validation failures, returned in ItemResult.Err.
var (
	// ErrInvalidRequest means a request is malformed (empty ID, bad modulus or
	// exponent, missing/non-prime/non-unique factors, factors whose product is
	// not the modulus, or an exponent sharing a factor with lambda(n)).
	ErrInvalidRequest = errors.New("admission: invalid import request")
	// ErrIDReused is returned when an already admitted key ID refers to a
	// different public key than the one originally published.
	ErrIDReused = errors.New("admission: key ID already in use with a different public key")
	// ErrFactorConflict means a prime factor is already owned by another
	// published key (or another item in the same batch).
	ErrFactorConflict = errors.New("admission: prime factor shared with another key")
)

// ImportRequest is one admission entry: the RSA public key (N, E) together
// with the complete, caller-provided factorization of N. The service never
// attempts to factor N itself; the factors are verified before the key is
// trusted.
type ImportRequest struct {
	// KeyID is the caller-assigned stable identifier of the public key.
	KeyID string
	// N is the RSA modulus.
	N *big.Int
	// E is the public exponent.
	E *big.Int
	// Factors is the full list of distinct prime factors whose product is N.
	// Multi-prime moduli (more than two factors) are supported.
	Factors []*big.Int
}

// ItemResult reports the outcome of a single request, at the same index as the
// corresponding ImportRequest.
type ItemResult struct {
	// KeyID mirrors the request ID.
	KeyID string
	// Replayed is true when an identical public key under the same ID was
	// already published and this item is an idempotent retransmission.
	Replayed bool
	// Err is nil for admitted (or replayed) items and non-nil otherwise.
	Err error
	// ConflictKeyID identifies the other key responsible for an
	// ErrFactorConflict, when known.
	ConflictKeyID string
	// ConflictFactor is the canonical hash of the prime factor causing the
	// conflict, if any.
	ConflictFactor []byte
}

// BatchError is returned by ImportBatch when at least one item is rejected.
// The published index is never modified when a BatchError is returned.
type BatchError struct {
	Results []ItemResult
}

func (e *BatchError) Error() string {
	rejected := 0
	for _, r := range e.Results {
		if r.Err != nil {
			rejected++
		}
	}
	return fmt.Sprintf("admission: batch rejected, %d of %d items failed", rejected, len(e.Results))
}

// publishedKey records an admitted public key and its factorization.
type publishedKey struct {
	n *big.Int
	e *big.Int
}

// Service is the concurrent-safe key admission service. The zero value is
// ready to use.
type Service struct {
	mu sync.Mutex
	// keys maps key ID to the published public key.
	keys map[string]*publishedKey
	// factorOwners maps a canonical prime-factor hash to the ID of the unique
	// key allowed to use that prime.
	factorOwners map[[sha256.Size]byte]string
}

// NewService creates an empty admission service.
func NewService() *Service {
	return &Service{
		keys:         make(map[string]*publishedKey),
		factorOwners: make(map[[sha256.Size]byte]string),
	}
}

// FactorHash returns the canonical hash of a prime factor: SHA-256 over the
// unsigned big-endian byte representation of the factor. Two big.Int values
// that are numerically equal always produce the same hash.
func FactorHash(p *big.Int) [sha256.Size]byte {
	return sha256.Sum256(p.Bytes())
}

// validate normalizes and validates a single request independently of any
// published state. It returns the canonical hashes of the factors, or an
// error wrapping ErrInvalidRequest.
func (req ImportRequest) validate() ([][sha256.Size]byte, error) {
	if req.KeyID == "" {
		return nil, fmt.Errorf("%w: empty key ID", ErrInvalidRequest)
	}
	if req.N == nil || req.E == nil {
		return nil, fmt.Errorf("%w: missing modulus or exponent", ErrInvalidRequest)
	}
	if req.N.Sign() <= 0 {
		return nil, fmt.Errorf("%w: non-positive modulus", ErrInvalidRequest)
	}
	if req.N.BitLen() < 128 {
		return nil, fmt.Errorf("%w: modulus shorter than 128 bits", ErrInvalidRequest)
	}
	if req.E.Sign() <= 0 {
		return nil, fmt.Errorf("%w: non-positive exponent", ErrInvalidRequest)
	}
	if len(req.Factors) < 2 {
		return nil, fmt.Errorf("%w: RSA modulus needs at least two prime factors", ErrInvalidRequest)
	}

	seen := make(map[[sha256.Size]byte]struct{}, len(req.Factors))
	product := new(big.Int).SetInt64(1)
	hashes := make([][sha256.Size]byte, 0, len(req.Factors))

	for _, p := range req.Factors {
		if p == nil || p.Sign() <= 0 || !p.ProbablyPrime(20) {
			return nil, fmt.Errorf("%w: factor is not a prime", ErrInvalidRequest)
		}
		h := FactorHash(p)
		if _, dup := seen[h]; dup {
			return nil, fmt.Errorf("%w: repeated prime factor", ErrInvalidRequest)
		}
		seen[h] = struct{}{}
		hashes = append(hashes, h)
		product.Mul(product, p)
	}

	if product.Cmp(req.N) != 0 {
		return nil, fmt.Errorf("%w: factor product does not equal modulus", ErrInvalidRequest)
	}

	// gcd(E, lambda(N)) must be 1 for E to be a valid RSA public exponent.
	// With square-free moduli, gcd(E, lambda(N)) = 1 is equivalent to E being
	// coprime with every (p-1).
	g := new(big.Int)
	for _, p := range req.Factors {
		pm1 := new(big.Int).Sub(p, big.NewInt(1))
		g.GCD(nil, nil, req.E, pm1)
		if g.Cmp(big.NewInt(1)) != 0 {
			return nil, fmt.Errorf("%w: exponent not coprime with lambda(n)", ErrInvalidRequest)
		}
	}

	return hashes, nil
}

// ImportBatch atomically admits a batch of public-key import requests.
//
// Every item is first validated on its own. The batch is then checked against
// both the published index and the other items of the same batch. Only when
// all items pass is anything published, so a rejected batch never pollutes the
// index. On success the per-item results are returned with Replayed set for
// idempotent retransmissions. On rejection a *BatchError carrying one result
// per input item is returned and no state changes occur.
func (s *Service) ImportBatch(requests []ImportRequest) ([]ItemResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.keys == nil {
		s.keys = make(map[string]*publishedKey)
		s.factorOwners = make(map[[sha256.Size]byte]string)
	}

	results := make([]ItemResult, len(requests))
	validHashes := make([][][sha256.Size]byte, len(requests))

	// Phase 1: per-item, state-independent validation.
	for i, req := range requests {
		results[i].KeyID = req.KeyID
		hashes, err := req.validate()
		if err != nil {
			results[i].Err = err
			continue
		}
		validHashes[i] = hashes
	}

	// A key ID appearing twice inside one batch is ambiguous and always fails,
	// even when the two copies are byte-identical.
	batchIDOwners := make(map[string]int)
	for i, req := range requests {
		if results[i].Err != nil {
			continue
		}
		if first, dup := batchIDOwners[req.KeyID]; dup {
			err := fmt.Errorf("%w: duplicate key ID within batch", ErrInvalidRequest)
			results[i].Err = err
			if results[first].Err == nil {
				results[first].Err = err
			}
			continue
		}
		batchIDOwners[req.KeyID] = i
	}

	// Phase 2: dry run against published state and the rest of the batch. The
	// staged map holds only not-yet-published ownership; the real maps are not
	// touched until every item has passed.
	stagedOwners := make(map[[sha256.Size]byte]int)

	for i, req := range requests {
		if results[i].Err != nil {
			continue
		}

		if existing, ok := s.keys[req.KeyID]; ok {
			if existing.n.Cmp(req.N) != 0 || existing.e.Cmp(req.E) != 0 {
				results[i].Err = fmt.Errorf("%w: key %q reuses ID with N=%s E=%s",
					ErrIDReused, req.KeyID, existing.n.String(), existing.e.String())
				continue
			}
			// Same (ID, N, E) and a verified factorization: the arithmetic
			// uniqueness of prime factorization guarantees the factor set is
			// the recorded one, so this is an idempotent replay. Replayed
			// items neither claim nor conflict on factors.
			results[i].Replayed = true
			continue
		}

		for _, h := range validHashes[i] {
			if ownerID, published := s.factorOwners[h]; published {
				results[i].Err = fmt.Errorf("%w: factor owned by published key %q",
					ErrFactorConflict, ownerID)
				results[i].ConflictKeyID = ownerID
				results[i].ConflictFactor = h[:]
				break
			}
			if other, staged := stagedOwners[h]; staged {
				results[i].Err = fmt.Errorf("%w: factor also used by batch item %q",
					ErrFactorConflict, requests[other].KeyID)
				results[i].ConflictKeyID = requests[other].KeyID
				results[i].ConflictFactor = h[:]
				break
			}
		}
		if results[i].Err == nil {
			for _, h := range validHashes[i] {
				stagedOwners[h] = i
			}
		}
	}

	for _, r := range results {
		if r.Err != nil {
			// Atomic failure: return without mutating any published state.
			return nil, &BatchError{Results: results}
		}
	}

	// Phase 3: commit every admissible item together.
	for i, req := range requests {
		if results[i].Replayed {
			continue
		}
		s.keys[req.KeyID] = &publishedKey{
			n: new(big.Int).Set(req.N),
			e: new(big.Int).Set(req.E),
		}
		for _, h := range validHashes[i] {
			s.factorOwners[h] = req.KeyID
		}
	}

	return results, nil
}

// PublishedKey reports whether a key ID has been admitted and returns a
// defensive copy of its public key.
func (s *Service) PublishedKey(keyID string) (*big.Int, *big.Int, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	k, ok := s.keys[keyID]
	if !ok {
		return nil, nil, false
	}
	return new(big.Int).Set(k.n), new(big.Int).Set(k.e), true
}

// FactorOwner returns the ID of the key that owns the given prime factor, or
// "" if the factor is not part of any published modulus.
func (s *Service) FactorOwner(p *big.Int) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.factorOwners[FactorHash(p)]
}

// PublishedCount returns the number of currently admitted key IDs.
func (s *Service) PublishedCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.keys)
}
