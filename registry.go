// Package netsec provides verifier registration and aggregate BLS signature
// verification for consensus nodes.
//
// Pairing arithmetic and hash-to-curve are supplied by the caller through the
// PairingEngine interface.  The package deliberately does not depend on a
// particular pairing-friendly curve implementation.
package netsec

import (
	"bytes"
	"errors"
	"fmt"
	"sync"
)

var (
	// ErrInvalidEngine is returned when the injected engine is unusable.
	ErrInvalidEngine = errors.New("bls: invalid pairing engine")
	// ErrEmptyDomain is returned when either domain separation tag is empty.
	ErrEmptyDomain = errors.New("bls: empty domain separation tag")
	// ErrDomainCollision is returned when signature and proof-of-possession
	// domain separation tags are identical.
	ErrDomainCollision = errors.New("bls: signature and proof-of-possession domains must differ")
	// ErrInvalidPublicKey is returned for a nil or otherwise unusable public key.
	ErrInvalidPublicKey = errors.New("bls: invalid public key")
	// ErrInfinityPublicKey is returned when a public key is the point at infinity.
	ErrInfinityPublicKey = errors.New("bls: public key is the point at infinity")
	// ErrInfinitySignature is returned when a signature is the point at infinity.
	ErrInfinitySignature = errors.New("bls: signature is the point at infinity")
	// ErrInfinityAggregatePublicKey is returned when selected public keys sum to
	// the point at infinity.
	ErrInfinityAggregatePublicKey = errors.New("bls: aggregate public key is the point at infinity")
	// ErrInfinityHash is returned if the injected hash-to-curve returns infinity.
	ErrInfinityHash = errors.New("bls: hash-to-curve returned the point at infinity")
	// ErrInvalidProofOfPossession is returned when a public key's proof of
	// possession does not verify.
	ErrInvalidProofOfPossession = errors.New("bls: invalid proof of possession")
	// ErrInvalidThreshold is returned when the threshold is not positive.
	ErrInvalidThreshold = errors.New("bls: threshold must be positive")
	// ErrThresholdNotMet is returned when fewer than threshold verifiers are
	// selected or participate.
	ErrThresholdNotMet = errors.New("bls: threshold not met")
	// ErrBitmapLengthMismatch is returned when the bitmap length differs from
	// the number of registered verifiers.
	ErrBitmapLengthMismatch = errors.New("bls: bitmap length does not match verifier set size")
	// ErrMessageCountMismatch is returned when the number of messages does not
	// match the number of selected verifiers.
	ErrMessageCountMismatch = errors.New("bls: message count does not match selected verifiers")
	// ErrDuplicateMessage is returned when a multi-message aggregate contains
	// the same message more than once.
	ErrDuplicateMessage = errors.New("bls: duplicate message in aggregate")
	// ErrDuplicateSigner is returned when a multi-message aggregate names the
	// same verifier more than once.
	ErrDuplicateSigner = errors.New("bls: duplicate signer in aggregate")
	// ErrUnknownSigner is returned when a signer index is outside the verifier set.
	ErrUnknownSigner = errors.New("bls: signer is not in the verifier set")
	// ErrInvalidAggregateSignature is returned when an aggregate signature does
	// not verify under the selected public keys.
	ErrInvalidAggregateSignature = errors.New("bls: invalid aggregate signature")
)

// Point is the minimal group interface required by this package.  A point
// value must be safe for concurrent reads; Add must not mutate either operand.
type Point interface {
	// Add returns p + q.
	Add(q Point) Point
	// Equal reports whether p and q are the same group element.
	Equal(q Point) bool
	// IsInfinity reports whether p is the identity element of the group.
	IsInfinity() bool
	// Bytes returns the canonical encoding of p.  It is used as the message
	// signed by a proof of possession and as the registry's key identifier.
	Bytes() []byte
}

// PairingValue is an element of the target group.  Mul must not mutate either
// operand.
type PairingValue interface {
	// Mul returns a * b in the target group.
	Mul(b PairingValue) PairingValue
	// Equal reports whether a and b are the same target-group element.
	Equal(b PairingValue) bool
}

// PairingEngine is implemented by the caller for its chosen curve.
//
// HashToCurve must map a message to the signature group using the supplied
// domain separation tag.  Pair must implement a non-degenerate bilinear map
// such that Pair(generator, x) is usable for standard BLS verification.  The
// interface also supports asymmetric (type-3) pairings: public keys may live
// in a different group from hash-to-curve outputs and signatures.
type PairingEngine interface {
	// Generator returns the public-key-group generator.
	Generator() Point
	// Infinity returns the public-key-group identity.  It is used as the
	// starting point when aggregating public keys, so it must have the same
	// concrete group as Generator and registered public keys.
	Infinity() Point
	// HashToCurve hashes message under the supplied domain separation tag.
	HashToCurve(message, domain []byte) (Point, error)
	// Pair returns e(a, b).
	Pair(a, b Point) PairingValue
}

