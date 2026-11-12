package trustpolicy

import "fmt"

// Signature describes the verified facts about an image signature. The
// caller is responsible for cryptographic verification; this package
// enforces what the trust policy does with the outcome.
type Signature struct {
	// SignedDigest is the image digest the signature was made over. Empty
	// when the signature was bound to a tag instead of a digest.
	SignedDigest string
	// SignedTag is non-empty when the signature was bound to a mutable tag.
	// Tag-bound signatures never satisfy admission: tags can be re-pointed,
	// so a signature must bind to the immutable image digest.
	SignedTag string
	// Expired reports whether the signing certificate was expired at
	// verification time.
	Expired bool
	// RevocationUnknown reports whether the revocation status of the
	// signing certificate could not be determined.
	RevocationUnknown bool
}

// Decision is the outcome of evaluating a signature against a policy level.
type Decision struct {
	// Allowed reports whether the image may be admitted.
	Allowed bool
	// Findings lists every problem detected, for audit logging. It is
	// populated even when Allowed is true (audit and permissive levels).
	Findings []string
}

// Evaluate applies level to a signature presented for imageDigest.
//
// A signature that is not bound to imageDigest — whether tag-bound or bound
// to a different digest — is a finding at every level. Strict fails on any
// finding. Permissive tolerates only unknown revocation status. Audit
// allows everything and merely records findings.
func Evaluate(level Level, sig Signature, imageDigest string) Decision {
	var findings []string
	switch {
	case sig.SignedTag != "":
		findings = append(findings, fmt.Sprintf("signature bound to mutable tag %q instead of image digest", sig.SignedTag))
	case sig.SignedDigest != imageDigest:
		findings = append(findings, fmt.Sprintf("signature digest %q does not match image digest %q", sig.SignedDigest, imageDigest))
	}
	if sig.Expired {
		findings = append(findings, "signing certificate expired")
	}
	if sig.RevocationUnknown {
		findings = append(findings, "revocation status unknown")
	}

	allowed := false
	switch level {
	case LevelAudit:
		allowed = true
	case LevelStrict:
		allowed = len(findings) == 0
	case LevelPermissive:
		// Only unknown revocation is tolerated; it remains recorded.
		allowed = true
		for _, f := range findings {
			if f != "revocation status unknown" {
				allowed = false
				break
			}
		}
	}
	return Decision{Allowed: allowed, Findings: findings}
}
