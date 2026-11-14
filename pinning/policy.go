package pinning

import (
	"crypto/x509"
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"
)

// Policy is a versioned pin publication. A policy is active from
// NotBefore until NotAfter. During rotation the publisher issues a new
// policy whose NotBefore is the rotation time and keeps the old policy
// active until rotation time plus the agreed grace period; afterwards
// the old policy expires and is no longer accepted.
type Policy struct {
	// Version identifies this publication, for example "2026-11-r1".
	Version string
	// NotBefore is the time from which this policy is effective.
	NotBefore time.Time
	// NotAfter is the time at which this policy expires. For a
	// superseded policy this is the rotation time plus the grace period.
	NotAfter time.Time
	// Roots is the exclusive trust anchor pool for chain building.
	// Verification never falls back to the system trust store.
	Roots *x509.CertPool
	// Pins maps each purpose to its declared pin set.
	Pins map[Purpose]*PinSet
}

func (p *Policy) validate() error {
	if p.Version == "" {
		return errors.New("pinning: policy version is required")
	}
	if p.NotBefore.IsZero() || p.NotAfter.IsZero() {
		return fmt.Errorf("pinning: policy %q requires NotBefore and NotAfter", p.Version)
	}
	if !p.NotBefore.Before(p.NotAfter) {
		return fmt.Errorf("pinning: policy %q has NotBefore >= NotAfter", p.Version)
	}
	if p.Roots == nil {
		return fmt.Errorf("pinning: policy %q requires an explicit root pool", p.Version)
	}
	if len(p.Pins) == 0 {
		return fmt.Errorf("pinning: policy %q declares no pin sets", p.Version)
	}
	return nil
}

func (p *Policy) activeAt(t time.Time) bool {
	return !t.Before(p.NotBefore) && t.Before(p.NotAfter)
}

// Manager holds the published policies and verifies peer certificate
// chains against every policy active at the current time. A chain is
// accepted if any active policy accepts it, which lets an old policy
// remain valid in parallel with its successor during the grace period.
type Manager struct {
	now func() time.Time

	mu       sync.RWMutex
	policies []*Policy
}

// NewManager returns a Manager. now may be nil to use the wall clock;
// tests inject a fixed clock to exercise rotation and expiry.
func NewManager(now func() time.Time) *Manager {
	if now == nil {
		now = time.Now
	}
	return &Manager{now: now}
}

// AddPolicy publishes a policy. Policies are evaluated in NotBefore
// order; duplicates of the same version are rejected.
func (m *Manager) AddPolicy(p *Policy) error {
	if err := p.validate(); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, existing := range m.policies {
		if existing.Version == p.Version {
			return fmt.Errorf("pinning: duplicate policy version %q", p.Version)
		}
	}
	m.policies = append(m.policies, p)
	sort.Slice(m.policies, func(i, j int) bool {
		return m.policies[i].NotBefore.Before(m.policies[j].NotBefore)
	})
	return nil
}

// activePolicies returns the policies in force at time t.
func (m *Manager) activePolicies(t time.Time) []*Policy {
	m.mu.RLock()
	defer m.mu.RUnlock()
	var active []*Policy
	for _, p := range m.policies {
		if p.activeAt(t) {
			active = append(active, p)
		}
	}
	return active
}
