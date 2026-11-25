package workloadid

import (
	"crypto/x509"
	"errors"
	"time"
)

// Verification errors.
var (
	ErrNoPeerCertificates = errors.New("workloadid: peer presented no certificate")
	ErrChainDepth         = errors.New("workloadid: certificate chain exceeds max depth")
	ErrLeafIsCA           = errors.New("workloadid: workload leaf must not be a CA")
	ErrLeafKeyUsage       = errors.New("workloadid: leaf missing DigitalSignature key usage")
	ErrCertNotValidYet    = errors.New("workloadid: certificate NotBefore is in the future")
	ErrCertExpired        = errors.New("workloadid: certificate NotAfter has passed")
	ErrChainVerify        = errors.New("workloadid: signature chain verification failed")
	ErrNoPolicies         = errors.New("workloadid: caller and target policies are both required")
)

// Decision is the result of authenticating and authorizing one connection.
type Decision struct {
	Identity   *Identity
	Generation uint64
	Resumed    bool
	// CallerMatched and TargetMatched record the policy outcomes.
	CallerMatched bool
	TargetMatched bool
}

// AuthRequest carries everything needed to evaluate one connection. The same
// structure is used for fresh handshakes and session resumptions so policy is
// always re-evaluated against the current generation.
type AuthRequest struct {
	// RawCerts are the peer certificates (leaf first) from the TLS state.
	RawCerts [][]byte
	// Resumed indicates the TLS handshake resumed a prior session.
	Resumed bool
	// CallerPolicy is evaluated against the peer identity.
	CallerPolicy *Policy
	// TargetPolicy is evaluated against the local workload identity.
	TargetPolicy *Policy
	// Attestation is the node evidence for the peer.
	Attestation *NodeAttestationClaim
	// ExpectedServerName is optional SNI enforcement for the target DNS SAN.
	ExpectedServerName string
}

// Verifier performs workload authentication against the current Manager
// generation. It is safe for concurrent use.
type Verifier struct {
	manager    *Manager
	clock      Clock
	nonceCache NonceCache
	// wallHighWater tracks the greatest verification time observed across all
	// connections so a clock that jumps backwards between handshakes is caught
	// even when the per-chain monotonic check has no prior value.
	wallHighWater atomicTime
	// MaxAttestationAge is the default claim freshness bound.
	MaxAttestationAge Duration
}

// NewVerifier builds a Verifier bound to a Manager.
func NewVerifier(manager *Manager, opts ...VerifierOption) *Verifier {
	v := &Verifier{
		manager:           manager,
		clock:             manager.clock,
		MaxAttestationAge: 5 * time.Minute,
	}
	for _, opt := range opts {
		opt(v)
	}
	if v.nonceCache == nil {
		v.nonceCache = NewMemoryNonceCache(v.MaxAttestationAge)
	}
	return v
}

// VerifierOption customizes a Verifier.
type VerifierOption func(*Verifier)

// WithClock overrides the clock (mainly for tests).
func WithClock(clock Clock) VerifierOption {
	return func(v *Verifier) { v.clock = clock }
}

// WithNonceCache supplies a custom replay cache.
func WithNonceCache(cache NonceCache) VerifierOption {
	return func(v *Verifier) { v.nonceCache = cache }
}

