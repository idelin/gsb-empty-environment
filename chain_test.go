package netsec

import (
	"crypto/x509"
	"encoding/json"
	"errors"
	"testing"
	"time"
)

func TestOldEvidenceContentSwapRejected(t *testing.T) {
	captured := utc(2026, time.June, 1, 9, 0)
	ca := newTestCA(t, "ecdsa", utc(2026, time.January, 1, 0, 0), utc(2028, time.January, 1, 0, 0))
	issuer := ca.issue(t, "ecdsa", utc(2026, time.January, 1, 0, 0), utc(2028, time.January, 1, 0, 0))

	evidence := []byte("original forensic image bytes")
	chain, err := NewChainAt(evidence, captured, captured.Add(time.Hour), issuer.issuer())
	if err != nil {
		t.Fatalf("seal evidence: %v", err)
	}
	chain, err = chain.Append(captured.Add(2*time.Hour), issuer.issuer())
	if err != nil {
		t.Fatalf("append second generation: %v", err)
	}
	if err := chain.Verify(evidence); err != nil {
		t.Fatalf("verify intact chain: %v", err)
	}

	swapped := []byte("attacker replaced the evidence bytes")
	err = chain.Verify(swapped)
	if err == nil {
		t.Fatal("verification accepted swapped evidence")
	}
	if !errors.Is(err, ErrEvidenceMismatch) {
		t.Fatalf("want ErrEvidenceMismatch, got %v", err)
	}
	var verificationErr *VerificationError
	if !errors.As(err, &verificationErr) {
		t.Fatalf("want *VerificationError, got %T", err)
	}
	if len(verificationErr.Violations) == 0 {
		t.Fatal("verification error carried no violations")
	}
	if verificationErr.Violations[0].Generation != -1 {
		t.Fatalf("evidence mismatch should be a header violation, got generation %d", verificationErr.Violations[0].Generation)
	}

	if got := chain.EvidenceDigest(); got != DigestOf(evidence) {
		t.Fatalf("evidence digest was overwritten: %s", got)
	}
	if got := chain.CapturedAt(); !got.Equal(captured) {
		t.Fatalf("capture time was overwritten: %s", got)
	}
	if got := chain.Len(); got != 2 {
		t.Fatalf("failed verification truncated chain: got %d generations", got)
	}
	if err := chain.Verify(evidence); err != nil {
		t.Fatalf("failed verification mutated the chain: %v", err)
	}
}

func TestResealKeepsOriginalCaptureTime(t *testing.T) {
	captured := utc(2020, time.March, 3, 8, 30)
	ca := newTestCA(t, "ecdsa", utc(2019, time.January, 1, 0, 0), utc(2030, time.January, 1, 0, 0))
	issuer := ca.issue(t, "ecdsa", utc(2019, time.January, 1, 0, 0), utc(2030, time.January, 1, 0, 0))

	evidence := []byte("evidence collected in 2020")
	firstSeal := utc(2026, time.June, 1, 12, 0)
	chain, err := NewChainAt(evidence, captured, firstSeal, issuer.issuer())
	if err != nil {
		t.Fatalf("seal old evidence: %v", err)
	}
	secondSeal := utc(2027, time.June, 1, 12, 0)
	chain, err = chain.Reseal(secondSeal, issuer.issuer())
	if err != nil {
		t.Fatalf("resign old evidence: %v", err)
	}

	if !chain.CapturedAt().Equal(captured) {
		t.Fatalf("capture time changed to %s", chain.CapturedAt())
	}
	g1, _ := chain.Generation(0)
	g2, _ := chain.Generation(1)
	if !g1.IssuedAt.Equal(firstSeal) || !g2.IssuedAt.Equal(secondSeal) {
		t.Fatalf("issuance times were not preserved: %s, %s", g1.IssuedAt, g2.IssuedAt)
	}
	if err := chain.Verify(evidence, VerifyOptions{ExpectedCapturedAt: &captured}); err != nil {
		t.Fatalf("verify historical chain: %v", err)
	}

	before, _ := chain.TipDigest()
	_, err = chain.Append(firstSeal.Add(-time.Hour), issuer.issuer())
	if !errors.Is(err, ErrTimeRegression) {
		t.Fatalf("want ErrTimeRegression for backward reseal, got %v", err)
	}
	if chain.Len() != 2 {
		t.Fatalf("failed append changed chain length to %d", chain.Len())
	}
	after, _ := chain.TipDigest()
	if before != after {
		t.Fatal("failed append changed chain tip")
	}

	tooEarly := captured.Add(-365 * 24 * time.Hour)
	_, err = chain.Append(tooEarly, issuer.issuer())
	if !errors.Is(err, ErrTimeBeforeCapture) {
		t.Fatalf("want ErrTimeBeforeCapture, got %v", err)
	}

	wrongCapture := captured.Add(time.Hour)
	err = chain.Verify(evidence, VerifyOptions{ExpectedCapturedAt: &wrongCapture})
	if !errors.Is(err, ErrCaptureTimeMismatch) {
		t.Fatalf("want ErrCaptureTimeMismatch, got %v", err)
	}
}

