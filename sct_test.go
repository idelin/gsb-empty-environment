package netsec

import (
	"crypto/x509"
	"errors"
	"testing"
	"time"
)

var (
	validationTime = time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
	retiredAt      = validationTime.Add(-30 * 24 * time.Hour)
)

func logID(b byte) (id [32]byte) {
	id[0] = b
	return id
}

var (
	logA1 = logID(0xA1) // operator Alpha
	logA2 = logID(0xA2) // operator Alpha (second log, same operator)
	logB1 = logID(0xB1) // operator Beta
	logR1 = logID(0xC1) // operator Gamma, retired
	logX1 = logID(0xD1) // operator Delta, rejected
	logP1 = logID(0xE1) // operator Epsilon, pending
)

func testLogs() LogList {
	logs := map[[32]byte]LogInfo{
		logA1: {LogID: logA1, Operator: "Alpha", State: LogStateUsable},
		logA2: {LogID: logA2, Operator: "Alpha", State: LogStateUsable},
		logB1: {LogID: logB1, Operator: "Beta", State: LogStateUsable},
		logR1: {LogID: logR1, Operator: "Gamma", State: LogStateRetired, RetiredAt: retiredAt},
		logX1: {LogID: logX1, Operator: "Delta", State: LogStateRejected},
		logP1: {LogID: logP1, Operator: "Epsilon", State: LogStatePending},
	}
	return LogListFunc(func(id [32]byte) (LogInfo, bool) {
		info, ok := logs[id]
		return info, ok
	})
}

// acceptAll treats every SCT signature as valid.
func acceptAll(_ *x509.Certificate, _ SignedCertificateTimestamp) bool { return true }

func newPolicy(verifier SignatureVerifier) *SCTPolicy {
	return &SCTPolicy{
		Verifier: verifier,
		Logs:     testLogs(),
		Now:      func() time.Time { return validationTime },
	}
}

func sct(id [32]byte, ts time.Time) SignedCertificateTimestamp {
	return SignedCertificateTimestamp{
		Version:   SCTVersionV1,
		LogID:     id,
		Timestamp: ts,
		Signature: []byte("sig"),
	}
}

func certWithLifetime(d time.Duration) *x509.Certificate {
	return &x509.Certificate{
		NotBefore: validationTime.Add(-d),
		NotAfter:  validationTime,
	}
}

func TestHappyPathShortLivedCert(t *testing.T) {
	p := newPolicy(SignatureVerifierFunc(acceptAll))
	cert := certWithLifetime(90 * 24 * time.Hour)
	scts := []SignedCertificateTimestamp{
		sct(logA1, validationTime.Add(-time.Hour)),
		sct(logB1, validationTime.Add(-time.Hour)),
	}
	if err := p.Evaluate(cert, scts); err != nil {
		t.Fatalf("expected success, got %v", err)
	}
}

func TestSameOperatorMultipleLogsRejected(t *testing.T) {
	p := newPolicy(SignatureVerifierFunc(acceptAll))
	cert := certWithLifetime(90 * 24 * time.Hour)
	// Two different logs, but both run by operator Alpha.
	scts := []SignedCertificateTimestamp{
		sct(logA1, validationTime.Add(-time.Hour)),
		sct(logA2, validationTime.Add(-time.Hour)),
	}
	err := p.Evaluate(cert, scts)
	var pe *PolicyError
	if !errors.As(err, &pe) {
		t.Fatalf("expected PolicyError, got %v", err)
	}
	if pe.ValidSCTs != 2 || pe.DistinctOps != 1 {
		t.Fatalf("unexpected counts: %+v", pe)
	}
}

