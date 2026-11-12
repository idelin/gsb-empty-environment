package taskpkg

// Policy is the host-wide admission baseline. It is independent of individual
// edge constraints: an edge may be misconfigured to admit something the host
// would never authorize. The audit pass uses Authorized to surface exactly
// those candidates.
type Policy struct {
	// TrustedSources is the set of source identities the host recognizes.
	TrustedSources map[SourceID]bool
	// TrustedSigners is the set of signers the host accepts regardless of
	// which edge requested them.
	TrustedSigners map[SignerID]bool
}

// NewPolicy builds a Policy from source and signer allow lists.
func NewPolicy(sources []SourceID, signers []SignerID) Policy {
	p := Policy{
		TrustedSources: make(map[SourceID]bool, len(sources)),
		TrustedSigners: make(map[SignerID]bool, len(signers)),
	}
	for _, s := range sources {
		p.TrustedSources[s] = true
	}
	for _, s := range signers {
		p.TrustedSigners[s] = true
	}
	return p
}

// RejectReason explains why a candidate is not host-authorized.
type RejectReason string

const (
	ReasonUntrustedSource RejectReason = "untrusted-source"
	ReasonUntrustedSigner RejectReason = "untrusted-signer"
)

// Authorized reports whether the host baseline admits the candidate. It does
// not replace per-edge checks; proof verification is also separate.
func (p Policy) Authorized(c Candidate) (bool, RejectReason) {
	if !p.TrustedSources[c.Node.Source] {
		return false, ReasonUntrustedSource
	}
	if !p.TrustedSigners[c.Proof.Signer] {
		return false, ReasonUntrustedSigner
	}
	return true, ""
}