func TestSameTimeForkRejected(t *testing.T) {
	captured := utc(2026, time.June, 1, 9, 0)
	ca := newTestCA(t, "ecdsa", utc(2026, time.January, 1, 0, 0), utc(2028, time.January, 1, 0, 0))
	issuer := ca.issue(t, "ecdsa", utc(2026, time.January, 1, 0, 0), utc(2028, time.January, 1, 0, 0))
	evidence := []byte("evidence with a disputed timestamp")

	chain, err := NewChainAt(evidence, captured, captured.Add(time.Hour), issuer.issuer())
	if err != nil {
		t.Fatalf("seal evidence: %v", err)
	}
	secondAt := captured.Add(2 * time.Hour)
	chain, err = chain.Append(secondAt, issuer.issuer())
	if err != nil {
		t.Fatalf("append second generation: %v", err)
	}
	parent, err := chain.GenerationDigest(0)
	if err != nil {
		t.Fatalf("digest first generation: %v", err)
	}

	// A second signer signs the same parent at the exact same time. Both
	// generations are individually valid, but together they form a fork.
	sibling, err := SignGeneration(chain.Header(), parent, secondAt, issuer.issuer())
	if err != nil {
		t.Fatalf("sign forked sibling: %v", err)
	}
	forked := append(chain.Generations(), sibling)
	err = VerifyGenerations(evidence, captured, forked)
	if err == nil {
		t.Fatal("verification accepted a same-time fork")
	}
	for _, target := range []error{ErrFork, ErrSameTimeFork, ErrParentMismatch} {
		if !errors.Is(err, target) {
			t.Fatalf("fork error does not contain %v: %v", target, err)
		}
	}
	var verificationErr *VerificationError
	if !errors.As(err, &verificationErr) {
		t.Fatalf("want *VerificationError, got %T", err)
	}
	if len(forked) != 3 || len(verificationErr.Violations) == 0 {
		t.Fatalf("full fork history was not retained: %d generations, %d violations", len(forked), len(verificationErr.Violations))
	}

	// A fork at a different time is still a fork, but not a same-time fork.
	laterSibling, err := SignGeneration(chain.Header(), parent, secondAt.Add(time.Hour), issuer.issuer())
	if err != nil {
		t.Fatalf("sign later forked sibling: %v", err)
	}
	laterFork := append(chain.Generations(), laterSibling)
	err = VerifyGenerations(evidence, captured, laterFork)
	if !errors.Is(err, ErrFork) {
		t.Fatalf("want ErrFork for later sibling, got %v", err)
	}
	if errors.Is(err, ErrSameTimeFork) {
		t.Fatalf("later sibling was misclassified as same-time fork: %v", err)
	}
}