func TestDuplicateSCTCountsOnce(t *testing.T) {
	p := newPolicy(SignatureVerifierFunc(acceptAll))
	cert := certWithLifetime(90 * 24 * time.Hour)
	// Same SCT submitted twice plus one from another operator: only 2 count.
	dup := sct(logA1, validationTime.Add(-time.Hour))
	scts := []SignedCertificateTimestamp{
		dup,
		dup,
		sct(logB1, validationTime.Add(-time.Hour)),
	}
	if err := p.Evaluate(cert, scts); err != nil {
		t.Fatalf("expected success, got %v", err)
	}

	// Duplicates alone must not reach the threshold.
	err := p.Evaluate(cert, []SignedCertificateTimestamp{dup, dup})
	var pe *PolicyError
	if !errors.As(err, &pe) {
		t.Fatalf("expected PolicyError, got %v", err)
	}
	if pe.ValidSCTs != 1 || pe.DistinctLogIDs != 1 {
		t.Fatalf("duplicate SCTs should count once: %+v", pe)
	}
}

func TestRetiredLogTimestamp(t *testing.T) {
	p := newPolicy(SignatureVerifierFunc(acceptAll))
	cert := certWithLifetime(90 * 24 * time.Hour)

	// SCT from retired log dated before retirement counts.
	okSCTs := []SignedCertificateTimestamp{
		sct(logA1, validationTime.Add(-time.Hour)),
		sct(logR1, retiredAt.Add(-time.Hour)),
	}
	if err := p.Evaluate(cert, okSCTs); err != nil {
		t.Fatalf("pre-retirement SCT should count, got %v", err)
	}

	// SCT from retired log dated after retirement does not count.
	badSCTs := []SignedCertificateTimestamp{
		sct(logA1, validationTime.Add(-time.Hour)),
		sct(logR1, retiredAt.Add(time.Hour)),
	}
	err := p.Evaluate(cert, badSCTs)
	var pe *PolicyError
	if !errors.As(err, &pe) {
		t.Fatalf("expected PolicyError, got %v", err)
	}
	if pe.ValidSCTs != 1 {
		t.Fatalf("post-retirement SCT must not count: %+v", pe)
	}
}

func TestLifetimeBoundary180Days(t *testing.T) {
	p := newPolicy(SignatureVerifierFunc(acceptAll))
	twoOps := []SignedCertificateTimestamp{
		sct(logA1, validationTime.Add(-time.Hour)),
		sct(logB1, validationTime.Add(-time.Hour)),
	}
	threeOps := append(append([]SignedCertificateTimestamp{}, twoOps...),
		sct(logR1, retiredAt.Add(-time.Hour)))

	// Exactly 180 days: 2 SCTs suffice.
	exact := certWithLifetime(LifetimeThreshold)
	if err := p.Evaluate(exact, twoOps); err != nil {
		t.Fatalf("180-day cert with 2 SCTs should pass, got %v", err)
	}

	// One second over 180 days: 2 SCTs no longer suffice, 3 do.
	over := certWithLifetime(LifetimeThreshold + time.Second)
	err := p.Evaluate(over, twoOps)
	var pe *PolicyError
	if !errors.As(err, &pe) {
		t.Fatalf("expected PolicyError for >180-day cert with 2 SCTs, got %v", err)
	}
	if pe.RequiredSCTs != 3 {
		t.Fatalf("expected requirement of 3 SCTs, got %+v", pe)
	}
	if err := p.Evaluate(over, threeOps); err != nil {
		t.Fatalf(">180-day cert with 3 SCTs should pass, got %v", err)
	}
}

func TestRejectedAndPendingLogsDoNotCount(t *testing.T) {
	p := newPolicy(SignatureVerifierFunc(acceptAll))
	cert := certWithLifetime(90 * 24 * time.Hour)
	scts := []SignedCertificateTimestamp{
		sct(logA1, validationTime.Add(-time.Hour)),
		sct(logX1, validationTime.Add(-time.Hour)), // rejected log
		sct(logP1, validationTime.Add(-time.Hour)), // pending log
	}
	err := p.Evaluate(cert, scts)
	var pe *PolicyError
	if !errors.As(err, &pe) {
		t.Fatalf("expected PolicyError, got %v", err)
	}
	if pe.ValidSCTs != 1 {
		t.Fatalf("rejected/pending log SCTs must not count: %+v", pe)
	}
}

