package blsregistry

import (
	"errors"
	"sync"
)

// Sentinel errors returned by the registry. Callers can use errors.Is to
// distinguish protocol rejections from internal failures.
var (
	// ErrNilPublicKey is returned when a nil public key is supplied.
	ErrNilPublicKey = errors.New("blsregistry: public key is nil")
	// ErrNilProof is returned when a nil proof of possession is supplied.
	ErrNilProof = errors.New("blsregistry: proof of possession is nil")
	// ErrNilSignature is returned when a nil aggregate signature is supplied.
	ErrNilSignature = errors.New("blsregistry: signature is nil")
	// ErrIdentityPublicKey is returned when a public key is the point at
	// infinity.
	ErrIdentityPublicKey = errors.New("blsregistry: public key is the point at infinity")
	// ErrInvalidProofOfPossession is returned when the proof of possession
	// does not verify against the supplied public key.
	ErrInvalidProofOfPossession = errors.New("blsregistry: proof of possession is invalid")
	// ErrBitmapLength is returned when the bitmap length differs from the
	// validator set size.
	ErrBitmapLength = errors.New("blsregistry: bitmap length does not match validator set size")
	// ErrNoSelectedValidators is returned when no bitmap bit is set.
	ErrNoSelectedValidators = errors.New("blsregistry: bitmap selects no validators")
	// ErrQuorumNotReached is returned when fewer validators than the quorum
	// threshold are selected by the bitmap.
	ErrQuorumNotReached = errors.New("blsregistry: number of selected validators is below quorum threshold")
	// ErrDistinctMessageCount is returned when the number of distinct-message
	// inputs does not match the number of selected validators.
	ErrDistinctMessageCount = errors.New("blsregistry: number of messages must match number of selected validators")
	// ErrDuplicateMessage is returned when distinct-message aggregation
	// receives two equal messages.
	ErrDuplicateMessage = errors.New("blsregistry: distinct-message aggregation requires pairwise distinct messages")
	// ErrPairingMismatch is returned when the aggregate pairing equation
	// does not hold, including identity-signature submissions.
	ErrPairingMismatch = errors.New("blsregistry: aggregate signature pairing check failed")
)

// Validator is one registered member of the committee.
type Validator struct {
	index int
	key   G1Point
}

// Index returns the position of the validator within the registry.
func (v Validator) Index() int { return v.index }

// PublicKey returns the registered BLS public key.
func (v Validator) PublicKey() G1Point { return v.key }

// Registry is the concurrency-safe set of registered validators.
type Registry struct {
	engine Engine
	quorum int

	mu    sync.RWMutex
	keys  []G1Point
	index map[string]int
}

// NewRegistry creates an empty registry. quorum is the minimum number of
// selected validators that any aggregate verification requires and must be
// positive.
func NewRegistry(engine Engine, quorum int) (*Registry, error) {
	if engine == nil {
		return nil, errors.New("blsregistry: engine must not be nil")
	}
	if quorum <= 0 {
		return nil, errors.New("blsregistry: quorum threshold must be positive")
	}
	return &Registry{
		engine: engine,
		quorum: quorum,
		index:  make(map[string]int),
	}, nil
}

// Quorum returns the configured quorum threshold.
func (r *Registry) Quorum() int { return r.quorum }

// Size returns the number of registered validators.
func (r *Registry) Size() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.keys)
}

// Validators returns a snapshot of the registered validators in registry
// order.
func (r *Registry) Validators() []Validator {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]Validator, len(r.keys))
	for i, key := range r.keys {
		out[i] = Validator{index: i, key: key}
	}
	return out
}

// Register adds publicKey to the validator set.
//
// proof must be a proof of possession produced by
// SignProofOfPossession: a signature over the canonical encoding of
// publicKey in the DomainProofOfPossession domain. Identity public keys and
// invalid or missing proofs are rejected. Registering an already present
// public key (by encoded equality) is ignored and returns nil.
func (r *Registry) Register(key G1Point, proof G2Point) error {
	if key == nil {
		return ErrNilPublicKey
	}
	if proof == nil {
		return ErrNilProof
	}
	if key.IsIdentity() {
		return ErrIdentityPublicKey
	}
	if err := verifyProofOfPossession(r.engine, key, proof); err != nil {
		return err
	}

	encoded := string(key.Encode())
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.index[encoded]; exists {
		return nil
	}
	r.index[encoded] = len(r.keys)
	r.keys = append(r.keys, key)
	return nil
}