func TestExpiredCertificateResealRejected(t *testing.T) {
	captured := utc(2026, time.June, 1, 9, 0)
	ca := newTestCA(t, "ecdsa", utc(2019, time.January, 1, 0, 0), utc(2030, time.January, 1, 0, 0))
	current := ca.issue(t, "ecdsa", utc(2026, time.January, 1, 0, 0), utc(2028, time.January, 1, 0, 0))
	expired := ca.issue(t, "ecdsa", utc(2025, time.January, 1, 0, 0), utc(2025, time.December, 31, 0, 0))
	evidence := []byte("old evidence whose issuer later expired")

	chain, err := NewChainAt(evidence, captured, captured.Add(time.Hour), current.issuer())
	if err != nil {
		t.Fatalf("seal evidence: %v", err)
	}
	tipBefore, _ := chain.TipDigest()

	_, err = chain.Append(captured.Add(2*time.Hour), expired.issuer())
	if !errors.Is(err, ErrIssuerExpired) {
		t.Fatalf("want ErrIssuerExpired for re-sign, got %v", err)
	}
	if chain.Len() != 1 {
		t.Fatalf("expired re-sign appended a generation: length %d", chain.Len())
	}
	tipAfter, _ := chain.TipDigest()
	if tipBefore != tipAfter {
		t.Fatal("expired re-sign changed chain tip")
	}
	if err := chain.Verify(evidence); err != nil {
		t.Fatalf("failed re-sign mutated the chain: %v", err)
	}

	_, err = NewChainAt(evidence, captured, captured.Add(2*time.Hour), expired.issuer())
	if !errors.Is(err, ErrIssuerExpired) {
		t.Fatalf("want ErrIssuerExpired for first seal, got %v", err)
	}

	notYetValid := ca.issue(t, "ecdsa", utc(2026, time.July, 1, 0, 0), utc(2028, time.January, 1, 0, 0))
	_, err = chain.Append(captured.Add(2*time.Hour), notYetValid.issuer())
	if !errors.Is(err, ErrIssuerNotYetValid) {
		t.Fatalf("want ErrIssuerNotYetValid, got %v", err)
	}
}

func TestIssuerValidityUsesIssuanceTimeNotVerificationTime(t *testing.T) {
	captured := utc(2026, time.June, 1, 9, 0)
	ca := newTestCA(t, "ecdsa", utc(2020, time.January, 1, 0, 0), utc(2030, time.January, 1, 0, 0))
	// The issuer is valid only around the issuance dates. Verification happens
	// later and must still use each generation's historical issuance time.
	issuer := ca.issue(t, "ecdsa", utc(2026, time.January, 1, 0, 0), utc(2026, time.June, 2, 23, 59))
	evidence := []byte("historical evidence")

	chain, err := NewChainAt(evidence, captured, captured.Add(2*time.Hour), issuer.issuer())
	if err != nil {
		t.Fatalf("seal evidence: %v", err)
	}
	chain, err = chain.Append(utc(2026, time.June, 2, 23, 0), issuer.issuer())
	if err != nil {
		t.Fatalf("append within certificate window: %v", err)
	}
	if err := chain.Verify(evidence); err != nil {
		t.Fatalf("issuer validity was evaluated at verification time instead of issuance time: %v", err)
	}
}

func TestVerificationRetainsCompleteFailureChain(t *testing.T) {
	captured := utc(2026, time.June, 1, 9, 0)
	ca := newTestCA(t, "ecdsa", utc(2026, time.January, 1, 0, 0), utc(2028, time.January, 1, 0, 0))
	issuer := ca.issue(t, "ecdsa", utc(2026, time.January, 1, 0, 0), utc(2028, time.January, 1, 0, 0))
	evidence := []byte("multi-generation forensic evidence")

	chain, err := NewChainAt(evidence, captured, captured.Add(time.Hour), issuer.issuer())
	if err != nil {
		t.Fatalf("seal evidence: %v", err)
	}
	for i := 1; i <= 3; i++ {
		chain, err = chain.Append(captured.Add(time.Duration(i+1)*time.Hour), issuer.issuer())
		if err != nil {
			t.Fatalf("append generation %d: %v", i, err)
		}
	}
	if err := chain.Verify(evidence); err != nil {
		t.Fatalf("verify intact chain: %v", err)
	}

	brokenGenerations := chain.Generations()
	brokenGenerations[1].Signature[0] ^= 0xff
	brokenGenerations[2].ParentDigest[0] ^= 0xff
	brokenGenerations[3].IssuedAt = brokenGenerations[2].IssuedAt.Add(-time.Hour)
	broken := &Chain{header: chain.Header(), generations: brokenGenerations}

	err = broken.Verify([]byte("swapped evidence"))
	if err == nil {
		t.Fatal("verification accepted a chain with multiple defects")
	}
	var verificationErr *VerificationError
	if !errors.As(err, &verificationErr) {
		t.Fatalf("want *VerificationError, got %T", err)
	}
	if len(verificationErr.Violations) < 4 {
		t.Fatalf("verification stopped early: only %d violations for a 4-generation broken chain: %v",
			len(verificationErr.Violations), err)
	}
	for _, target := range []error{ErrEvidenceMismatch, ErrIssuerSignature, ErrParentMismatch, ErrTimeRegression} {
		if !errors.Is(err, target) {
			t.Fatalf("complete failure chain is missing %v: %v", target, err)
		}
	}
	if got := broken.Generations(); len(got) != 4 {
		t.Fatalf("failed verification discarded part of the chain: %d generations", len(got))
	}
	if err := chain.Verify(evidence); err != nil {
		t.Fatalf("failed verification mutated the intact source chain: %v", err)
	}
}

