package threshold

import "math/big"

// ProofOfPossession is the requirement-level name for the tagged Schnorr PoK.
type ProofOfPossession = SchnorrProof

// ComplaintEvidence is the requirement-level name for an independently
// verifiable bad-share complaint.
type ComplaintEvidence = Complaint

// Aggregator is the FROST coordinator/aggregator state machine.
type Aggregator = SigningSession

// NewP256Group returns the real P-256 Group implementation.
func NewP256Group() Group { return P256Group() }

// NewP256 is a concise alias for NewP256Group.
func NewP256() Group { return P256Group() }

// NewAggregator creates a FROST aggregation state machine.
func NewAggregator(g Group, keys *DKGPublicKey, message []byte) (*Aggregator, error) {
	return NewSigningSession(g, keys, message)
}

// Rho computes the FROST binding factor rho_i for the canonical commitment
// list B. It is an alias for BindingFactor.
func Rho(g Group, indices map[ID]int, participant ID, message []byte, commitments []*NonceCommitment) (*big.Int, error) {
	return BindingFactor(g, indices, participant, message, commitments)
}

// VerifyPartial is a short alias for VerifyPartialSignature.
func VerifyPartial(g Group, keys *DKGPublicKey, pkg *SigningPackage, partial *PartialSignature) error {
	return VerifyPartialSignature(g, keys, pkg, partial)
}