func TestFutureTimestampSkew(t *testing.T) {
	p := newPolicy(SignatureVerifierFunc(acceptAll))
	cert := certWithLifetime(90 * 24 * time.Hour)

	// Within the 5-minute skew: counts.
	within := []SignedCertificateTimestamp{
		sct(logA1, validationTime.Add(4*time.Minute)),
		sct(logB1, validationTime.Add(5*time.Minute)),
	}
	if err := p.Evaluate(cert, within); err != nil {
		t.Fatalf("timestamps within skew should count, got %v", err)
	}

	// More than 5 minutes in the future: does not count.
	beyond := []SignedCertificateTimestamp{
		sct(logA1, validationTime.Add(-time.Hour)),
		sct(logB1, validationTime.Add(5*time.Minute+time.Second)),
	}
	err := p.Evaluate(cert, beyond)
	var pe *PolicyError
	if !errors.As(err, &pe) {
		t.Fatalf("expected PolicyError, got %v", err)
	}
	if pe.ValidSCTs != 1 {
		t.Fatalf("future timestamp beyond skew must not count: %+v", pe)
	}
}

func TestNonV1VersionDoesNotCount(t *testing.T) {
	p := newPolicy(SignatureVerifierFunc(acceptAll))
	cert := certWithLifetime(90 * 24 * time.Hour)
	v2 := sct(logB1, validationTime.Add(-time.Hour))
	v2.Version = SCTVersion(2)
	scts := []SignedCertificateTimestamp{
		sct(logA1, validationTime.Add(-time.Hour)),
		v2,
	}
	err := p.Evaluate(cert, scts)
	var pe *PolicyError
	if !errors.As(err, &pe) {
		t.Fatalf("expected PolicyError, got %v", err)
	}
	if pe.ValidSCTs != 1 {
		t.Fatalf("non-v1 SCT must not count: %+v", pe)
	}
}

func TestBadSignatureDoesNotCount(t *testing.T) {
	verifier := SignatureVerifierFunc(func(_ *x509.Certificate, s SignedCertificateTimestamp) bool {
		return s.LogID != logB1
	})
	p := newPolicy(verifier)
	cert := certWithLifetime(90 * 24 * time.Hour)
	scts := []SignedCertificateTimestamp{
		sct(logA1, validationTime.Add(-time.Hour)),
		sct(logB1, validationTime.Add(-time.Hour)),
	}
	err := p.Evaluate(cert, scts)
	var pe *PolicyError
	if !errors.As(err, &pe) {
		t.Fatalf("expected PolicyError, got %v", err)
	}
	if pe.ValidSCTs != 1 {
		t.Fatalf("SCT with invalid signature must not count: %+v", pe)
	}
}

func TestUnknownLogDoesNotCount(t *testing.T) {
	p := newPolicy(SignatureVerifierFunc(acceptAll))
	cert := certWithLifetime(90 * 24 * time.Hour)
	scts := []SignedCertificateTimestamp{
		sct(logA1, validationTime.Add(-time.Hour)),
		sct(logID(0xFF), validationTime.Add(-time.Hour)),
	}
	err := p.Evaluate(cert, scts)
	var pe *PolicyError
	if !errors.As(err, &pe) {
		t.Fatalf("expected PolicyError, got %v", err)
	}
	if pe.ValidSCTs != 1 {
		t.Fatalf("SCT from unknown log must not count: %+v", pe)
	}
}

func TestUnconfiguredPolicy(t *testing.T) {
	p := &SCTPolicy{}
	if err := p.Evaluate(certWithLifetime(90*24*time.Hour), nil); !errors.Is(err, ErrInvalidPolicy) {
		t.Fatalf("expected ErrInvalidPolicy, got %v", err)
	}
}