func TestTrustedIssuerAndJSONRoundTrip(t *testing.T) {
	captured := utc(2026, time.June, 1, 9, 0)
	ca := newTestCA(t, "ecdsa", utc(2020, time.January, 1, 0, 0), utc(2030, time.January, 1, 0, 0))
	issuer := ca.issue(t, "ecdsa", utc(2026, time.January, 1, 0, 0), utc(2028, time.January, 1, 0, 0))
	evidence := []byte("evidence for trusted timestamp authorities")

	chain, err := NewChainAt(evidence, captured, captured.Add(time.Hour), issuer.issuer())
	if err != nil {
		t.Fatalf("seal evidence: %v", err)
	}
	chain, err = chain.Append(captured.Add(2*time.Hour), issuer.issuer())
	if err != nil {
		t.Fatalf("append generation: %v", err)
	}
	if err := chain.Verify(evidence, VerifyOptions{Roots: ca.pool}); err != nil {
		t.Fatalf("verify with trusted root: %v", err)
	}

	data, err := json.Marshal(chain)
	if err != nil {
		t.Fatalf("marshal chain: %v", err)
	}
	restored, err := ParseChain(data)
	if err != nil {
		t.Fatalf("parse chain: %v", err)
	}
	if restored.EvidenceDigest() != chain.EvidenceDigest() || !restored.CapturedAt().Equal(chain.CapturedAt()) {
		t.Fatal("JSON round trip changed the immutable header")
	}
	if restored.Len() != chain.Len() {
		t.Fatalf("JSON round trip changed generation count: %d != %d", restored.Len(), chain.Len())
	}
	if err := restored.Verify(evidence, VerifyOptions{Roots: ca.pool}); err != nil {
		t.Fatalf("verify restored chain: %v", err)
	}

	emptyRoots := x509.NewCertPool()
	err = chain.Verify(evidence, VerifyOptions{Roots: emptyRoots})
	if !errors.Is(err, ErrUntrustedIssuer) {
		t.Fatalf("want ErrUntrustedIssuer with empty root pool, got %v", err)
	}
}

func TestStandardLibrarySignatureAlgorithms(t *testing.T) {
	for _, algorithm := range []string{"ecdsa", "ed25519", "rsa"} {
		t.Run(algorithm, func(t *testing.T) {
			captured := utc(2026, time.June, 1, 9, 0)
			ca := newTestCA(t, algorithm, utc(2020, time.January, 1, 0, 0), utc(2030, time.January, 1, 0, 0))
			issuer := ca.issue(t, algorithm, utc(2026, time.January, 1, 0, 0), utc(2028, time.January, 1, 0, 0))
			evidence := []byte("algorithm test evidence")

			chain, err := NewChainAt(evidence, captured, captured.Add(time.Hour), issuer.issuer())
			if err != nil {
				t.Fatalf("seal with %s: %v", algorithm, err)
			}
			chain, err = chain.Append(captured.Add(2*time.Hour), issuer.issuer())
			if err != nil {
				t.Fatalf("append with %s: %v", algorithm, err)
			}
			if err := chain.Verify(evidence); err != nil {
				t.Fatalf("verify with %s: %v", algorithm, err)
			}
		})
	}
}