// selectedKeys validates bitmap against the current set and returns the
// selected public keys in ascending registry-index order.
func (r *Registry) selectedKeys(bitmap []bool) ([]G1Point, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	if len(bitmap) != len(r.keys) {
		return nil, ErrBitmapLength
	}
	selected := make([]G1Point, 0, len(r.keys))
	for i, on := range bitmap {
		if on {
			selected = append(selected, r.keys[i])
		}
	}
	if len(selected) == 0 {
		return nil, ErrNoSelectedValidators
	}
	if len(selected) < r.quorum {
		return nil, ErrQuorumNotReached
	}
	return selected, nil
}

// VerifyAggregateSameMessage verifies an aggregate BLS signature over a
// single message.
//
// The aggregate public key is summed exclusively from registered public
// keys selected by bitmap, in registry order. The bitmap length must equal
// the validator set size, and the number of selected validators must reach
// the quorum threshold. The verification equation is
//
//	e(sum(PK_i), H_sig(message)) * e(G1, -aggregateSignature) == 1_GT.
func (r *Registry) VerifyAggregateSameMessage(message []byte, bitmap []bool, aggregateSignature G2Point) error {
	if aggregateSignature == nil {
		return ErrNilSignature
	}
	if aggregateSignature.IsIdentity() {
		return ErrPairingMismatch
	}
	keys, err := r.selectedKeys(bitmap)
	if err != nil {
		return err
	}

	aggregateKey := keys[0]
	for _, key := range keys[1:] {
		aggregateKey = aggregateKey.Add(key)
	}
	hashed := r.engine.HashToG2(DomainSignature, message)
	if !r.engine.CheckPairing(
		[]G1Point{aggregateKey, r.engine.G1Generator()},
		[]G2Point{hashed, aggregateSignature.Neg()},
	) {
		return ErrPairingMismatch
	}
	return nil
}

// VerifyAggregateDistinctMessages verifies an aggregate BLS signature where
// every selected validator signed its own message.
//
// messages[i] corresponds to the i-th selected validator in ascending
// registry-index order; len(messages) must equal the number of selected
// validators and the messages must be pairwise distinct. The bitmap length
// must equal the validator set size and the selected count must reach the
// quorum threshold. The verification equation is
//
//	prod_i e(PK_i, H_sig(messages[i])) * e(G1, -aggregateSignature) == 1_GT.
func (r *Registry) VerifyAggregateDistinctMessages(messages [][]byte, bitmap []bool, aggregateSignature G2Point) error {
	if aggregateSignature == nil {
		return ErrNilSignature
	}
	if aggregateSignature.IsIdentity() {
		return ErrPairingMismatch
	}
	keys, err := r.selectedKeys(bitmap)
	if err != nil {
		return err
	}
	if len(messages) != len(keys) {
		return ErrDistinctMessageCount
	}

	g1 := make([]G1Point, 0, len(keys)+1)
	g2 := make([]G2Point, 0, len(keys)+1)
	seen := make(map[string]struct{}, len(messages))
	for i, message := range messages {
		raw := string(message)
		if _, duplicate := seen[raw]; duplicate {
			return ErrDuplicateMessage
		}
		seen[raw] = struct{}{}
		g1 = append(g1, keys[i])
		g2 = append(g2, r.engine.HashToG2(DomainSignature, message))
	}
	g1 = append(g1, r.engine.G1Generator())
	g2 = append(g2, aggregateSignature.Neg())

	if !r.engine.CheckPairing(g1, g2) {
		return ErrPairingMismatch
	}
	return nil
}

// verifyProofOfPossession checks proof against key in the dedicated PoP
// domain: e(PK, H_pop(Encode(PK))) * e(G1, -proof) == 1_GT.
func verifyProofOfPossession(engine Engine, key G1Point, proof G2Point) error {
	if proof.IsIdentity() {
		return ErrInvalidProofOfPossession
	}
	hashed := engine.HashToG2(DomainProofOfPossession, key.Encode())
	if !engine.CheckPairing(
		[]G1Point{key, engine.G1Generator()},
		[]G2Point{hashed, proof.Neg()},
	) {
		return ErrInvalidProofOfPossession
	}
	return nil
}
