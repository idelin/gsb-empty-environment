package netsec

import (
	"bytes"
	"crypto/ed25519"
	"errors"
	"fmt"
	"math"
	"sort"
)

// ChainID identifies a source or destination chain.
type ChainID string

const maxChainIDLength = 128

// Validate checks that the chain identifier is non-empty and bounded.
func (c ChainID) Validate() error {
	if len(c) == 0 {
		return errors.New("netsec: empty chain id")
	}
	if len(c) > maxChainIDLength {
		return fmt.Errorf("netsec: chain id exceeds %d bytes", maxChainIDLength)
	}
	return nil
}

// Header is a source-chain block header committed to by the validator set.
//
// BlockHash is the header hash and commits to every other field. TxRoot and
// EventRoot are Merkle roots over transaction leaves and event leaves.
type Header struct {
	ChainID        ChainID `json:"chain_id"`
	Height         uint64  `json:"height"`
	BlockHash      Hash    `json:"block_hash"`
	ParentHash     Hash    `json:"parent_hash"`
	TxRoot         Hash    `json:"tx_root"`
	EventRoot      Hash    `json:"event_root"`
	Timestamp      int64   `json:"timestamp"`
	ValidatorsHash Hash    `json:"validators_hash"`
}

// ComputeHash returns the canonical hash of the header fields.
func (h Header) ComputeHash() Hash {
	digest := hashDomainSeparated("header/v1",
		[]byte(h.ChainID),
		uint64Bytes(h.Height),
		h.ParentHash[:],
		h.TxRoot[:],
		h.EventRoot[:],
		int64Bytes(h.Timestamp),
		h.ValidatorsHash[:],
	)
	return digest
}

// SignBytes returns the digest validators sign for this header.
func (h Header) SignBytes() Hash {
	return hashDomainSeparated("header-signature/v1", h.BlockHash[:])
}

// Validate performs structural header checks, including BlockHash
// self-consistency.
func (h Header) Validate() error {
	if err := h.ChainID.Validate(); err != nil {
		return err
	}
	if h.BlockHash.IsZero() {
		return errors.New("netsec: zero block hash")
	}
	if h.Height > 0 && h.ParentHash.IsZero() {
		return errors.New("netsec: non-genesis block has zero parent hash")
	}
	if h.TxRoot.IsZero() {
		return errors.New("netsec: zero transaction root")
	}
	if h.EventRoot.IsZero() {
		return errors.New("netsec: zero event root")
	}
	if h.ValidatorsHash.IsZero() {
		return errors.New("netsec: zero validator set hash")
	}
	if computed := h.ComputeHash(); computed != h.BlockHash {
		return fmt.Errorf("%w: block hash does not commit to header fields", ErrInvalidHeader)
	}
	return nil
}

func uint64Bytes(v uint64) []byte {
	var buf [8]byte
	for i := 7; i >= 0; i-- {
		buf[i] = byte(v)
		v >>= 8
	}
	return buf[:]
}

func int64Bytes(v int64) []byte {
	return uint64Bytes(uint64(v))
}

// Validator is a consensus validator with voting power.
type Validator struct {
	PublicKey ed25519.PublicKey `json:"public_key"`
	Power     uint64            `json:"power"`
}

// ValidatorSet is a static validator set trusted for one source chain.
// Instances built with NewValidatorSet are sorted by public key so that
// ValidatorSet.Hash is deterministic.
type ValidatorSet struct {
	Validators []Validator `json:"validators"`
}

// NewValidatorSet validates, copies, and canonically sorts validators.
func NewValidatorSet(validators []Validator) (ValidatorSet, error) {
	if len(validators) == 0 {
		return ValidatorSet{}, errors.New("netsec: empty validator set")
	}
	if len(validators) > 1024 {
		return ValidatorSet{}, errors.New("netsec: too many validators")
	}
	copied := make([]Validator, len(validators))
	seen := make(map[string]struct{}, len(validators))
	for i, v := range validators {
		if len(v.PublicKey) != ed25519.PublicKeySize {
			return ValidatorSet{}, fmt.Errorf("netsec: validator %d has invalid public key length", i)
		}
		if v.Power == 0 {
			return ValidatorSet{}, fmt.Errorf("netsec: validator %d has zero power", i)
		}
		key := string(v.PublicKey)
		if _, exists := seen[key]; exists {
			return ValidatorSet{}, fmt.Errorf("netsec: duplicate validator %d", i)
		}
		seen[key] = struct{}{}
		copied[i] = Validator{
			PublicKey: append(ed25519.PublicKey(nil), v.PublicKey...),
			Power:     v.Power,
		}
	}
	sort.Slice(copied, func(i, j int) bool {
		return bytes.Compare(copied[i].PublicKey, copied[j].PublicKey) < 0
	})
	var total uint64
	for _, v := range copied {
		if math.MaxUint64-total < v.Power {
			return ValidatorSet{}, errors.New("netsec: total validator power overflows uint64")
		}
		total += v.Power
	}
	return ValidatorSet{Validators: copied}, nil
}