// Domains holds the domain separation tags used for ordinary signatures and
// proofs of possession.  They must be non-empty and distinct.
type Domains struct {
	Signature         []byte
	ProofOfPossession []byte
}

// DefaultDomains returns the package's default, distinct domain separation
// tags.  The returned byte slices are owned by the caller.
func DefaultDomains() Domains {
	return Domains{
		Signature:         []byte("example.org/netsec/q0817:BLS_SIGNATURE:v1"),
		ProofOfPossession: []byte("example.org/netsec/q0817:BLS_PROOF_OF_POSSESSION:v1"),
	}
}

// Validate checks that both domain separation tags are present and distinct.
func (d Domains) Validate() error {
	if len(d.Signature) == 0 || len(d.ProofOfPossession) == 0 {
		return ErrEmptyDomain
	}
	if bytes.Equal(d.Signature, d.ProofOfPossession) {
		return ErrDomainCollision
	}
	return nil
}

func (d Domains) clone() Domains {
	return Domains{
		Signature:         append([]byte(nil), d.Signature...),
		ProofOfPossession: append([]byte(nil), d.ProofOfPossession...),
	}
}

// SignerMessage identifies a registered verifier and the message it signed in
// a multi-message aggregate.
type SignerMessage struct {
	Signer  int
	Message []byte
}

// VerifierSet is a concurrency-safe, append-only set of verifiers that have
// proven possession of their public keys.  Registration order is stable and
// defines bitmap bit positions as well as SignerMessage indexes.
type VerifierSet struct {
	mu      sync.RWMutex
	engine  PairingEngine
	domains Domains
	keys    []Point
	byKey   map[string]int
}

// Registry is an alias for VerifierSet.
type Registry = VerifierSet

// NewVerifierSet constructs an empty verifier set.  The engine and domains are
// validated immediately; mismatched signature and proof-of-possession domains
// are rejected.
func NewVerifierSet(engine PairingEngine, domains Domains) (*VerifierSet, error) {
	if engine == nil {
		return nil, ErrInvalidEngine
	}
	if err := domains.Validate(); err != nil {
		return nil, err
	}

	generator := engine.Generator()
	infinity := engine.Infinity()
	if generator == nil || infinity == nil || generator.IsInfinity() || !infinity.IsInfinity() {
		return nil, ErrInvalidEngine
	}

	return &VerifierSet{
		engine:  engine,
		domains: domains.clone(),
		byKey:   make(map[string]int),
	}, nil
}

// NewDefaultVerifierSet constructs an empty verifier set with DefaultDomains.
func NewDefaultVerifierSet(engine PairingEngine) (*VerifierSet, error) {
	return NewVerifierSet(engine, DefaultDomains())
}

// NewRegistry is an alias for NewVerifierSet.
func NewRegistry(engine PairingEngine, domains Domains) (*VerifierSet, error) {
	return NewVerifierSet(engine, domains)
}

// Len returns the number of registered verifiers.
func (s *VerifierSet) Len() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.keys)
}

// PublicKeys returns a copy of the verifier set in registration order.
func (s *VerifierSet) PublicKeys() []Point {
	s.mu.RLock()
	defer s.mu.RUnlock()
	keys := make([]Point, len(s.keys))
	copy(keys, s.keys)
	return keys
}

// PublicKey returns the verifier at index in registration order.
func (s *VerifierSet) PublicKey(index int) (Point, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if index < 0 || index >= len(s.keys) {
		return nil, ErrUnknownSigner
	}
	return s.keys[index], nil
}

