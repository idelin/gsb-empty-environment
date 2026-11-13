package netsec

import (
	"bytes"
	"crypto"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"errors"
	"fmt"
	"sync"
)

// Sentinel errors returned by the policy. Use errors.Is to match them.
var (
	// ErrNoSCTs reports that a certificate carries no SCT at all.
	ErrNoSCTs = errors.New("netsec: certificate has no signed certificate timestamps")
	// ErrPolicyFailed reports that none of the certificate's SCTs is an
	// acceptable SCT: either no log is in the pinned set, or every pinned SCT
	// failed signature verification.
	ErrPolicyFailed = errors.New("netsec: no valid SCT from a pinned log")
)

// PinnedLog is one CT log that the policy trusts. ID is the log identifier
// (SHA-256 of the log's SubjectPublicKeyInfo DER) and Key is its public key
// used to verify SCT signatures.
type PinnedLog struct {
	ID  []byte
	Key crypto.PublicKey
}

// NewPinnedLog builds a PinnedLog from a log public key, deriving the log ID as
// SHA-256 over the DER-encoded SubjectPublicKeyInfo.
func NewPinnedLog(key crypto.PublicKey) (*PinnedLog, error) {
	id, err := LogID(key)
	if err != nil {
		return nil, err
	}
	return &PinnedLog{ID: id, Key: key}, nil
}

// NewPinnedLogWithID builds a PinnedLog with an explicit 32-byte log ID. The ID
// is checked against the identifier derived from the key.
func NewPinnedLogWithID(id []byte, key crypto.PublicKey) (*PinnedLog, error) {
	if len(id) != LogIDLength {
		return nil, fmt.Errorf("netsec: log id must be %d bytes, got %d", LogIDLength, len(id))
	}
	derived, err := LogID(key)
	if err != nil {
		return nil, err
	}
	if !bytes.Equal(derived, id) {
		return nil, errors.New("netsec: log id does not match public key")
	}
	return &PinnedLog{ID: append([]byte(nil), id...), Key: key}, nil
}

// LogID returns the CT log identifier for a public key: SHA-256 of the
// DER-encoded SubjectPublicKeyInfo.
func LogID(key crypto.PublicKey) ([]byte, error) {
	der, err := x509.MarshalPKIXPublicKey(key)
	if err != nil {
		return nil, fmt.Errorf("netsec: cannot marshal log public key: %w", err)
	}
	sum := sha256.Sum256(der)
	return sum[:], nil
}

func validateLogKey(key crypto.PublicKey) error {
	switch key.(type) {
	case *ecdsa.PublicKey, *rsa.PublicKey, ed25519.PublicKey:
		return nil
	default:
		return fmt.Errorf("netsec: unsupported log public key type %T", key)
	}
}

// Policy enforces CT log pinning. Verification succeeds only when a certificate
// has at least one SCT whose log ID is pinned and whose signature verifies over
// the certificate. SCTs from logs outside the pinned set are ignored.
type Policy struct {
	mu   sync.RWMutex
	logs map[string]*PinnedLog
}

// NewPolicy creates a policy preloaded with the given pinned logs.
func NewPolicy(logs ...*PinnedLog) (*Policy, error) {
	p := &Policy{logs: make(map[string]*PinnedLog)}
	for _, log := range logs {
		if err := p.Add(log); err != nil {
			return nil, err
		}
	}
	return p, nil
}

// Add inserts a pinned log. It is safe for concurrent use.
func (p *Policy) Add(log *PinnedLog) error {
	if log == nil {
		return errors.New("netsec: nil pinned log")
	}
	if len(log.ID) != LogIDLength {
		return fmt.Errorf("netsec: log id must be %d bytes, got %d", LogIDLength, len(log.ID))
	}
	if err := validateLogKey(log.Key); err != nil {
		return err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.logs == nil {
		p.logs = make(map[string]*PinnedLog)
	}
	id := append([]byte(nil), log.ID...)
	p.logs[string(id)] = &PinnedLog{ID: id, Key: log.Key}
	return nil
}

// Pinned reports whether logID belongs to the pinned set.
func (p *Policy) Pinned(logID []byte) bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	_, ok := p.logs[string(logID)]
	return ok
}

// VerifyCertificate applies the pinning policy to a parsed certificate, using
// the SCTs embedded in its SignedCertificateTimestampList extension.
func (p *Policy) VerifyCertificate(cert *x509.Certificate) error {
	if cert == nil {
		return errors.New("netsec: nil certificate")
	}
	scts, err := EmbeddedSCTs(cert)
	if err != nil {
		return fmt.Errorf("%w: malformed embedded SCT list: %v", ErrPolicyFailed, err)
	}
	return p.VerifySCTs(cert.Raw, scts)
}

// VerifySCT checks a single serialized SCT against the pinned set and the
// certificate. It returns an error if the log is not pinned or the signature
// does not verify.
func (p *Policy) VerifySCT(certDER []byte, rawSCT []byte) error {
	sct, err := ParseSCT(rawSCT)
	if err != nil {
		return err
	}
	p.mu.RLock()
	log := p.logs[string(sct.LogID)]
	p.mu.RUnlock()
	if log == nil {
		return fmt.Errorf("%w: log id %x", ErrPolicyFailed, sct.LogID)
	}
	if err := verifySCTSignature(sct, log.Key, certDER); err != nil {
		return err
	}
	return nil
}

// VerifySCTs applies the pinning policy to a certificate and its SCTs. Each
// entry in scts is one serialized SCT (the form stored in crypto/x509
// Certificate.SignedCertificateTimestamps). The policy passes as soon as one
// SCT is both from a pinned log and has a valid signature over certDER.
func (p *Policy) VerifySCTs(certDER []byte, scts [][]byte) error {
	if len(certDER) == 0 {
		return errors.New("netsec: empty certificate DER")
	}
	if len(scts) == 0 {
		return ErrNoSCTs
	}

	var failures []error
	unpinned := 0
	for _, raw := range scts {
		sct, err := ParseSCT(raw)
		if err != nil {
			failures = append(failures, err)
			continue
		}

		p.mu.RLock()
		log := p.logs[string(sct.LogID)]
		p.mu.RUnlock()
		if log == nil {
			unpinned++
			failures = append(failures, fmt.Errorf("netsec: SCT log %x is not in the pinned set", sct.LogID))
			continue
		}

		if err := verifySCTSignature(sct, log.Key, certDER); err != nil {
			failures = append(failures, fmt.Errorf("netsec: pinned SCT log %x: %w", sct.LogID, err))
			continue
		}
		return nil
	}

	if len(failures) == 0 {
		return ErrPolicyFailed
	}
	return fmt.Errorf("%w (%d SCTs, %d from unpinned logs): %w",
		ErrPolicyFailed, len(scts), unpinned, errors.Join(failures...))
}
