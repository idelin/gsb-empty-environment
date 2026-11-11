package netsec

import (
	"bytes"
	"encoding/json"
	"fmt"
)

// Chain is an ordered, immutable sequence of signed attestations. The only
// way to extend it is Append, which binds a new signature to the digest of
// the current tail; attestations can never be inserted or rewritten.
type Chain struct {
	attestations []*Attestation
}

// NewChain starts a proof chain with its root attestation. The root must use
// index 0, bind the genesis digest for its audience and challenge, and report
// AssuranceNone as UpstreamMin because no hop precedes it.
func NewChain(root *Attestation) (*Chain, error) {
	if root == nil {
		return nil, fmt.Errorf("%w: root attestation is nil", ErrEmptyChain)
	}
	if err := validateShape(root, 0); err != nil {
		return nil, err
	}
	if root.UpstreamMin != AssuranceNone {
		return nil, fmt.Errorf("%w: root must report upstream minimum %q, got %q",
			ErrAssuranceElevation, AssuranceNone, root.UpstreamMin)
	}
	genesis := GenesisDigest(root.Audience, root.Challenge)
	if !bytes.Equal(root.PrevDigest, genesis[:]) {
		return nil, fmt.Errorf("%w: root must bind the genesis digest", ErrBrokenChain)
	}
	out := make([]*Attestation, 1)
	out[0] = cloneAttestation(root)
	return &Chain{attestations: out}, nil
}

// Append returns a new chain extended with a, leaving the receiver unchanged.
// a must be signed by signer, occupy the next index, bind the digest of the
// current tail, and carry the same audience, challenge and validity semantics
// as the rest of the chain.
func (c *Chain) Append(a *Attestation, signer *Signer) (*Chain, error) {
	if c == nil || len(c.attestations) == 0 {
		return nil, ErrEmptyChain
	}
	if a == nil || signer == nil {
		return nil, fmt.Errorf("%w: attestation and signer are required", ErrInvalidAttestation)
	}
	index := len(c.attestations)
	if err := validateShape(a, index); err != nil {
		return nil, err
	}
	tail := c.attestations[index-1]
	tailDigest, err := AttestationDigest(tail)
	if err != nil {
		return nil, err
	}
	if !bytes.Equal(a.PrevDigest, tailDigest[:]) {
		return nil, fmt.Errorf("%w: hop %d does not bind attestation %d",
			ErrBrokenChain, index, index-1)
	}
	if a.Audience != tail.Audience {
		return nil, fmt.Errorf("%w: appended audience %q, chain audience %q",
			ErrAudienceMismatch, a.Audience, tail.Audience)
	}
	if a.Challenge != tail.Challenge {
		return nil, fmt.Errorf("%w: appended challenge %q, chain challenge %q",
			ErrChallengeMismatch, a.Challenge, tail.Challenge)
	}
	if a.HopID != signer.HopID() || a.KeyID != signer.KeyID() || a.SigAlg != signer.Alg() {
		return nil, fmt.Errorf("%w: attestation does not match the supplied signer",
			ErrHopIdentityMismatch)
	}
	if err := verifySignature(a, trustedKey{
		hopID: signer.HopID(),
		alg:   signer.Alg(),
		key:   signer.Public(),
	}); err != nil {
		return nil, err
	}
	extended := make([]*Attestation, 0, index+1)
	extended = append(extended, c.attestations...)
	extended = append(extended, cloneAttestation(a))
	return &Chain{attestations: extended}, nil
}

// Len returns the number of attestations in the chain.
func (c *Chain) Len() int {
	if c == nil {
		return 0
	}
	return len(c.attestations)
}

// At returns a copy of the attestation at index i.
func (c *Chain) At(i int) (*Attestation, error) {
	if c == nil || i < 0 || i >= len(c.attestations) {
		return nil, fmt.Errorf("%w: attestation index %d out of range", ErrInvalidAttestation, i)
	}
	return cloneAttestation(c.attestations[i]), nil
}

// Attestations returns copies of all attestations in chain order.
func (c *Chain) Attestations() []*Attestation {
	if c == nil {
		return nil
	}
	out := make([]*Attestation, len(c.attestations))
	for i, a := range c.attestations {
		out[i] = cloneAttestation(a)
	}
	return out
}

func validateShape(a *Attestation, expectedIndex int) error {
	if a.Version != Version {
		return fmt.Errorf("%w: unsupported version %d", ErrInvalidAttestation, a.Version)
	}
	if a.Index != expectedIndex {
		return fmt.Errorf("%w: attestation index %d, expected %d",
			ErrInvalidAttestation, a.Index, expectedIndex)
	}
	if a.HopID == "" || a.KeyID == "" || a.VerifiedSubject == "" ||
		a.Audience == "" || a.Challenge == "" || a.SigAlg == "" {
		return fmt.Errorf("%w: required field is empty", ErrInvalidAttestation)
	}
	if !a.Assurance.Valid() || !a.UpstreamMin.Valid() {
		return fmt.Errorf("%w: assurance level out of range", ErrInvalidAttestation)
	}
	if a.NotBefore.IsZero() || a.NotAfter.IsZero() || !a.NotAfter.After(a.NotBefore) {
		return fmt.Errorf("%w: invalid validity window", ErrInvalidAttestation)
	}
	if len(a.PrevDigest) != 32 || len(a.Signature) == 0 {
		return fmt.Errorf("%w: prev digest and signature are required", ErrInvalidAttestation)
	}
	return nil
}

func cloneAttestation(a *Attestation) *Attestation {
	cp := *a
	cp.PrevDigest = append([]byte(nil), a.PrevDigest...)
	cp.Signature = append([]byte(nil), a.Signature...)
	return &cp
}

// Encode serializes the chain to deterministic JSON for transport.
func (c *Chain) Encode() ([]byte, error) {
	if c == nil || len(c.attestations) == 0 {
		return nil, ErrEmptyChain
	}
	return json.Marshal(c.attestations)
}

// ParseChain decodes a chain produced by Encode. Parsing only checks shape;
// signatures, links and binding rules are enforced by Verify.
func ParseChain(raw []byte) (*Chain, error) {
	var attestations []*Attestation
	if err := json.Unmarshal(raw, &attestations); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidAttestation, err)
	}
	if len(attestations) == 0 {
		return nil, ErrEmptyChain
	}
	for i, a := range attestations {
		if a == nil {
			return nil, fmt.Errorf("%w: nil attestation at index %d", ErrInvalidAttestation, i)
		}
		if err := validateShape(a, i); err != nil {
			return nil, err
		}
	}
	return &Chain{attestations: attestations}, nil
}