// Contains reports whether pub is already registered.
func (s *VerifierSet) Contains(pub Point) bool {
	if pub == nil {
		return false
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	_, ok := s.findLocked(pub)
	return ok
}

// Register verifies a proof of possession over the exact public-key bytes and
// adds the key on success.  Registering an already-present public key is
// ignored and returns nil.  A key that fails proof-of-possession verification
// is never added.
func (s *VerifierSet) Register(pub, proofOfPossession Point) error {
	if pub == nil {
		return ErrInvalidPublicKey
	}
	if pub.IsInfinity() {
		return ErrInfinityPublicKey
	}

	// Duplicate registration is a no-op.  Checking before the proof both makes
	// the operation idempotent and avoids relying on a proof supplied for an
	// already trusted key.
	s.mu.RLock()
	_, duplicate := s.findLocked(pub)
	s.mu.RUnlock()
	if duplicate {
		return nil
	}

	if err := s.VerifyProofOfPossession(pub, proofOfPossession); err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if _, duplicate := s.findLocked(pub); duplicate {
		return nil
	}
	s.byKey[string(pub.Bytes())] = len(s.keys)
	s.keys = append(s.keys, pub)
	return nil
}

// VerifyProofOfPossession verifies that proof is a BLS signature over the
// public key's own canonical bytes under the proof-of-possession domain.
func (s *VerifierSet) VerifyProofOfPossession(pub, proof Point) error {
	if pub == nil {
		return ErrInvalidPublicKey
	}
	if pub.IsInfinity() {
		return ErrInfinityPublicKey
	}
	if proof == nil || proof.IsInfinity() {
		return ErrInvalidProofOfPossession
	}

	hash, err := s.engine.HashToCurve(pub.Bytes(), s.domains.ProofOfPossession)
	if err != nil {
		return fmt.Errorf("bls: hash public key for proof of possession: %w", err)
	}
	if hash == nil {
		return ErrInvalidEngine
	}
	if hash.IsInfinity() {
		return ErrInfinityHash
	}

	left := s.engine.Pair(pub, hash)
	right := s.engine.Pair(s.engine.Generator(), proof)
	if left == nil || right == nil {
		return ErrInvalidEngine
	}
	if !left.Equal(right) {
		return ErrInvalidProofOfPossession
	}
	return nil
}

// VerifySameMessage verifies an aggregate signature over one message.
//
// The aggregate public key is constructed internally from exactly the
// registered keys whose bitmap byte is non-zero.  A caller cannot supply or
// influence an aggregate public key.  The bitmap must have one byte per
// registered verifier, and the number of selected verifiers must be at least
// threshold.
func (s *VerifierSet) VerifySameMessage(bitmap []byte, message []byte, signature Point, threshold int) error {
	if threshold <= 0 {
		return ErrInvalidThreshold
	}
	if signature == nil || signature.IsInfinity() {
		return ErrInfinitySignature
	}

	keys, domains := s.snapshot()
	selected, err := selectedIndexes(bitmap, len(keys))
	if err != nil {
		return err
	}
	if len(selected) < threshold {
		return ErrThresholdNotMet
	}

	aggregate, err := s.aggregateSelected(keys, selected)
	if err != nil {
		return err
	}

	hash, err := s.engine.HashToCurve(message, domains.Signature)
	if err != nil {
		return fmt.Errorf("bls: hash message: %w", err)
	}
	if hash == nil {
		return ErrInvalidEngine
	}
	if hash.IsInfinity() {
		return ErrInfinityHash
	}

	left := s.engine.Pair(aggregate, hash)
	right := s.engine.Pair(s.engine.Generator(), signature)
	if left == nil || right == nil {
		return ErrInvalidEngine
	}
	if !left.Equal(right) {
		return ErrInvalidAggregateSignature
	}
	return nil
}

// AggregatePublicKey returns the sum of the registered keys selected by
// bitmap and the number of selected keys.  It performs the same length and
// threshold checks as VerifySameMessage.
func (s *VerifierSet) AggregatePublicKey(bitmap []byte, threshold int) (Point, int, error) {
	if threshold <= 0 {
		return nil, 0, ErrInvalidThreshold
	}
	keys, _ := s.snapshot()
	selected, err := selectedIndexes(bitmap, len(keys))
	if err != nil {
		return nil, 0, err
	}
	if len(selected) < threshold {
		return nil, 0, ErrThresholdNotMet
	}
	aggregate, err := s.aggregateSelected(keys, selected)
	if err != nil {
		return nil, 0, err
	}
	return aggregate, len(selected), nil
}

// VerifyDistinctMessages verifies one aggregate signature over messages signed
// by different registered verifiers.
//
// The bitmap has one byte per registered verifier.  messages corresponds, in
// ascending signer-index order, to the selected verifiers.  Messages must be
// pairwise distinct; nil and []byte{} are treated as the same message.  At
// least threshold verifiers must be selected.
func (s *VerifierSet) VerifyDistinctMessages(bitmap []byte, messages [][]byte, signature Point, threshold int) error {
	if threshold <= 0 {
		return ErrInvalidThreshold
	}
	if signature == nil || signature.IsInfinity() {
		return ErrInfinitySignature
	}

	keys, domains := s.snapshot()
	selected, err := selectedIndexes(bitmap, len(keys))
	if err != nil {
		return err
	}
	if len(selected) < threshold {
		return ErrThresholdNotMet
	}
	if len(messages) != len(selected) {
		return ErrMessageCountMismatch
	}
	if hasDuplicateMessage(messages) {
		return ErrDuplicateMessage
	}

	product, err := s.verifyMultiMessageProduct(keys, selected, messages, domains.Signature)
	if err != nil {
		return err
	}
	right := s.engine.Pair(s.engine.Generator(), signature)
	if right == nil {
		return ErrInvalidEngine
	}
	if !product.Equal(right) {
		return ErrInvalidAggregateSignature
	}
	return nil
}

// VerifyDistinctSignerMessages verifies one aggregate signature over messages
// identified by explicit registered signer indexes.  It is the non-bitmap
// counterpart to VerifyDistinctMessages.  Signer indexes and messages must
// each be pairwise distinct, and all signers must already be registered.
func (s *VerifierSet) VerifyDistinctSignerMessages(entries []SignerMessage, signature Point, threshold int) error {
	if threshold <= 0 {
		return ErrInvalidThreshold
	}
	if signature == nil || signature.IsInfinity() {
		return ErrInfinitySignature
	}
	if len(entries) < threshold {
		return ErrThresholdNotMet
	}

	keys, domains := s.snapshot()
	selected := make([]int, len(entries))
	messages := make([][]byte, len(entries))
	seen := make(map[int]struct{}, len(entries))
	for i, entry := range entries {
		if entry.Signer < 0 || entry.Signer >= len(keys) {
			return ErrUnknownSigner
		}
		if _, ok := seen[entry.Signer]; ok {
			return ErrDuplicateSigner
		}
		seen[entry.Signer] = struct{}{}
		selected[i] = entry.Signer
		messages[i] = entry.Message
	}
	if hasDuplicateMessage(messages) {
		return ErrDuplicateMessage
	}

	product, err := s.verifyMultiMessageProduct(keys, selected, messages, domains.Signature)
	if err != nil {
		return err
	}
	right := s.engine.Pair(s.engine.Generator(), signature)
	if right == nil {
		return ErrInvalidEngine
	}
	if !product.Equal(right) {
		return ErrInvalidAggregateSignature
	}
	return nil
}

// VerifyAggregateDifferentMessages is an alias for VerifyDistinctMessages.
func (s *VerifierSet) VerifyAggregateDifferentMessages(bitmap []byte, messages [][]byte, signature Point, threshold int) error {
	return s.VerifyDistinctMessages(bitmap, messages, signature, threshold)
}

func (s *VerifierSet) snapshot() ([]Point, Domains) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	keys := make([]Point, len(s.keys))
	copy(keys, s.keys)
	return keys, s.domains.clone()
}