// Hash returns a commitment to the canonicalized validator set.
func (vs ValidatorSet) Hash() Hash {
	sorted := append([]Validator(nil), vs.Validators...)
	sort.Slice(sorted, func(i, j int) bool {
		return bytes.Compare(sorted[i].PublicKey, sorted[j].PublicKey) < 0
	})
	parts := make([][]byte, 0, 1+2*len(sorted))
	parts = append(parts, uint64Bytes(uint64(len(sorted))))
	for _, v := range sorted {
		parts = append(parts, v.PublicKey, uint64Bytes(v.Power))
	}
	return hashDomainSeparated("validator-set/v1", parts...)
}

// TotalPower sums validator voting power.
func (vs ValidatorSet) TotalPower() uint64 {
	var total uint64
	for _, v := range vs.Validators {
		if math.MaxUint64-total < v.Power {
			return math.MaxUint64
		}
		total += v.Power
	}
	return total
}

// QuorumPower returns the minimum power required for a >2/3 quorum.
func QuorumPower(total uint64) uint64 {
	if total == 0 {
		return 0
	}
	// Avoid overflow for adversarial power values.
	if total > math.MaxUint64/2 {
		total = math.MaxUint64 / 2
	}
	return (2*total)/3 + 1
}

// HeaderSignature is one validator's signature over a header. ValidatorIndex
// indexes ValidatorSet.Validators in its canonical (public-key sorted) order.
type HeaderSignature struct {
	ValidatorIndex uint32 `json:"validator_index"`
	Signature      []byte `json:"signature"`
}

// VerifyHeaderSignatureSet verifies that signatures represent more than
// two-thirds of the validator set's power. Duplicate signer indices are
// rejected so the same validator cannot be counted twice.
func VerifyHeaderSignatureSet(h Header, signatures []HeaderSignature, validators ValidatorSet) error {
	if len(validators.Validators) == 0 {
		return errors.New("netsec: empty validator set")
	}
	if len(signatures) == 0 {
		return fmt.Errorf("%w: no signatures", ErrInsufficientPower)
	}
	if len(signatures) > len(validators.Validators) {
		return fmt.Errorf("%w: more signatures than validators", ErrInvalidConsensus)
	}
	signBytes := h.SignBytes()
	seen := make(map[uint32]struct{}, len(signatures))
	var signedPower uint64
	for i, sig := range signatures {
		if int(sig.ValidatorIndex) >= len(validators.Validators) {
			return fmt.Errorf("%w: signature %d has unknown validator index", ErrInvalidConsensus, i)
		}
		if _, exists := seen[sig.ValidatorIndex]; exists {
			return fmt.Errorf("%w: duplicate validator index %d", ErrInvalidConsensus, sig.ValidatorIndex)
		}
		seen[sig.ValidatorIndex] = struct{}{}
		validator := validators.Validators[sig.ValidatorIndex]
		if len(sig.Signature) != ed25519.SignatureSize {
			return fmt.Errorf("%w: signature %d has invalid length", ErrInvalidConsensus, i)
		}
		if !ed25519.Verify(validator.PublicKey, signBytes[:], sig.Signature) {
			return fmt.Errorf("%w: signature %d is invalid", ErrInvalidConsensus, i)
		}
		if math.MaxUint64-signedPower < validator.Power {
			return fmt.Errorf("%w: signed power overflow", ErrInvalidConsensus)
		}
		signedPower += validator.Power
	}
	total := validators.TotalPower()
	if signedPower < QuorumPower(total) {
		return fmt.Errorf("%w: signed power %d, need %d of %d", ErrInsufficientPower, signedPower, QuorumPower(total), total)
	}
	return nil
}
