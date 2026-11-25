package workloadid

import "errors"

// Attestation errors.
var (
	ErrAttestationTypeMismatch = errors.New("workloadid: node attestation type does not match policy")
	ErrAttestationExpired      = errors.New("workloadid: node attestation claim is expired")
	ErrAttestationReplayed     = errors.New("workloadid: node attestation nonce is stale or replayed")
	ErrInvalidAttestation      = errors.New("workloadid: invalid attestation configuration")
)

// NodeAttestation is the policy-side description of an acceptable node
// attestation. Identity is only granted when the cryptographic chain and the
// node evidence agree, so a stolen test certificate cannot be replayed on an
// unrelated node.
type NodeAttestation struct {
	// Type is the attestation mechanism, for example "k8s-sa" or "aws-ec2".
	Type string
	// AllowedPrincipals lists the acceptable attestation principals (e.g. node
	// or service-account names). At least one is required.
	AllowedPrincipals []string
	// MaxAge bounds the age of an accepted claim to defeat replay of stale
	// evidence. Zero means the verifier default is used.
	MaxAge Duration
}

func (a *NodeAttestation) validate() error {
	if a == nil || a.Type == "" || len(a.AllowedPrincipals) == 0 {
		return ErrInvalidAttestation
	}
	for _, principal := range a.AllowedPrincipals {
		if principal == "" {
			return ErrInvalidAttestation
		}
	}
	return nil
}

func (a *NodeAttestation) matches(claim *NodeAttestationClaim) error {
	if claim.Type != a.Type {
		return ErrAttestationTypeMismatch
	}
	if claim.IssuedAt.IsZero() {
		return ErrAttestationReplayed
	}
	principalOK := false
	for _, allowed := range a.AllowedPrincipals {
		if claim.Principal == allowed {
			principalOK = true
			break
		}
	}
	if !principalOK {
		return ErrAttestationDenied
	}
	return nil
}

// NodeAttestationClaim is the evidence produced by the node agent.
type NodeAttestationClaim struct {
	// Type mirrors NodeAttestation.Type.
	Type string
	// Principal is the attested node/workload principal.
	Principal string
	// IssuedAt is when the evidence was generated.
	IssuedAt Time
	// ExpiresAt optionally bounds claim validity.
	ExpiresAt Time
	// Nonce ties the claim to the current handshake to prevent replay.
	Nonce string
}

// validateFreshness enforces expiry, maximum age and nonce presence.
func (c *NodeAttestationClaim) validateFreshness(now Time, maxAge Duration, seenNonces NonceCache) error {
	if c == nil {
		return ErrAttestationDenied
	}
	if c.Nonce == "" {
		return ErrAttestationReplayed
	}
	if !c.ExpiresAt.IsZero() && !now.Before(c.ExpiresAt) {
		return ErrAttestationExpired
	}
	if maxAge > 0 && now.Sub(c.IssuedAt) > maxAge {
		return ErrAttestationExpired
	}
	if seenNonces != nil {
		if seenNonces.Seen(c.Nonce) {
			return ErrAttestationReplayed
		}
		seenNonces.Remember(c.Nonce, now)
	}
	return nil
}

// NonceCache remembers consumed attestation nonces. Implementations must be
// safe for concurrent use.
type NonceCache interface {
	Seen(nonce string) bool
	Remember(nonce string, at Time)
}