func (s *VerifierSet) findLocked(pub Point) (int, bool) {
	encoded := pub.Bytes()
	if index, ok := s.byKey[string(encoded)]; ok {
		return index, true
	}
	for index, key := range s.keys {
		if key.Equal(pub) {
			return index, true
		}
	}
	return 0, false
}

func (s *VerifierSet) aggregateSelected(keys []Point, selected []int) (Point, error) {
	aggregate := s.engine.Infinity()
	if aggregate == nil || !aggregate.IsInfinity() {
		return nil, ErrInvalidEngine
	}
	for _, index := range selected {
		key := keys[index]
		if key == nil {
			return nil, ErrInvalidPublicKey
		}
		if key.IsInfinity() {
			return nil, ErrInfinityPublicKey
		}
		aggregate = aggregate.Add(key)
		if aggregate == nil {
			return nil, ErrInvalidEngine
		}
	}
	if aggregate.IsInfinity() {
		return nil, ErrInfinityAggregatePublicKey
	}
	return aggregate, nil
}

func (s *VerifierSet) verifyMultiMessageProduct(keys []Point, selected []int, messages [][]byte, domain []byte) (PairingValue, error) {
	var product PairingValue
	for i, index := range selected {
		key := keys[index]
		if key == nil {
			return nil, ErrInvalidPublicKey
		}
		if key.IsInfinity() {
			return nil, ErrInfinityPublicKey
		}
		hash, err := s.engine.HashToCurve(messages[i], domain)
		if err != nil {
			return nil, fmt.Errorf("bls: hash message: %w", err)
		}
		if hash == nil {
			return nil, ErrInvalidEngine
		}
		if hash.IsInfinity() {
			return nil, ErrInfinityHash
		}
		pair := s.engine.Pair(key, hash)
		if pair == nil {
			return nil, ErrInvalidEngine
		}
		if product == nil {
			product = pair
		} else {
			product = product.Mul(pair)
			if product == nil {
				return nil, ErrInvalidEngine
			}
		}
	}
	return product, nil
}

func selectedIndexes(bitmap []byte, setSize int) ([]int, error) {
	if len(bitmap) != setSize {
		return nil, ErrBitmapLengthMismatch
	}
	selected := make([]int, 0, setSize)
	for index, flag := range bitmap {
		if flag != 0 {
			selected = append(selected, index)
		}
	}
	return selected, nil
}

func hasDuplicateMessage(messages [][]byte) bool {
	seen := make(map[string]struct{}, len(messages))
	for _, message := range messages {
		key := string(message)
		if _, ok := seen[key]; ok {
			return true
		}
		seen[key] = struct{}{}
	}
	return false
}
