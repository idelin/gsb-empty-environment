package workloadid

import (
	"crypto"
	"crypto/x509"
	"errors"
	"sync/atomic"
)

// Bundle/rotation errors.
var (
	ErrNoSnapshot       = errors.New("workloadid: no bundle generation has been activated")
	ErrShortLivedTTL    = errors.New("workloadid: credential exceeds short-lived TTL limit")
	ErrClockRollback    = errors.New("workloadid: observed clock moved backwards")
	ErrBundleValidation = errors.New("workloadid: bundle failed validation")
	ErrGenerationStale  = errors.New("workloadid: bundle generation is older than active generation")
	ErrNoCredential     = errors.New("workloadid: no workload credential available")
)

// BundleConfig controls generation-wide limits.
type BundleConfig struct {
	// TrustDomain is the mesh trust domain served by this control plane.
	TrustDomain string
	// MaxChainDepth bounds the number of certificates from leaf to root. Zero
	// applies a conservative default.
	MaxChainDepth int
	// MaxLeafTTL enforces the short-lived credential ceiling. Zero applies a
	// conservative default.
	MaxLeafTTL Duration
	// Revocation controls revocation checking behavior.
	Revocation RevocationConfig
}

const (
	defaultMaxChainDepth = 6
	// defaultMaxLeafTTL is 24h: workload identities must rotate frequently.
	defaultMaxLeafTTL Duration = 24 * 60 * 60 * 1_000_000_000
)

// RootBundle is the trust material for one generation.
type RootBundle struct {
	Roots         []*x509.Certificate
	Intermediates []*x509.Certificate
	Revocation    *RevocationState
}

// WorkloadCredential is the local workload's rotating identity material.
type WorkloadCredential struct {
	Leaf       *x509.Certificate
	PrivateKey crypto.PrivateKey
}

// Snapshot is an immutable, generation-tagged view of all trust material. A
// verification either observes one whole snapshot consistently or fails; a
// partial update can never be observed.
type Snapshot struct {
	Generation       uint64
	Config           BundleConfig
	Roots            *x509.CertPool
	Intermediates    *x509.CertPool
	rawRoots         []*x509.Certificate
	rawIntermediates []*x509.Certificate
	Revocation       *RevocationState
	Credential       *WorkloadCredential
}

// Manager owns the active snapshot and swaps it atomically when a new bundle
// generation or rotated credential is published.
type Manager struct {
	clock  Clock
	active atomic.Pointer[Snapshot]
	// maxObservedGeneration and lastWall are protected by the atomic snapshot
	// semantics; rollback is tracked in a dedicated atomic.
	lastWall atomicTime
}

// NewManager creates a manager backed by the given clock.
func NewManager(clock Clock) *Manager {
	if clock == nil {
		clock = RealClock{}
	}
	return &Manager{clock: clock}
}

// Current returns the active snapshot or ErrNoSnapshot.
func (m *Manager) Current() (*Snapshot, error) {
	snap := m.active.Load()
	if snap == nil {
		return nil, ErrNoSnapshot
	}
	return snap, nil
}

// MustCurrent returns the active snapshot and panics if none exists. It is
// intended for control-plane startup wiring where a bundle is mandatory.
func (m *Manager) MustCurrent() *Snapshot {
	snap, err := m.Current()
	if err != nil {
		panic(err)
	}
	return snap
}

// Activate validates and atomically installs a new bundle generation.
func (m *Manager) Activate(generation uint64, cfg BundleConfig, bundle RootBundle) (*Snapshot, error) {
	current, _ := m.Current()
	if current != nil && generation < current.Generation {
		return nil, ErrGenerationStale
	}
	if err := m.observeWall(); err != nil {
		return nil, err
	}

	roots := x509.NewCertPool()
	for _, root := range bundle.Roots {
		roots.AddCert(root)
	}
	intermediates := x509.NewCertPool()
	for _, intermediate := range bundle.Intermediates {
		intermediates.AddCert(intermediate)
	}

	revocation := bundle.Revocation
	if revocation == nil {
		revocation = &RevocationState{OCSP: map[string]OCSPRecord{}, CRLs: map[string]CRLRecord{}}
	}
	if err := revocation.build(); err != nil {
		return nil, errors.Join(ErrBundleValidation, err)
	}

	normalizeConfig(&cfg)
	snap := &Snapshot{
		Generation:       generation,
		Config:           cfg,
		Roots:            roots,
		Intermediates:    intermediates,
		rawRoots:         bundle.Roots,
		rawIntermediates: bundle.Intermediates,
		Revocation:       revocation,
	}
	if current != nil {
		snap.Credential = current.Credential
	}
	m.active.Store(snap)
	return snap, nil
}

// RotateCredential atomically replaces the workload credential within a new
// generation, enforcing the short-lived TTL ceiling. The trust material is
// carried over so credential rotation and bundle rotation are separate
// generation events, each atomic.
func (m *Manager) RotateCredential(generation uint64, credential WorkloadCredential) (*Snapshot, error) {
	current, err := m.Current()
	if err != nil {
		return nil, err
	}
	if generation < current.Generation {
		return nil, ErrGenerationStale
	}
	if credential.Leaf == nil || credential.PrivateKey == nil {
		return nil, errors.Join(ErrBundleValidation, ErrNoCredential)
	}
	if err := m.observeWall(); err != nil {
		return nil, err
	}
	now := m.clock.Now()
	maxTTL := current.Config.MaxLeafTTL
	if ttl := credential.Leaf.NotAfter.Sub(now); ttl > maxTTL {
		return nil, ErrShortLivedTTL
	}
	if !credential.Leaf.NotBefore.Before(now.Add(current.Config.Revocation.MaxSkew)) {
		return nil, ErrClockRollback
	}

	next := *current
	next.Generation = generation
	next.Credential = &credential
	m.active.Store(&next)
	return &next, nil
}

// Credential returns the active workload credential, if any.
func (m *Manager) Credential() (*WorkloadCredential, error) {
	snap, err := m.Current()
	if err != nil {
		return nil, err
	}
	if snap.Credential == nil {
		return nil, ErrNoCredential
	}
	return snap.Credential, nil
}

func normalizeConfig(cfg *BundleConfig) {
	if cfg.MaxChainDepth <= 0 {
		cfg.MaxChainDepth = defaultMaxChainDepth
	}
	if cfg.MaxLeafTTL <= 0 {
		cfg.MaxLeafTTL = defaultMaxLeafTTL
	}
}

// intermediateDERs returns the DER-encoded intermediate certificates held in
// the bundle, for embedding in presented TLS certificates.
func (s *Snapshot) intermediateDERs() [][]byte {
	if s == nil || s.Intermediates == nil {
		return nil
	}
	// x509.CertPool does not expose its contents publicly; retain the raw
	// intermediates on the snapshot instead.
	var out [][]byte
	for _, cert := range s.rawIntermediates {
		out = append(out, cert.Raw)
	}
	return out
}

// observeWall records wall-clock progression and rejects a backwards jump.
func (m *Manager) observeWall() error {
	now := m.clock.Now()
	previous := m.lastWall.load()
	if !previous.IsZero() && now.Before(previous) {
		return ErrClockRollback
	}
	m.lastWall.store(now)
	return nil
}
