package netsec

import (
	"errors"
	"testing"
	"time"
)

var testNow = time.Date(2026, time.November, 19, 12, 0, 0, 0, time.UTC)

func verificationSet(t *testing.T, level VerificationLevel) *PolicySet {
	t.Helper()
	return mustPolicySet(t, policy("prod", level, "example.com/prod/*"))
}

func validSignature(digest string) *Signature {
	return &Signature{
		Digest:     digest,
		IssuedAt:   testNow.Add(-time.Hour),
		ExpiresAt:  testNow.Add(time.Hour),
		Revocation: RevocationValid,
	}
}

func evaluate(t *testing.T, set *PolicySet, reference, digest string, signature *Signature) *Decision {
	t.Helper()
	decision, err := set.EvaluateImage(reference, digest, signature, testNow)
	if err != nil {
		t.Fatalf("EvaluateImage: %v", err)
	}
	return decision
}

func TestStrictAcceptsValidDigestBoundSignature(t *testing.T) {
	set := verificationSet(t, VerificationLevelStrict)
	signature := validSignature(testDigestA)

	decision := evaluate(t, set, "example.com/prod/app:v1", testDigestA, signature)
	if !decision.Allowed || !decision.Verified {
		t.Fatalf("decision = %+v, want allowed and verified", decision)
	}
	if decision.Level != VerificationLevelStrict || decision.AuditOnly {
		t.Fatalf("decision level = %q auditOnly=%v", decision.Level, decision.AuditOnly)
	}
	if decision.ImageDigest != testDigestA {
		t.Fatalf("image digest = %q, want %q", decision.ImageDigest, testDigestA)
	}

	if _, err := set.VerifyImage("example.com/prod/app:v1", testDigestA, signature, testNow); err != nil {
		t.Fatalf("VerifyImage: %v", err)
	}
}

func TestStrictRejectsExpiredSignature(t *testing.T) {
	set := verificationSet(t, VerificationLevelStrict)
	signature := validSignature(testDigestA)
	signature.ExpiresAt = testNow.Add(-time.Second)

	decision := evaluate(t, set, "example.com/prod/app:v1", testDigestA, signature)
	if decision.Allowed {
		t.Fatalf("decision = %+v, want denied", decision)
	}
	if !decision.HasFinding(FindingSignatureExpired) {
		t.Fatalf("findings = %+v, want %s", decision.Findings, FindingSignatureExpired)
	}
	if _, err := set.VerifyImage("example.com/prod/app:v1", testDigestA, signature, testNow); !errors.Is(err, ErrVerificationFailed) {
		t.Fatalf("VerifyImage error = %v, want ErrVerificationFailed", err)
	}
}

func TestExpirationBoundaryIsExclusive(t *testing.T) {
	set := verificationSet(t, VerificationLevelStrict)
	signature := validSignature(testDigestA)
	signature.ExpiresAt = testNow

	decision := evaluate(t, set, "example.com/prod/app:v1", testDigestA, signature)
	if decision.Allowed || !decision.HasFinding(FindingSignatureExpired) {
		t.Fatalf("decision = %+v, want denied at exact expiration", decision)
	}
}

func TestNotYetValidSignatureIsRejected(t *testing.T) {
	set := verificationSet(t, VerificationLevelStrict)
	signature := validSignature(testDigestA)
	signature.NotBefore = testNow.Add(time.Minute)

	decision := evaluate(t, set, "example.com/prod/app:v1", testDigestA, signature)
	if decision.Allowed || !decision.HasFinding(FindingSignatureNotYetValid) {
		t.Fatalf("decision = %+v, want denied", decision)
	}
}

func TestStrictRejectsUnknownRevocation(t *testing.T) {
	set := verificationSet(t, VerificationLevelStrict)
	signature := validSignature(testDigestA)
	signature.Revocation = RevocationUnknown

	decision := evaluate(t, set, "example.com/prod/app:v1", testDigestA, signature)
	if decision.Allowed {
		t.Fatalf("decision = %+v, want denied", decision)
	}
	if !decision.HasFinding(FindingRevocationUnknown) {
		t.Fatalf("findings = %+v, want %s", decision.Findings, FindingRevocationUnknown)
	}
}

func TestRevokedSignatureIsAlwaysRejected(t *testing.T) {
	for _, level := range []VerificationLevel{VerificationLevelStrict, VerificationLevelPermissive} {
		t.Run(string(level), func(t *testing.T) {
			set := verificationSet(t, level)
			signature := validSignature(testDigestA)
			signature.Revocation = RevocationRevoked

			decision := evaluate(t, set, "example.com/prod/app:v1", testDigestA, signature)
			if decision.Allowed || !decision.HasFinding(FindingSignatureRevoked) {
				t.Fatalf("decision = %+v, want denied", decision)
			}
		})
	}
}

