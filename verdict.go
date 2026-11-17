package netsec

import (
	"crypto/sha256"
	"encoding/hex"
)

// Verdict is the boolean result of a rule or composite expression.
type Verdict uint8

const (
	// VerdictInvalid is the zero value. It is not a valid check result and is
	// used for aborted evaluations.
	VerdictInvalid Verdict = iota
	// VerdictDeny denies the request.
	VerdictDeny
	// VerdictAllow allows the request.
	VerdictAllow
)

// Valid reports whether v is a boolean verdict.
func (v Verdict) Valid() bool {
	return v == VerdictDeny || v == VerdictAllow
}

func (v Verdict) String() string {
	switch v {
	case VerdictDeny:
		return "deny"
	case VerdictAllow:
		return "allow"
	default:
		return "invalid"
	}
}

// Digest is a SHA-256 digest. Checks use it to bind the evidence that produced
// a verdict without placing sensitive evidence in the audit trace.
type Digest [sha256.Size]byte

// IsZero reports whether d is the all-zero digest.
func (d Digest) IsZero() bool {
	return d == Digest{}
}

// String returns the lower-case hexadecimal encoding of d.
func (d Digest) String() string {
	return hex.EncodeToString(d[:])
}

// DigestOf returns the SHA-256 digest of payload.
func DigestOf(payload []byte) Digest {
	return Digest(sha256.Sum256(payload))
}

// Evidence is the audit-visible summary of what a check observed.
//
// Digest must be non-zero for a successful check. Summary is optional
// human-readable context; it is sealed into the trace but must not contain
// secrets that are not suitable for the audit log.
type Evidence struct {
	Digest  Digest
	Summary string
}

// NewEvidence hashes payload and attaches a human-readable summary.
func NewEvidence(payload []byte, summary string) Evidence {
	return Evidence{Digest: DigestOf(payload), Summary: summary}
}

// CheckResult is the value returned by a rule check.
type CheckResult struct {
	Verdict  Verdict
	Evidence Evidence
}