// Authenticate fully verifies the peer chain and returns its identity. It is the
// entry point for a fresh mTLS handshake.
func (v *Verifier) Authenticate(rawCerts [][]byte) (*Identity, *Snapshot, error) {
	if len(rawCerts) == 0 {
		return nil, nil, ErrNoPeerCertificates
	}
	snap, err := v.manager.Current()
	if err != nil {
		return nil, nil, err
	}
	now := v.clock.Now()
	if err := v.guardMonotonic(now); err != nil {
		return nil, nil, err
	}

	certs, err := parseCerts(rawCerts)
	if err != nil {
		return nil, nil, err
	}
	leaf := certs[0]

	if err := validateLeafBasics(leaf, now, snap.Config); err != nil {
		return nil, nil, err
	}

	chain, err := verifyChain(leaf, certs[1:], snap, now)
	if err != nil {
		return nil, nil, errors.Join(ErrChainVerify, err)
	}
	if len(chain) > snap.Config.MaxChainDepth {
		return nil, nil, ErrChainDepth
	}

	if err := checkNameConstraints(leaf, chain[1:]); err != nil {
		return nil, nil, err
	}

	if err := v.checkChainRevocation(chain, snap, now); err != nil {
		return nil, nil, err
	}

	id, err := buildIdentity(leaf)
	if err != nil {
		return nil, nil, err
	}
	if id.TrustDomain != snap.Config.TrustDomain {
		return nil, nil, ErrPolicyDenied
	}
	return id, snap, nil
}

// AuthenticateResumed re-validates a resumed session against the current
// generation. The chain was verified when the session was first established, but
// revocation and policy must be re-checked because the bundle may have rotated.
func (v *Verifier) AuthenticateResumed(rawCerts [][]byte) (*Identity, *Snapshot, error) {
	if len(rawCerts) == 0 {
		return nil, nil, ErrNoPeerCertificates
	}
	snap, err := v.manager.Current()
	if err != nil {
		return nil, nil, err
	}
	now := v.clock.Now()
	if err := v.guardMonotonic(now); err != nil {
		return nil, nil, err
	}
	leaf, err := parseLeaf(rawCerts[0])
	if err != nil {
		return nil, nil, err
	}
	if err := validateLeafTime(leaf, now); err != nil {
		return nil, nil, err
	}
	// Re-check revocation of the leaf against the latest generation.
	var last Time
	// On resumption crypto/tls only retains the leaf, so OCSP certID (which
	// needs issuer material) cannot be recomputed here; CRL checks keyed by the
	// leaf's issuer still apply. Passing the leaf as its own issuer means an OCSP
	// record is ignored on this path rather than falsely validated.
	if err := checkRevocation(leaf, leaf, snap.Revocation, snap.Config.Revocation, now, &last); err != nil {
		return nil, nil, err
	}
	id, err := buildIdentity(leaf)
	if err != nil {
		return nil, nil, err
	}
	if id.TrustDomain != snap.Config.TrustDomain {
		return nil, nil, ErrPolicyDenied
	}
	return id, snap, nil
}

// guardMonotonic enforces that verification time never moves backwards across
// connections. A backwards jump is a strong indicator of clock-rollback
// tampering aimed at resurrecting expired or revoked material.
func (v *Verifier) guardMonotonic(now Time) error {
	previous := v.wallHighWater.load()
	if !previous.IsZero() && now.Before(previous) {
		return ErrClockRollback
	}
	v.wallHighWater.store(now)
	return nil
}

// Authorize evaluates both the caller and target policies for a connection and
// re-evaluates them on every call, including session resumption.
func (v *Verifier) Authorize(req AuthRequest) (*Decision, error) {
	if req.CallerPolicy == nil || req.TargetPolicy == nil {
		return nil, ErrNoPolicies
	}

	var (
		id   *Identity
		snap *Snapshot
		err  error
	)
	if req.Resumed {
		id, snap, err = v.AuthenticateResumed(req.RawCerts)
	} else {
		id, snap, err = v.Authenticate(req.RawCerts)
	}
	if err != nil {
		return nil, err
	}

	// Attestation freshness and replay are enforced for fresh handshakes.
	if req.CallerPolicy.RequireAttestation != nil && !req.Resumed {
		maxAge := req.CallerPolicy.RequireAttestation.MaxAge
		if maxAge <= 0 {
			maxAge = v.MaxAttestationAge
		}
		if err := req.Attestation.validateFreshness(v.clock.Now(), maxAge, v.nonceCache); err != nil {
			return nil, err
		}
	}

	if err := req.CallerPolicy.Validate(); err != nil {
		return nil, err
	}
	if err := req.TargetPolicy.Validate(); err != nil {
		return nil, err
	}

	if err := req.CallerPolicy.Matches(id, req.Attestation); err != nil {
		return nil, err
	}

	localID, err := v.localIdentity(snap)
	if err != nil {
		return nil, err
	}
	if err := req.TargetPolicy.Matches(localID, nil); err != nil {
		return nil, err
	}

	if req.ExpectedServerName != "" && !id.HasSAN(SANDNS, req.ExpectedServerName) {
		return nil, ErrSANTypeMismatch
	}

	return &Decision{
		Identity:      id,
		Generation:    snap.Generation,
		Resumed:       req.Resumed,
		CallerMatched: true,
		TargetMatched: true,
	}, nil
}

