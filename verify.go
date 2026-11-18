package netsec

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

var (
	// ErrVerificationFailed is the sentinel wrapped by DeniedError.
	ErrVerificationFailed = errors.New("signature verification failed")
)

// RevocationStatus is the result of a revocation lookup for a signature.
type RevocationStatus string

const (
	// RevocationUnknown means revocation could not be determined. Strict mode
	// treats this as a failure; permissive mode allows it.
	RevocationUnknown RevocationStatus = "unknown"
	// RevocationValid means the signature is known not to be revoked.
	RevocationValid RevocationStatus = "valid"
	// RevocationRevoked means the signature has been revoked.
	RevocationRevoked RevocationStatus = "revoked"
)

// Aliases for callers that prefer names matching the type.
const (
	RevocationStatusUnknown = RevocationUnknown
	RevocationStatusValid   = RevocationValid
	RevocationStatusRevoked = RevocationRevoked
	RevocationNotRevoked    = RevocationValid
)

// FindingCode classifies a verification finding. Findings are recorded in
// every mode; the selected verification level decides whether they deny
// admission.
type FindingCode string

const (
	FindingMissingImageDigest        FindingCode = "missing-image-digest"
	FindingInvalidImageDigest        FindingCode = "invalid-image-digest"
	FindingMissingSignature          FindingCode = "missing-signature"
	FindingUnboundSignature          FindingCode = "unbound-signature"
	FindingTagSignature              FindingCode = "tag-signature"
	FindingInvalidSignatureReference FindingCode = "invalid-signature-reference"
	FindingInvalidSignatureDigest    FindingCode = "invalid-signature-digest"
	FindingReferenceMismatch         FindingCode = "signature-reference-mismatch"
	FindingDigestMismatch            FindingCode = "digest-mismatch"
	FindingSignatureExpired          FindingCode = "signature-expired"
	FindingSignatureNotYetValid      FindingCode = "signature-not-yet-valid"
	FindingRevocationUnknown         FindingCode = "revocation-unknown"
	FindingSignatureRevoked          FindingCode = "signature-revoked"
)

// Finding is one recorded verification problem.
type Finding struct {
	Code    FindingCode `json:"code"`
	Message string      `json:"message"`
}

// Signature describes the signature material relevant to admission. Digest is
// the digest bound by the signature. Reference is the signed subject and is
// optional; when present it must contain a digest (a bare digest such as
// "sha256:..." is accepted). A tag-only Reference is never a valid binding.
type Signature struct {
	Digest     string
	Reference  string
	Issuer     string
	Identity   string
	NotBefore  time.Time
	IssuedAt   time.Time
	ExpiresAt  time.Time
	Revocation RevocationStatus
}

// VerificationRequest is the input to Evaluate and Verify. Digest is the
// resolved image digest. It may be omitted when Reference already contains a
// digest, but a tag-only Reference with no Digest can never be verified.
type VerificationRequest struct {
	Reference string
	Digest    string
	Signature *Signature
	Now       time.Time
}

// Decision is the admission result for one image.
type Decision struct {
	Policy      *TrustPolicy      `json:"-"`
	PolicyName  string            `json:"policyName"`
	Scope       string            `json:"scope"`
	Global      bool              `json:"global"`
	Level       VerificationLevel `json:"level"`
	Allowed     bool              `json:"allowed"`
	Verified    bool              `json:"verified"`
	AuditOnly   bool              `json:"auditOnly"`
	ImageDigest string            `json:"imageDigest,omitempty"`
	Findings    []Finding         `json:"findings,omitempty"`
}

// HasFinding reports whether the decision contains a finding with code.
func (d *Decision) HasFinding(code FindingCode) bool {
	if d == nil {
		return false
	}
	for _, finding := range d.Findings {
		if finding.Code == code {
			return true
		}
	}
	return false
}

// DeniedError is returned by Verify when a selected policy denies admission.
type DeniedError struct {
	Decision *Decision
}