func TestPermissiveAllowsOnlyUnknownRevocation(t *testing.T) {
	set := verificationSet(t, VerificationLevelPermissive)

	unknown := validSignature(testDigestA)
	unknown.Revocation = RevocationUnknown
	decision := evaluate(t, set, "example.com/prod/app:v1", testDigestA, unknown)
	if !decision.Allowed {
		t.Fatalf("unknown revocation decision = %+v, want allowed", decision)
	}
	if decision.Verified {
		t.Fatalf("decision Verified = true, want false while revocation is unknown")
	}
	if !decision.HasFinding(FindingRevocationUnknown) {
		t.Fatalf("findings = %+v, want unknown revocation recorded", decision.Findings)
	}

	expired := validSignature(testDigestA)
	expired.Revocation = RevocationUnknown
	expired.ExpiresAt = testNow.Add(-time.Minute)
	decision = evaluate(t, set, "example.com/prod/app:v1", testDigestA, expired)
	if decision.Allowed {
		t.Fatalf("expired decision = %+v, want denied", decision)
	}
	if !decision.HasFinding(FindingSignatureExpired) {
		t.Fatalf("findings = %+v, want expiry recorded", decision.Findings)
	}
}

func TestAuditRecordsButDoesNotDeny(t *testing.T) {
	set := verificationSet(t, VerificationLevelAudit)
	signature := validSignature(testDigestA)
	signature.ExpiresAt = testNow.Add(-time.Minute)
	signature.Revocation = RevocationRevoked

	decision := evaluate(t, set, "example.com/prod/app:v1", testDigestA, signature)
	if !decision.Allowed {
		t.Fatalf("decision = %+v, want allowed in audit mode", decision)
	}
	if !decision.AuditOnly {
		t.Fatalf("decision AuditOnly = false, want true")
	}
	if decision.Verified {
		t.Fatalf("decision Verified = true, want false")
	}
	if !decision.HasFinding(FindingSignatureExpired) || !decision.HasFinding(FindingSignatureRevoked) {
		t.Fatalf("findings = %+v, want expiry and revocation findings", decision.Findings)
	}
	if _, err := set.VerifyImage("example.com/prod/app:v1", testDigestA, signature, testNow); err != nil {
		t.Fatalf("VerifyImage in audit mode = %v, want nil", err)
	}
}

func TestAuditRecordsMissingSignature(t *testing.T) {
	set := verificationSet(t, VerificationLevelAudit)
	decision := evaluate(t, set, "example.com/prod/app:v1", testDigestA, nil)
	if !decision.Allowed {
		t.Fatalf("decision = %+v, want allowed in audit mode", decision)
	}
	if !decision.HasFinding(FindingMissingSignature) {
		t.Fatalf("findings = %+v, want %s", decision.Findings, FindingMissingSignature)
	}
}

func TestTagBoundSignatureIsRejected(t *testing.T) {
	set := verificationSet(t, VerificationLevelStrict)

	t.Run("tag reference only", func(t *testing.T) {
		signature := validSignature("")
		signature.Reference = "example.com/prod/app:latest"
		decision := evaluate(t, set, "example.com/prod/app:latest", testDigestA, signature)
		if decision.Allowed {
			t.Fatalf("decision = %+v, want denied", decision)
		}
		if !decision.HasFinding(FindingTagSignature) {
			t.Fatalf("findings = %+v, want %s", decision.Findings, FindingTagSignature)
		}
	})

	t.Run("digest plus tag reference still lacks digest in subject", func(t *testing.T) {
		signature := validSignature(testDigestA)
		signature.Reference = "example.com/prod/app:latest"
		decision := evaluate(t, set, "example.com/prod/app:latest", testDigestA, signature)
		if decision.Allowed {
			t.Fatalf("decision = %+v, want denied", decision)
		}
		if !decision.HasFinding(FindingTagSignature) {
			t.Fatalf("findings = %+v, want %s", decision.Findings, FindingTagSignature)
		}
	})
}

func TestTagReferenceWithoutResolvedDigestIsRejected(t *testing.T) {
	set := verificationSet(t, VerificationLevelStrict)
	signature := validSignature(testDigestA)

	decision := evaluate(t, set, "example.com/prod/app:latest", "", signature)
	if decision.Allowed {
		t.Fatalf("decision = %+v, want denied", decision)
	}
	if !decision.HasFinding(FindingMissingImageDigest) {
		t.Fatalf("findings = %+v, want %s", decision.Findings, FindingMissingImageDigest)
	}
}