func (v *Verifier) localIdentity(snap *Snapshot) (*Identity, error) {
	if snap.Credential == nil {
		return nil, ErrNoCredential
	}
	return buildIdentity(snap.Credential.Leaf)
}

func (v *Verifier) checkChainRevocation(chain []*x509.Certificate, snap *Snapshot, now Time) error {
	var last Time
	// The root is trusted out of band; revocation applies from leaf through
	// intermediates.
	for i, cert := range chain {
		if isRoot(cert, snap) {
			continue
		}
		issuer := cert
		if i+1 < len(chain) {
			issuer = chain[i+1]
		}
		if err := checkRevocation(cert, issuer, snap.Revocation, snap.Config.Revocation, now, &last); err != nil {
			return err
		}
	}
	return nil
}

func isRoot(cert *x509.Certificate, snap *Snapshot) bool {
	return cert.Subject.String() == cert.Issuer.String()
}

func parseCerts(raw [][]byte) ([]*x509.Certificate, error) {
	certs := make([]*x509.Certificate, 0, len(raw))
	for _, der := range raw {
		cert, err := x509.ParseCertificate(der)
		if err != nil {
			return nil, errors.Join(ErrChainVerify, err)
		}
		certs = append(certs, cert)
	}
	return certs, nil
}

func parseLeaf(der []byte) (*x509.Certificate, error) {
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		return nil, errors.Join(ErrChainVerify, err)
	}
	return cert, nil
}

func validateLeafBasics(leaf *x509.Certificate, now Time, cfg BundleConfig) error {
	if leaf.IsCA {
		return ErrLeafIsCA
	}
	if leaf.KeyUsage&x509.KeyUsageDigitalSignature == 0 {
		return ErrLeafKeyUsage
	}
	if err := validateLeafTime(leaf, now); err != nil {
		return err
	}
	if ttl := leaf.NotAfter.Sub(now); ttl > cfg.MaxLeafTTL {
		return ErrShortLivedTTL
	}
	return nil
}

func validateLeafTime(leaf *x509.Certificate, now Time) error {
	// A NotBefore in the future (beyond skew) is treated as clock-rollback
	// evidence rather than silently accepted.
	if now.Before(leaf.NotBefore) {
		return ErrCertNotValidYet
	}
	if now.After(leaf.NotAfter) {
		return ErrCertExpired
	}
	return nil
}

func verifyChain(leaf *x509.Certificate, intermediates []*x509.Certificate, snap *Snapshot, now Time) ([]*x509.Certificate, error) {
	opts := x509.VerifyOptions{
		Roots:         snap.Roots,
		Intermediates: snap.Intermediates,
		CurrentTime:   now,
		KeyUsages:     []x509.ExtKeyUsage{x509.ExtKeyUsageAny},
	}
	chains, err := leaf.Verify(opts)
	if err != nil {
		return nil, err
	}
	// Prefer a chain whose intermediates are all in the bundle and that is
	// shortest to keep the depth bound meaningful.
	return chains[0], nil
}