func (e *DeniedError) Error() string {
	if e == nil || e.Decision == nil {
		return ErrVerificationFailed.Error()
	}
	var reasons []string
	for _, finding := range e.Decision.Findings {
		reasons = append(reasons, string(finding.Code))
	}
	if len(reasons) == 0 {
		return fmt.Sprintf("%s: policy %q", ErrVerificationFailed, e.Decision.PolicyName)
	}
	return fmt.Sprintf("%s: policy %q: %s", ErrVerificationFailed, e.Decision.PolicyName, strings.Join(reasons, ", "))
}

// Unwrap supports errors.Is(err, ErrVerificationFailed).
func (e *DeniedError) Unwrap() error { return ErrVerificationFailed }

// Evaluate selects a trust policy and verifies the signature. Errors are
// reserved for configuration and input problems such as overlapping scopes,
// no matching policy or a malformed image reference. A verification failure is
// reported in the returned Decision, whose Allowed field is false. Audit mode
// always sets Allowed to true while recording its findings.
func (s *PolicySet) Evaluate(req VerificationRequest) (*Decision, error) {
	if req.Reference == "" {
		return nil, fmt.Errorf("%w: empty reference", ErrInvalidReference)
	}
	image, err := ParseImageReference(req.Reference)
	if err != nil {
		return nil, err
	}
	match, err := s.SelectReference(image)
	if err != nil {
		return nil, err
	}

	decision := &Decision{
		Policy:     match.Policy,
		PolicyName: match.Policy.Name,
		Scope:      match.Scope,
		Global:     match.Global,
		Level:      match.Policy.SignatureVerification.Level,
	}

	now := req.Now
	if now.IsZero() {
		now = time.Now()
	}

	imageDigest := ""
	if image.Digest != "" {
		digest, err := normalizeDigest(image.Digest)
		if err != nil {
			decision.addFinding(FindingInvalidImageDigest, err.Error())
		} else {
			imageDigest = digest
		}
	}
	if req.Digest != "" {
		digest, err := normalizeDigest(req.Digest)
		if err != nil {
			decision.addFinding(FindingInvalidImageDigest, fmt.Sprintf("resolved digest: %v", err))
		} else if imageDigest != "" && digest != imageDigest {
			decision.addFinding(FindingDigestMismatch,
				fmt.Sprintf("resolved digest %s does not match reference digest %s", digest, imageDigest))
		} else {
			imageDigest = digest
		}
	}
	decision.ImageDigest = imageDigest
	if imageDigest == "" {
		decision.addFinding(FindingMissingImageDigest,
			"signature verification requires a resolved image digest, not a tag")
	}

	if req.Signature == nil {
		decision.addFinding(FindingMissingSignature, "no signature supplied")
	} else {
		evaluateSignature(req.Signature, image, imageDigest, now, decision)
	}

	decision.Verified = len(decision.Findings) == 0
	decision.Allowed = allowedAtLevel(decision.Level, decision.Findings)
	decision.AuditOnly = decision.Level == VerificationLevelAudit
	return decision, nil
}

// EvaluateImage is a convenience wrapper around Evaluate.
func (s *PolicySet) EvaluateImage(reference, digest string, signature *Signature, now time.Time) (*Decision, error) {
	return s.Evaluate(VerificationRequest{
		Reference: reference,
		Digest:    digest,
		Signature: signature,
		Now:       now,
	})
}

// Verify behaves like Evaluate but also returns a DeniedError when the
// selected policy denies admission.
func (s *PolicySet) Verify(req VerificationRequest) (*Decision, error) {
	decision, err := s.Evaluate(req)
	if err != nil {
		return nil, err
	}
	if !decision.Allowed {
		return decision, &DeniedError{Decision: decision}
	}
	return decision, nil
}

// VerifyImage is a convenience wrapper around Verify.
func (s *PolicySet) VerifyImage(reference, digest string, signature *Signature, now time.Time) (*Decision, error) {
	return s.Verify(VerificationRequest{
		Reference: reference,
		Digest:    digest,
		Signature: signature,
		Now:       now,
	})
}