func TestSignatureDigestMustMatchImageDigest(t *testing.T) {
	set := verificationSet(t, VerificationLevelStrict)
	signature := validSignature(testDigestB)

	decision := evaluate(t, set, "example.com/prod/app:v1", testDigestA, signature)
	if decision.Allowed {
		t.Fatalf("decision = %+v, want denied", decision)
	}
	if !decision.HasFinding(FindingDigestMismatch) {
		t.Fatalf("findings = %+v, want %s", decision.Findings, FindingDigestMismatch)
	}
}

func TestResolvedDigestMustMatchReferenceDigest(t *testing.T) {
	set := verificationSet(t, VerificationLevelStrict)
	signature := validSignature(testDigestA)

	decision := evaluate(t, set, "example.com/prod/app@"+testDigestA, testDigestB, signature)
	if decision.Allowed {
		t.Fatalf("decision = %+v, want denied", decision)
	}
	if !decision.HasFinding(FindingDigestMismatch) {
		t.Fatalf("findings = %+v, want %s", decision.Findings, FindingDigestMismatch)
	}
}

func TestSignedDigestReferenceIsAccepted(t *testing.T) {
	set := verificationSet(t, VerificationLevelStrict)

	full := validSignature("")
	full.Reference = "example.com/prod/app@" + testDigestA
	decision := evaluate(t, set, "example.com/prod/app:v1", testDigestA, full)
	if !decision.Allowed {
		t.Fatalf("full digest reference decision = %+v, want allowed", decision)
	}

	bare := validSignature("")
	bare.Reference = testDigestA
	decision = evaluate(t, set, "example.com/prod/app:v1", testDigestA, bare)
	if !decision.Allowed {
		t.Fatalf("bare digest reference decision = %+v, want allowed", decision)
	}
}

func TestSignatureForDifferentRepositoryIsRejected(t *testing.T) {
	set := verificationSet(t, VerificationLevelStrict)
	signature := validSignature("")
	signature.Reference = "example.com/prod/other@" + testDigestA

	decision := evaluate(t, set, "example.com/prod/app:v1", testDigestA, signature)
	if decision.Allowed {
		t.Fatalf("decision = %+v, want denied", decision)
	}
	if !decision.HasFinding(FindingReferenceMismatch) {
		t.Fatalf("findings = %+v, want %s", decision.Findings, FindingReferenceMismatch)
	}
}

func TestUnboundSignatureIsRejected(t *testing.T) {
	set := verificationSet(t, VerificationLevelStrict)
	signature := validSignature("")

	decision := evaluate(t, set, "example.com/prod/app:v1", testDigestA, signature)
	if decision.Allowed {
		t.Fatalf("decision = %+v, want denied", decision)
	}
	if !decision.HasFinding(FindingUnboundSignature) {
		t.Fatalf("findings = %+v, want %s", decision.Findings, FindingUnboundSignature)
	}
}

func TestOverlapStillFailsInAuditMode(t *testing.T) {
	set := mustPolicySet(t,
		policy("broad", VerificationLevelAudit, "example.com/prod/*"),
		policy("exact", VerificationLevelAudit, "example.com/prod/app"),
	)
	_, err := set.EvaluateImage("example.com/prod/app:v1", testDigestA, validSignature(testDigestA), testNow)
	if !errors.Is(err, ErrOverlappingScopes) {
		t.Fatalf("EvaluateImage error = %v, want ErrOverlappingScopes", err)
	}
}

func TestSpecificStrictOverridesGlobalAudit(t *testing.T) {
	set := mustPolicySet(t,
		policy("global", VerificationLevelAudit, "*"),
		policy("prod", VerificationLevelStrict, "example.com/prod/*"),
	)
	signature := validSignature(testDigestA)
	signature.ExpiresAt = testNow.Add(-time.Minute)

	decision := evaluate(t, set, "example.com/prod/app:v1", testDigestA, signature)
	if decision.Allowed {
		t.Fatalf("decision = %+v, want strict global-overriding policy to deny", decision)
	}
	if decision.PolicyName != "prod" || decision.Level != VerificationLevelStrict {
		t.Fatalf("decision policy = %q level = %q", decision.PolicyName, decision.Level)
	}
}

func TestDigestComparisonIsCanonical(t *testing.T) {
	set := verificationSet(t, VerificationLevelStrict)
	upper := "sha256:" + "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"
	signature := validSignature(upper)

	decision := evaluate(t, set, "example.com/prod/app:v1", testDigestA, signature)
	if !decision.Allowed {
		t.Fatalf("decision = %+v, want allowed after digest normalization", decision)
	}
}
