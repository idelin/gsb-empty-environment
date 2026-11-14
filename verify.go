package netsec

import (
	"crypto/x509"
	"errors"
	"fmt"
	"sync"
	"time"
)

// Verification errors. All are wrapped with context and matchable via
// errors.Is.
var (
	ErrUnsupportedSCTVersion  = errors.New("netsec: unsupported SCT version")
	ErrUnknownLog             = errors.New("netsec: SCT log is not in the current trust list")
	ErrDuplicateLog           = errors.New("netsec: duplicate SCTs from the same log")
	ErrZeroSignature          = errors.New("netsec: SCT signature is empty or all zero")
	ErrTimestampAfterNotAfter = errors.New("netsec: SCT timestamp is after certificate notAfter")
	ErrTimestampInFuture      = errors.New("netsec: SCT timestamp is in the future")
	ErrSCTTooOld              = errors.New("netsec: SCT timestamp exceeds policy max age")
	ErrNotEnoughLogs          = errors.New("netsec: fewer distinct logs than policy requires")
	ErrNoIssuer               = errors.New("netsec: issuer certificate is required for embedded SCTs")
	ErrGenerationMismatch     = errors.New("netsec: log list and policy generations differ")
	ErrStaleGeneration        = errors.New("netsec: generation is not newer than the active one")
)

// maxClockSkew bounds how far an SCT timestamp may lie ahead of validation time.
const maxClockSkew = 5 * time.Minute

// Result summarizes a successful verification.
type Result struct {
	// Generation is the trust-list/policy generation the SCTs were
	// validated against.
	Generation uint64
	// ValidSCTs is the number of verified SCTs from distinct logs.
	ValidSCTs int
	// LogIDs are the IDs of the logs that vouched for the certificate.
	LogIDs [][32]byte
}

// Verifier validates SCT lists for TLS clients against a generationed log
// trust list and policy. It is safe for concurrent use.
type Verifier struct {
	mu     sync.RWMutex
	logs   *LogList
	policy Policy
}

// NewVerifier creates a Verifier pinned to the given trust list and policy,
// which must share the same generation.
func NewVerifier(logs *LogList, policy Policy) (*Verifier, error) {
	if logs == nil {
		return nil, errors.New("netsec: nil log list")
	}
	if logs.Generation != policy.Generation {
		return nil, fmt.Errorf("%w: list=%d policy=%d", ErrGenerationMismatch, logs.Generation, policy.Generation)
	}
	return &Verifier{logs: logs, policy: policy}, nil
}

// UpdateGeneration atomically switches the verifier to a newer generation of
// the trust list and policy. The new policy only takes effect together with
// its log list; older or equal generations are rejected.
func (v *Verifier) UpdateGeneration(logs *LogList, policy Policy) error {
	if logs == nil {
		return errors.New("netsec: nil log list")
	}
	if logs.Generation != policy.Generation {
		return fmt.Errorf("%w: list=%d policy=%d", ErrGenerationMismatch, logs.Generation, policy.Generation)
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	if policy.Generation <= v.policy.Generation {
		return fmt.Errorf("%w: %d <= %d", ErrStaleGeneration, policy.Generation, v.policy.Generation)
	}
	v.logs = logs
	v.policy = policy
	return nil
}

// Generation returns the currently active policy generation.
func (v *Verifier) Generation() uint64 {
	v.mu.RLock()
	defer v.mu.RUnlock()
	return v.policy.Generation
}

// VerifyEmbeddedSCTs verifies an SCT list embedded in leaf (the X.509 SCT
// extension). The SCTs must be precertificate SCTs; the precert TBS is
// rebuilt from leaf and issuer.
func (v *Verifier) VerifyEmbeddedSCTs(leaf, issuer *x509.Certificate, sctList []byte, now time.Time) (*Result, error) {
	if issuer == nil {
		return nil, ErrNoIssuer
	}
	tbs, err := PrecertTBSCertificate(leaf)
	if err != nil {
		return nil, err
	}
	entry := precertEntry(IssuerKeyHash(issuer), tbs)
	return v.verify(leaf, sctList, entry, now)
}

// VerifyTransmittedSCTs verifies an SCT list delivered out-of-band (TLS
// extension or OCSP staple) for the final certificate itself.
func (v *Verifier) VerifyTransmittedSCTs(leaf *x509.Certificate, sctList []byte, now time.Time) (*Result, error) {
	return v.verify(leaf, sctList, x509Entry(leaf.Raw), now)
}

func (v *Verifier) verify(leaf *x509.Certificate, sctList, entry []byte, now time.Time) (*Result, error) {
	v.mu.RLock()
	logs, policy := v.logs, v.policy
	v.mu.RUnlock()

	scts, err := ParseSCTList(sctList)
	if err != nil {
		return nil, err
	}
	seen := make(map[[32]byte]struct{}, len(scts))
	res := &Result{Generation: policy.Generation}
	for _, sct := range scts {
		if err := checkSCT(sct, leaf, logs, policy, entry, now); err != nil {
			return nil, err
		}
		if _, dup := seen[sct.LogID]; dup {
			return nil, fmt.Errorf("%w: log %x", ErrDuplicateLog, sct.LogID[:8])
		}
		seen[sct.LogID] = struct{}{}
		res.ValidSCTs++
		res.LogIDs = append(res.LogIDs, sct.LogID)
	}
	if res.ValidSCTs < policy.MinDistinctLogs {
		return nil, fmt.Errorf("%w: got %d, want %d", ErrNotEnoughLogs, res.ValidSCTs, policy.MinDistinctLogs)
	}
	return res, nil
}

func checkSCT(sct *SCT, leaf *x509.Certificate, logs *LogList, policy Policy, entry []byte, now time.Time) error {
	if sct.Version != VersionV1 {
		return fmt.Errorf("%w: %d", ErrUnsupportedSCTVersion, sct.Version)
	}
	log := logs.ByID(sct.LogID)
	if log == nil {
		return fmt.Errorf("%w: %x", ErrUnknownLog, sct.LogID[:8])
	}
	if isZeroSignature(sct.Signature) {
		return fmt.Errorf("%w: log %q", ErrZeroSignature, log.Name)
	}
	ts := sct.Time()
	if ts.After(leaf.NotAfter) {
		return fmt.Errorf("%w: %s > %s", ErrTimestampAfterNotAfter, ts, leaf.NotAfter)
	}
	if ts.After(now.Add(maxClockSkew)) {
		return fmt.Errorf("%w: %s", ErrTimestampInFuture, ts)
	}
	if policy.MaxAge > 0 && now.Sub(ts) > policy.MaxAge {
		return fmt.Errorf("%w: age %s > %s", ErrSCTTooOld, now.Sub(ts), policy.MaxAge)
	}
	return log.verify(sct, signatureInput(sct, entry))
}

func isZeroSignature(sig []byte) bool {
	for _, b := range sig {
		if b != 0 {
			return false
		}
	}
	return true
}