func evaluateSignature(sig *Signature, image ImageReference, imageDigest string, now time.Time, decision *Decision) {
	signedDigest := ""

	if sig.Digest != "" {
		digest, err := normalizeDigest(sig.Digest)
		if err != nil {
			decision.addFinding(FindingInvalidSignatureDigest, err.Error())
		} else {
			signedDigest = digest
		}
	}

	if sig.Reference != "" {
		if looksLikeBareDigest(sig.Reference) {
			digest, err := normalizeDigest(sig.Reference)
			if err != nil {
				decision.addFinding(FindingInvalidSignatureReference, err.Error())
			} else if signedDigest != "" && signedDigest != digest {
				decision.addFinding(FindingDigestMismatch,
					fmt.Sprintf("signature digest %s does not match signed reference %s", signedDigest, digest))
			} else {
				signedDigest = digest
			}
		} else {
			subject, err := ParseImageReference(sig.Reference)
			if err != nil {
				decision.addFinding(FindingInvalidSignatureReference, err.Error())
			} else {
				if !strings.EqualFold(subject.Registry, image.Registry) || subject.Repository != image.Repository {
					decision.addFinding(FindingReferenceMismatch,
						fmt.Sprintf("signature subject %q does not identify image %q", subject.Name(), image.Name()))
				}
				if subject.Digest == "" {
					decision.addFinding(FindingTagSignature,
						fmt.Sprintf("signature subject %q is tag-bound; a digest is required", sig.Reference))
				} else {
					digest, err := normalizeDigest(subject.Digest)
					if err != nil {
						decision.addFinding(FindingInvalidSignatureDigest, err.Error())
					} else if signedDigest != "" && signedDigest != digest {
						decision.addFinding(FindingDigestMismatch,
							fmt.Sprintf("signature digest %s does not match signed reference %s", signedDigest, digest))
					} else {
						signedDigest = digest
					}
				}
			}
		}
	}

	if signedDigest == "" {
		decision.addFinding(FindingUnboundSignature,
			"signature does not bind an image digest")
	} else if imageDigest != "" && signedDigest != imageDigest {
		decision.addFinding(FindingDigestMismatch,
			fmt.Sprintf("signature binds %s but image digest is %s", signedDigest, imageDigest))
	}

	if !sig.NotBefore.IsZero() && now.Before(sig.NotBefore) {
		decision.addFinding(FindingSignatureNotYetValid,
			fmt.Sprintf("signature is not valid before %s", sig.NotBefore.Format(time.RFC3339)))
	}
	if !sig.ExpiresAt.IsZero() && !now.Before(sig.ExpiresAt) {
		decision.addFinding(FindingSignatureExpired,
			fmt.Sprintf("signature expired at %s", sig.ExpiresAt.Format(time.RFC3339)))
	}
	if !sig.IssuedAt.IsZero() && !sig.ExpiresAt.IsZero() && !sig.ExpiresAt.After(sig.IssuedAt) {
		decision.addFinding(FindingSignatureExpired,
			"signature expiration is not after its issue time")
	}

	switch normalizeRevocation(sig.Revocation) {
	case RevocationValid:
		// No finding.
	case RevocationRevoked:
		decision.addFinding(FindingSignatureRevoked, "signature has been revoked")
	default:
		decision.addFinding(FindingRevocationUnknown,
			"signature revocation status is unknown")
	}
}

func normalizeRevocation(status RevocationStatus) RevocationStatus {
	switch strings.ToLower(strings.TrimSpace(string(status))) {
	case "":
		return RevocationUnknown
	case "valid", "not-revoked", "notrevoked", "ok":
		return RevocationValid
	case "revoked":
		return RevocationRevoked
	default:
		return RevocationUnknown
	}
}

func looksLikeBareDigest(value string) bool {
	return !strings.ContainsAny(value, "/@") && strings.ContainsRune(value, ':')
}

func allowedAtLevel(level VerificationLevel, findings []Finding) bool {
	switch level {
	case VerificationLevelAudit:
		return true
	case VerificationLevelPermissive:
		for _, finding := range findings {
			if finding.Code == FindingRevocationUnknown {
				continue
			}
			return false
		}
		return true
	default:
		return len(findings) == 0
	}
}

func (d *Decision) addFinding(code FindingCode, message string) {
	d.Findings = append(d.Findings, Finding{Code: code, Message: message})
}
