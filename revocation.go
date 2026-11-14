package netsec

import (
	"context"
	"crypto/sha256"
	"crypto/x509"
	"sync"
	"time"
)

// RevocationStatus is the result of a revocation lookup. Epoch identifies the
// state of the checked certificate; a change invalidates tickets issued under
// the older epoch even if the certificate is later reinstated.
type RevocationStatus struct {
	Revoked   bool
	Reason    string
	Epoch     uint64
	CheckedAt time.Time
}

// RevocationChecker checks the current revocation state of a verified chain.
// The chain is ordered leaf first. Implementations must be safe for concurrent
// use. Returning an error is treated as fail-closed.
type RevocationChecker interface {
	Check(ctx context.Context, chain []*x509.Certificate, at time.Time) (RevocationStatus, error)
}

type revocationEntry struct {
	revoked bool
	reason  string
	epoch   uint64
}

type revocationState struct {
	mu      sync.RWMutex
	epoch   uint64
	entries map[[sha256.Size]byte]revocationEntry
}

// MemoryRevocationChecker is an in-process checker backed by certificate
// fingerprints. Revoke advances the per-certificate epoch, so reinstating the
// certificate does not silently revive tickets minted before the revocation.
type MemoryRevocationChecker struct {
	state revocationState
}

func NewMemoryRevocationChecker() *MemoryRevocationChecker {
	return &MemoryRevocationChecker{
		state: revocationState{entries: make(map[[sha256.Size]byte]revocationEntry)},
	}
}

func (m *MemoryRevocationChecker) Revoke(cert *x509.Certificate, reason string) {
	if m == nil || cert == nil {
		return
	}
	m.RevokeFingerprint(sha256.Sum256(cert.Raw), reason)
}

func (m *MemoryRevocationChecker) RevokeFingerprint(fingerprint [sha256.Size]byte, reason string) {
	if m == nil {
		return
	}
	m.state.mu.Lock()
	defer m.state.mu.Unlock()
	m.state.epoch++
	m.state.entries[fingerprint] = revocationEntry{
		revoked: true,
		reason:  reason,
		epoch:   m.state.epoch,
	}
}

func (m *MemoryRevocationChecker) Reinstate(cert *x509.Certificate) {
	if m == nil || cert == nil {
		return
	}
	fingerprint := sha256.Sum256(cert.Raw)
	m.state.mu.Lock()
	defer m.state.mu.Unlock()
	entry, ok := m.state.entries[fingerprint]
	if !ok {
		return
	}
	entry.revoked = false
	entry.reason = ""
	m.state.entries[fingerprint] = entry
}

func (m *MemoryRevocationChecker) Check(ctx context.Context, chain []*x509.Certificate, at time.Time) (RevocationStatus, error) {
	if m == nil {
		return RevocationStatus{}, ErrRevocationUnavailable
	}
	if err := ctx.Err(); err != nil {
		return RevocationStatus{}, err
	}
	m.state.mu.RLock()
	defer m.state.mu.RUnlock()

	status := RevocationStatus{CheckedAt: at}
	for _, cert := range chain {
		if cert == nil {
			continue
		}
		fingerprint := sha256.Sum256(cert.Raw)
		entry, ok := m.state.entries[fingerprint]
		if !ok {
			continue
		}
		if entry.epoch > status.Epoch {
			status.Epoch = entry.epoch
		}
		if entry.revoked && !status.Revoked {
			status.Revoked = true
			status.Reason = entry.reason
		}
	}
	return status, nil
}
