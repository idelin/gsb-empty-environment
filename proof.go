package netsec

import (
	"encoding/json"
	"fmt"
	"time"
)

// canonicalVersion tags the canonical byte layout so that future, incompatible
// encodings cannot collide with proofs produced by this version.
const canonicalVersion = 1

// Proof is one signed step witness in an authorization chain.
//
// Every proof commits to the operation being authorized (OpDigest), the
// session it belongs to (SessionID), the immediately preceding proof in the
// chain (ParentDigest) and its own validity window (NotBefore/NotAfter).
// The signature covers all of these fields, so two independent success events
// cannot be recombined without breaking either the signature or the parent
// linkage.
type Proof struct {
	StepID       string    `json:"step_id"`
	OpDigest     Digest    `json:"op_digest"`
	SessionID    string    `json:"session_id"`
	ParentDigest Digest    `json:"parent_digest"`
	NotBefore    time.Time `json:"not_before"`
	NotAfter     time.Time `json:"not_after"`
	SignerID     string    `json:"signer_id"`
	Sig          []byte    `json:"sig"`
}

// canonicalBody is the fixed-field view that is actually signed and hashed.
// The Sig field is deliberately excluded: a proof digest identifies the
// unsigned content the signature is made over.
type canonicalBody struct {
	Version      int    `json:"version"`
	StepID       string `json:"step_id"`
	OpDigest     string `json:"op_digest"`
	SessionID    string `json:"session_id"`
	ParentDigest string `json:"parent_digest"`
	NotBefore    int64  `json:"not_before_unix"`
	NotAfter     int64  `json:"not_after_unix"`
	SignerID     string `json:"signer_id"`
}

// canonicalBytes returns the deterministic byte representation of the proof's
// signed content. Times are reduced to UTC Unix seconds so that location and
// monotonic-clock information cannot create two encodings of one intent.
func (p Proof) canonicalBytes() ([]byte, error) {
	body := canonicalBody{
		Version:      canonicalVersion,
		StepID:       p.StepID,
		OpDigest:     p.OpDigest.Hex(),
		SessionID:    p.SessionID,
		ParentDigest: p.ParentDigest.Hex(),
		NotBefore:    p.NotBefore.UTC().Unix(),
		NotAfter:     p.NotAfter.UTC().Unix(),
		SignerID:     p.SignerID,
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("canonical encode proof %q: %w", p.StepID, err)
	}
	return raw, nil
}

// Digest returns the real SHA-256 digest of the proof's signed content.
// It does not depend on the signature bytes.
func (p Proof) Digest() (Digest, error) {
	raw, err := p.canonicalBytes()
	if err != nil {
		return ZeroDigest, err
	}
	return SumDigest(raw), nil
}

// SignedBytes returns the exact bytes a Signer must sign for this proof.
func (p Proof) SignedBytes() ([]byte, error) {
	return p.canonicalBytes()
}
