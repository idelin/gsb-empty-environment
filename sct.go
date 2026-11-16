package netsec

import (
	"crypto/x509"
	"errors"
	"fmt"
	"time"
)

// SCTVersion is the version of a Signed Certificate Timestamp.
type SCTVersion int

// SCTVersionV1 is the only SCT version accepted by the policy.
const SCTVersionV1 SCTVersion = 1

// LogState describes the lifecycle state of a CT log at validation time.
type LogState int

const (
	// LogStateUsable means the log is currently qualified/usable.
	LogStateUsable LogState = iota
	// LogStateRetired means the log stopped accepting new entries at RetiredAt.
	LogStateRetired
	// LogStateRejected means the log has been rejected and must not be trusted.
	LogStateRejected
	// LogStatePending means the log is not yet included in the log program.
	LogStatePending
)

// LogInfo describes a CT log known to the caller-supplied log list.
type LogInfo struct {
	// LogID is the log's key identifier (SHA-256 of the log public key).
	LogID [32]byte
	// Operator identifies the entity running the log; SCTs from logs with
	// the same operator do not count as independent.
	Operator string
	// State is the log's lifecycle state at validation time.
	State LogState
	// RetiredAt is the moment the log stopped accepting new entries.
	// Only meaningful when State == LogStateRetired.
	RetiredAt time.Time
}

// LogList is injected by the caller and resolves log IDs to log metadata.
type LogList interface {
	Lookup(logID [32]byte) (LogInfo, bool)
}

// LogListFunc adapts a function to the LogList interface.
type LogListFunc func(logID [32]byte) (LogInfo, bool)

// Lookup implements LogList.
func (f LogListFunc) Lookup(logID [32]byte) (LogInfo, bool) { return f(logID) }

// SignedCertificateTimestamp is an SCT embedded in (or delivered with) a
// certificate, already parsed from its wire encoding.
type SignedCertificateTimestamp struct {
	Version   SCTVersion
	LogID     [32]byte
	Timestamp time.Time
	Signature []byte
}

// SignatureVerifier is injected by the caller and verifies an SCT's
// signature against the certificate it covers.
type SignatureVerifier interface {
	VerifySCTSignature(cert *x509.Certificate, sct SignedCertificateTimestamp) bool
}

// SignatureVerifierFunc adapts a function to the SignatureVerifier interface.
type SignatureVerifierFunc func(cert *x509.Certificate, sct SignedCertificateTimestamp) bool

// VerifySCTSignature implements SignatureVerifier.
func (f SignatureVerifierFunc) VerifySCTSignature(cert *x509.Certificate, sct SignedCertificateTimestamp) bool {
	return f(cert, sct)
}

// DefaultMaxClockSkew bounds how far an SCT timestamp may lie in the future
// relative to the validation time.
const DefaultMaxClockSkew = 5 * time.Minute

// LifetimeThreshold is the certificate validity boundary: certificates valid
// for at most this long need fewer SCTs than longer-lived ones.
const LifetimeThreshold = 180 * 24 * time.Hour

// SCTPolicy evaluates whether the SCTs accompanying a certificate satisfy
// the count and operator-diversity requirements. It is intended to be
// invoked from a post-handshake callback (e.g. tls.Config.VerifyPeerCertificate).
type SCTPolicy struct {
	// Verifier checks SCT signatures. Required.
	Verifier SignatureVerifier
	// Logs resolves log IDs to operator and state metadata. Required.
	Logs LogList
	// Now supplies the validation time; defaults to time.Now.
	Now func() time.Time
	// MaxClockSkew bounds acceptable future timestamps; defaults to
	// DefaultMaxClockSkew.
	MaxClockSkew time.Duration
}

// PolicyError reports why a certificate's SCT set failed the policy.
type PolicyError struct {
	Reason         string
	ValidSCTs      int
	RequiredSCTs   int
	DistinctLogIDs int
	DistinctOps    int
	RequiredMinOps int
}

func (e *PolicyError) Error() string {
	return fmt.Sprintf("sct policy: %s (valid SCTs %d/%d, distinct operators %d/%d)",
		e.Reason, e.ValidSCTs, e.RequiredSCTs, e.DistinctOps, e.RequiredMinOps)
}

// ErrInvalidPolicy is returned when the policy is not fully configured.
var ErrInvalidPolicy = errors.New("sct policy: verifier and log list are required")

// RequiredSCTCount returns the minimum number of valid SCTs for a
// certificate with the given validity period: 2 for lifetimes up to 180
// days, 3 for longer lifetimes.
func RequiredSCTCount(notBefore, notAfter time.Time) int {
	if notAfter.Sub(notBefore) <= LifetimeThreshold {
		return 2
	}
	return 3
}

// minDistinctOperators is the minimum number of independent log operators
// the valid SCTs must originate from.
const minDistinctOperators = 2

// Evaluate checks cert's embedded SCTs against the policy. It returns nil
// when the requirements are met, or a *PolicyError describing the failure.
func (p *SCTPolicy) Evaluate(cert *x509.Certificate, scts []SignedCertificateTimestamp) error {
	if p.Verifier == nil || p.Logs == nil {
		return ErrInvalidPolicy
	}
	now := time.Now()
	if p.Now != nil {
		now = p.Now()
	}
	skew := p.MaxClockSkew
	if skew <= 0 {
		skew = DefaultMaxClockSkew
	}

	required := RequiredSCTCount(cert.NotBefore, cert.NotAfter)

	seenLogs := make(map[[32]byte]struct{})
	operators := make(map[string]struct{})
	valid := 0

	for _, sct := range scts {
		if !p.countsAsValid(cert, sct, now, skew) {
			continue
		}
		if _, dup := seenLogs[sct.LogID]; dup {
			// Same log_id counts only once, even if submitted repeatedly.
			continue
		}
		info, _ := p.Logs.Lookup(sct.LogID)
		seenLogs[sct.LogID] = struct{}{}
		operators[info.Operator] = struct{}{}
		valid++
	}

	result := &PolicyError{
		ValidSCTs:      valid,
		RequiredSCTs:   required,
		DistinctLogIDs: len(seenLogs),
		DistinctOps:    len(operators),
		RequiredMinOps: minDistinctOperators,
	}
	if valid < required {
		result.Reason = "not enough valid SCTs"
		return result
	}
	if len(operators) < minDistinctOperators {
		result.Reason = "valid SCTs do not come from enough independent operators"
		return result
	}
	return nil
}

// countsAsValid reports whether a single SCT is well-formed and timely.
// It does not account for log_id deduplication.
func (p *SCTPolicy) countsAsValid(cert *x509.Certificate, sct SignedCertificateTimestamp, now time.Time, skew time.Duration) bool {
	if sct.Version != SCTVersionV1 {
		return false
	}
	if sct.Timestamp.After(now.Add(skew)) {
		// Timestamp too far in the future relative to validation time.
		return false
	}
	info, ok := p.Logs.Lookup(sct.LogID)
	if !ok {
		// Unknown log.
		return false
	}
	switch info.State {
	case LogStateUsable:
	case LogStateRetired:
		if sct.Timestamp.After(info.RetiredAt) {
			// Issued after the log stopped accepting entries.
			return false
		}
	case LogStateRejected, LogStatePending:
		return false
	default:
		return false
	}
	return p.Verifier.VerifySCTSignature(cert, sct)
}
