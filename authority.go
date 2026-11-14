package netsec

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"crypto/subtle"
	"crypto/x509"
	"encoding/binary"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const (
	defaultMaxCertificateLifetime = 24 * time.Hour
	defaultMaxAttestationAge      = 10 * time.Minute
	defaultClockSkew              = time.Minute
	defaultMaxChainLength         = 8
)

// Config configures an Authority. TrustDomain and a node attestor are
// required. The trust domain is local; workloads from other trust domains are
// rejected unless the trust domain is explicitly federated.
type Config struct {
	TrustDomain            TrustDomain
	FederatedTrustDomains  []TrustDomain
	NodeAttestor           NodeAttestor
	AttestationKeys        map[string]ed25519.PublicKey
	Clock                  func() time.Time
	MaxCertificateLifetime time.Duration
	MaxAttestationAge      time.Duration
	ClockSkew              time.Duration
	MaxPathDepth           int
	MaxChainLength         int

	// AllowEmptyAttestationNonce disables the requirement that attestation
	// documents carry a nonce and that connection nonces match them. It should
	// only be used by offline importers, never for live connection setup.
	AllowEmptyAttestationNonce bool

	// RequireRotationRecords rejects workloads that have no rotation record.
	// Rotation records, when present, always take precedence over expiry: a
	// certificate outside the active generation is rejected immediately.
	RequireRotationRecords bool
}

type authorityConfig struct {
	allowedTrustDomains    map[TrustDomain]struct{}
	attestor               NodeAttestor
	clock                  func() time.Time
	maxCertificateLifetime time.Duration
	maxAttestationAge      time.Duration
	clockSkew              time.Duration
	maxPathDepth           int
	maxChainLength         int
	allowEmptyNonce        bool
	requireRotationRecords bool
}

// Authority is a concurrency-safe workload identity verifier and connection
// authorizer. Readers use one immutable snapshot; updates are serialized and
// must carry strictly newer generations.
type Authority struct {
	cfg         authorityConfig
	mu          sync.Mutex
	state       atomic.Pointer[state]
	evaluations atomic.Uint64
}

// Verifier is an alias for Authority.
type Verifier = Authority

// NewAuthority constructs an Authority.
func NewAuthority(cfg Config) (*Authority, error) {
	local, err := ParseTrustDomain(string(cfg.TrustDomain))
	if err != nil {
		return nil, fmt.Errorf("%w: trust domain: %v", ErrInvalidConfig, err)
	}

	allowed := map[TrustDomain]struct{}{local: {}}
	for i, raw := range cfg.FederatedTrustDomains {
		td, parseErr := ParseTrustDomain(string(raw))
		if parseErr != nil {
			return nil, fmt.Errorf("%w: federated trust domain %d: %v", ErrInvalidConfig, i, parseErr)
		}
		allowed[td] = struct{}{}
	}

	attestor := cfg.NodeAttestor
	if attestor == nil {
		if len(cfg.AttestationKeys) == 0 {
			return nil, fmt.Errorf("%w: a node attestor or attestation key registry is required", ErrInvalidConfig)
		}
		attestor = NewEd25519Attestor(cfg.AttestationKeys)
	}

	clock := cfg.Clock
	if clock == nil {
		clock = time.Now
	}
	maxLifetime := cfg.MaxCertificateLifetime
	if maxLifetime == 0 {
		maxLifetime = defaultMaxCertificateLifetime
	}
	if maxLifetime < 0 {
		return nil, fmt.Errorf("%w: max certificate lifetime must be positive", ErrInvalidConfig)
	}
	maxAge := cfg.MaxAttestationAge
	if maxAge == 0 {
		maxAge = defaultMaxAttestationAge
	}
	if maxAge < 0 {
		return nil, fmt.Errorf("%w: max attestation age must be positive", ErrInvalidConfig)
	}
	skew := cfg.ClockSkew
	if skew == 0 {
		skew = defaultClockSkew
	}
	if skew < 0 {
		return nil, fmt.Errorf("%w: clock skew must not be negative", ErrInvalidConfig)
	}
	maxPathDepth := cfg.MaxPathDepth
	if maxPathDepth == 0 {
		maxPathDepth = defaultMaxPathDepth
	}
	if maxPathDepth < 0 {
		return nil, fmt.Errorf("%w: max path depth must be positive", ErrInvalidConfig)
	}
	maxChainLength := cfg.MaxChainLength
	if maxChainLength == 0 {
		maxChainLength = defaultMaxChainLength
	}
	if maxChainLength < 1 {
		return nil, fmt.Errorf("%w: max chain length must be positive", ErrInvalidConfig)
	}

	a := &Authority{cfg: authorityConfig{
		allowedTrustDomains:    allowed,
		attestor:               attestor,
		clock:                  clock,
		maxCertificateLifetime: maxLifetime,
		maxAttestationAge:      maxAge,
		clockSkew:              skew,
		maxPathDepth:           maxPathDepth,
		maxChainLength:         maxChainLength,
		allowEmptyNonce:        cfg.AllowEmptyAttestationNonce,
		requireRotationRecords: cfg.RequireRotationRecords,
	}}
	a.state.Store(newState())
	return a, nil
}

// NewVerifier is an alias for NewAuthority.
func NewVerifier(cfg Config) (*Authority, error) {
	return NewAuthority(cfg)
}

// UpdateBundle installs a full trust bundle for one trust domain. A bundle
// update with a generation less than or equal to the current one is rejected.
func (a *Authority) UpdateBundle(td TrustDomain, generation Generation, roots []*x509.Certificate) error {
	canonicalTD, err := ParseTrustDomain(string(td))
	if err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidConfig, err)
	}
	if len(roots) == 0 {
		return fmt.Errorf("%w: trust bundle is empty", ErrInvalidConfig)
	}
	for i, root := range roots {
		if root == nil {
			return fmt.Errorf("%w: bundle root %d is nil", ErrInvalidConfig, i)
		}
		if !root.BasicConstraintsValid || !root.IsCA {
			return fmt.Errorf("%w: bundle root %d is not a CA", ErrInvalidConfig, i)
		}
		if root.KeyUsage != 0 && root.KeyUsage&x509.KeyUsageCertSign == 0 {
			return fmt.Errorf("%w: bundle root %d cannot sign certificates", ErrInvalidConfig, i)
		}
	}
	cloned := cloneCertificates(roots)
	return a.mutate(func(next *state) error {
		if current, ok := next.bundles[canonicalTD]; ok && generation <= current.generation {
			return fmt.Errorf("%w: bundle %s generation %d is not newer than %d", ErrStaleGeneration, canonicalTD, generation, current.generation)
		}
		next.bundles[canonicalTD] = bundleState{generation: generation, roots: cloned}
		return nil
	})
}

// UpdateTrustBundle is an alias for UpdateBundle.
func (a *Authority) UpdateTrustBundle(td TrustDomain, generation Generation, roots []*x509.Certificate) error {
	return a.UpdateBundle(td, generation, roots)
}

// UpdateRevocations replaces the complete revocation list for a trust domain.
func (a *Authority) UpdateRevocations(td TrustDomain, generation Generation, entries []Revocation) error {
	canonicalTD, err := ParseTrustDomain(string(td))
	if err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidConfig, err)
	}
	compiled, err := compileRevocations(canonicalTD, entries)
	if err != nil {
		return err
	}
	return a.mutate(func(next *state) error {
		if current, ok := next.revocations[canonicalTD]; ok && generation <= current.generation {
			return fmt.Errorf("%w: revocation list %s generation %d is not newer than %d", ErrStaleGeneration, canonicalTD, generation, current.generation)
		}
		next.revocations[canonicalTD] = revocationState{generation: generation, entries: compiled}
		return nil
	})
}

// RevokeCertificates is a convenience wrapper around UpdateRevocations.
func (a *Authority) RevokeCertificates(td TrustDomain, generation Generation, certs ...*x509.Certificate) error {
	now := a.cfg.clock()
	entries := make([]Revocation, 0, len(certs))
	for _, cert := range certs {
		entry, err := RevocationForCertificate(td, cert, now)
		if err != nil {
			return err
		}
		entries = append(entries, entry)
	}
	return a.UpdateRevocations(td, generation, entries)
}

// UpdateRotations replaces the short-lived certificate rotation snapshot.
func (a *Authority) UpdateRotations(generation Generation, records []RotationRecord) error {
	compiled, err := compileRotations(generation, records)
	if err != nil {
		return err
	}
	return a.mutate(func(next *state) error {
		if next.rotations.initialized && generation <= next.rotations.generation {
			return fmt.Errorf("%w: rotation generation %d is not newer than %d", ErrStaleGeneration, generation, next.rotations.generation)
		}
		next.rotations = rotationState{generation: generation, initialized: true, records: compiled}
		return nil
	})
}

// UpdatePolicies replaces the complete named connection-policy snapshot.
func (a *Authority) UpdatePolicies(generation Generation, policies map[string]ConnectionPolicy) error {
	compiled := make(map[string]compiledConnectionPolicy, len(policies))
	for name, policy := range policies {
		if name == "" {
			return fmt.Errorf("%w: policy name is empty", ErrInvalidConfig)
		}
		item, err := compileConnectionPolicy(policy)
		if err != nil {
			return fmt.Errorf("%w: policy %q: %w", ErrInvalidConfig, name, err)
		}
		compiled[name] = item
	}
	return a.mutate(func(next *state) error {
		if next.policies.initialized && generation <= next.policies.generation {
			return fmt.Errorf("%w: policy generation %d is not newer than %d", ErrStaleGeneration, generation, next.policies.generation)
		}
		next.policies = policyState{generation: generation, initialized: true, policies: compiled}
		return nil
	})
}

// SetPolicies is an alias for UpdatePolicies.
func (a *Authority) SetPolicies(generation Generation, policies map[string]ConnectionPolicy) error {
	return a.UpdatePolicies(generation, policies)
}

func (a *Authority) mutate(fn func(*state) error) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	next := a.state.Load().clone()
	if err := fn(next); err != nil {
		return err
	}
	a.state.Store(next)
	return nil
}

// Generations returns the current generation snapshot.
func (a *Authority) Generations() Generations {
	return a.state.Load().generations()
}

// Evaluations returns the number of connection authorization evaluations.
func (a *Authority) Evaluations() uint64 {
	return a.evaluations.Load()
}

// PeerCredential is the certificate chain and node attestation presented by one
// side of an mTLS connection.
type PeerCredential struct {
	Leaf          *x509.Certificate
	Intermediates []*x509.Certificate
	Attestation   *NodeAttestation
}

// CredentialFromChain builds a credential from a leaf-first certificate chain.
func CredentialFromChain(chain []*x509.Certificate, attestation *NodeAttestation) (PeerCredential, error) {
	if len(chain) == 0 || chain[0] == nil {
		return PeerCredential{}, ErrNoCertificate
	}
	return PeerCredential{
		Leaf:          chain[0],
		Intermediates: append([]*x509.Certificate(nil), chain[1:]...),
		Attestation:   attestation,
	}, nil
}

// ParseCredential parses a leaf DER certificate and optional intermediate DER
// certificates into a PeerCredential.
func ParseCredential(leafDER []byte, intermediatesDER [][]byte, attestation *NodeAttestation) (PeerCredential, error) {
	leaf, err := x509.ParseCertificate(leafDER)
	if err != nil {
		return PeerCredential{}, fmt.Errorf("%w: parse leaf: %v", ErrInvalidCertificate, err)
	}
	intermediates := make([]*x509.Certificate, 0, len(intermediatesDER))
	for i, der := range intermediatesDER {
		cert, parseErr := x509.ParseCertificate(der)
		if parseErr != nil {
			return PeerCredential{}, fmt.Errorf("%w: parse intermediate %d: %v", ErrInvalidCertificate, i, parseErr)
		}
		intermediates = append(intermediates, cert)
	}
	return PeerCredential{Leaf: leaf, Intermediates: intermediates, Attestation: attestation}, nil
}

// VerifyPeer verifies the certificate chain, SAN set, revocation status,
// rotation generation, and node attestation. It does not apply a connection
// policy or bind a challenge nonce; use AuthorizeConnection for live mTLS.
func (a *Authority) VerifyPeer(ctx context.Context, credential PeerCredential) (*WorkloadIdentity, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	return a.verifyCredential(ctx, a.state.Load(), a.cfg.clock(), credential, 0, nil, false)
}

// Authenticate is an alias for VerifyPeer.
func (a *Authority) Authenticate(ctx context.Context, credential PeerCredential) (*WorkloadIdentity, error) {
	return a.VerifyPeer(ctx, credential)
}

// Verify is an alias for VerifyPeer.
func (a *Authority) Verify(ctx context.Context, credential PeerCredential) (*WorkloadIdentity, error) {
	return a.VerifyPeer(ctx, credential)
}

// VerifyPeerWithNonce verifies a peer and binds its attestation to a challenge.
func (a *Authority) VerifyPeerWithNonce(ctx context.Context, credential PeerCredential, nonce []byte) (*WorkloadIdentity, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	return a.verifyCredential(ctx, a.state.Load(), a.cfg.clock(), credential, 0, nonce, true)
}

// ConnectionRequest is evaluated afresh for every connection establishment.
type ConnectionRequest struct {
	Policy string
	Caller PeerCredential
	Target PeerCredential
	Action string
	// Nonce is the fresh control-plane challenge. Both peer attestations must
	// be bound to this value.
	Nonce []byte
}

// ConnectionDecision records the identities and generations used for an
// allowed connection.
type ConnectionDecision struct {
	Policy      string
	Action      string
	Caller      *WorkloadIdentity
	Target      *WorkloadIdentity
	Generations Generations
	EvaluatedAt time.Time
}

// AuthorizeConnection verifies both peers against one immutable snapshot and
// evaluates the caller policy, target policy, and action on every call.
func (a *Authority) AuthorizeConnection(ctx context.Context, request ConnectionRequest) (*ConnectionDecision, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	a.evaluations.Add(1)
	snapshot := a.state.Load()
	now := a.cfg.clock()

	policy, ok := snapshot.policies.policies[request.Policy]
	if !ok {
		return nil, fmt.Errorf("%w: %q", ErrNoPolicy, request.Policy)
	}

	caller, err := a.verifyCredential(ctx, snapshot, now, request.Caller, UseClient, request.Nonce, true)
	if err != nil {
		return nil, fmt.Errorf("caller: %w", err)
	}
	target, err := a.verifyCredential(ctx, snapshot, now, request.Target, UseServer, request.Nonce, true)
	if err != nil {
		return nil, fmt.Errorf("target: %w", err)
	}

	if err := policy.caller.match(*caller, UseClient); err != nil {
		return nil, fmt.Errorf("%w: caller policy: %w", ErrPolicyDenied, err)
	}
	if err := policy.target.match(*target, UseServer); err != nil {
		return nil, fmt.Errorf("%w: target policy: %w", ErrPolicyDenied, err)
	}
	if !policy.actionAllowed(request.Action) {
		return nil, fmt.Errorf("%w: %q", ErrActionDenied, request.Action)
	}

	return &ConnectionDecision{
		Policy:      request.Policy,
		Action:      request.Action,
		Caller:      caller,
		Target:      target,
		Generations: snapshot.generations(),
		EvaluatedAt: now,
	}, nil
}

// EvaluateConnection is an alias for AuthorizeConnection.
func (a *Authority) EvaluateConnection(ctx context.Context, request ConnectionRequest) (*ConnectionDecision, error) {
	return a.AuthorizeConnection(ctx, request)
}

// Authorize is an alias for AuthorizeConnection.
func (a *Authority) Authorize(ctx context.Context, request ConnectionRequest) (*ConnectionDecision, error) {
	return a.AuthorizeConnection(ctx, request)
}

func (a *Authority) verifyCredential(
	ctx context.Context,
	snapshot *state,
	now time.Time,
	credential PeerCredential,
	role PrincipalUse,
	expectedNonce []byte,
	bindNonce bool,
) (*WorkloadIdentity, error) {
	if credential.Leaf == nil {
		return nil, ErrNoCertificate
	}
	sanSet, id, err := parseCertificateSANs(credential.Leaf)
	if err != nil {
		return nil, err
	}
	if strings.Count(string(id.Path), "/") > a.cfg.maxPathDepth {
		return nil, fmt.Errorf("%w: path depth exceeds %d", ErrInvalidSPIFFEID, a.cfg.maxPathDepth)
	}

	if _, allowed := a.cfg.allowedTrustDomains[id.TrustDomain]; !allowed {
		return nil, fmt.Errorf("%w: %s", ErrForeignTrustDomain, id.TrustDomain)
	}
	bundle, ok := snapshot.bundles[id.TrustDomain]
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrNoTrustBundle, id.TrustDomain)
	}

	roots := x509.NewCertPool()
	for _, root := range bundle.roots {
		roots.AddCert(root)
	}
	intermediates := x509.NewCertPool()
	for i, cert := range credential.Intermediates {
		if cert == nil {
			return nil, fmt.Errorf("%w: intermediate %d is nil", ErrInvalidCertificate, i)
		}
		intermediates.AddCert(cert)
	}
	chains, err := credential.Leaf.Verify(x509.VerifyOptions{
		Roots:         roots,
		Intermediates: intermediates,
		CurrentTime:   now,
		KeyUsages:     []x509.ExtKeyUsage{x509.ExtKeyUsageAny},
	})
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidChain, err)
	}
	if len(chains) == 0 || len(chains[0]) == 0 {
		return nil, fmt.Errorf("%w: no verified chain", ErrInvalidChain)
	}
	chain := chains[0]
	if len(chain) > a.cfg.maxChainLength {
		return nil, fmt.Errorf("%w: chain length %d exceeds %d", ErrInvalidChain, len(chain), a.cfg.maxChainLength)
	}

	leaf := credential.Leaf
	if leaf.IsCA {
		return nil, fmt.Errorf("%w: workload certificate is a CA", ErrInvalidCertificate)
	}
	if leaf.KeyUsage != 0 && leaf.KeyUsage&x509.KeyUsageDigitalSignature == 0 {
		return nil, fmt.Errorf("%w: workload certificate cannot sign mTLS handshakes", ErrInvalidCertificate)
	}
	lifetime := leaf.NotAfter.Sub(leaf.NotBefore)
	if lifetime <= 0 {
		return nil, fmt.Errorf("%w: invalid certificate validity interval", ErrInvalidCertificate)
	}
	if lifetime > a.cfg.maxCertificateLifetime {
		return nil, fmt.Errorf("%w: lifetime %s exceeds %s", ErrCertificateLifetime, lifetime, a.cfg.maxCertificateLifetime)
	}
	if role != 0 && !certificateAllowsUse(leaf, role) {
		return nil, fmt.Errorf("%w: %s", ErrPrincipalUse, role)
	}

	var revocationGeneration Generation
	if revocations, ok := snapshot.revocations[id.TrustDomain]; ok {
		revocationGeneration = revocations.generation
		for _, cert := range chain {
			if revocationMatches(revocations.entries, cert) {
				return nil, fmt.Errorf("%w: serial %s", ErrRevoked, cert.SerialNumber)
			}
		}
	}

	rotationGeneration := snapshot.rotations.generation
	if record, ok := snapshot.rotations.records[id]; ok {
		if err := record.verifyCertificate(now, leaf); err != nil {
			return nil, err
		}
		rotationGeneration = record.Generation
	} else if a.cfg.requireRotationRecords {
		return nil, fmt.Errorf("%w: %s", ErrNoRotationRecord, id)
	}

	node, err := a.verifyAttestation(ctx, credential.Attestation, id, now, expectedNonce, bindNonce)
	if err != nil {
		return nil, err
	}

	return &WorkloadIdentity{
		TrustDomain:          id.TrustDomain,
		SPIFFEID:             id,
		SANs:                 sanSet,
		ChainFingerprint:     fingerprintChain(chain),
		Node:                 node.clone(),
		BundleGeneration:     bundle.generation,
		RevocationGeneration: revocationGeneration,
		RotationGeneration:   rotationGeneration,
	}, nil
}

func (a *Authority) verifyAttestation(
	ctx context.Context,
	document *NodeAttestation,
	id SPIFFEID,
	now time.Time,
	expectedNonce []byte,
	bindNonce bool,
) (NodeIdentity, error) {
	if document == nil {
		return NodeIdentity{}, ErrAttestationRequired
	}
	doc := *document

	node, err := a.cfg.attestor.VerifyAttestation(ctx, doc)
	if err != nil {
		return NodeIdentity{}, fmt.Errorf("%w: %v", ErrAttestation, err)
	}
	if node == nil {
		return NodeIdentity{}, fmt.Errorf("%w: attestor returned no node", ErrAttestation)
	}

	docTD, err := ParseTrustDomain(string(doc.TrustDomain))
	if err != nil || docTD != id.TrustDomain {
		return NodeIdentity{}, fmt.Errorf("%w: attestation trust domain %q does not match %q", ErrAttestation, doc.TrustDomain, id.TrustDomain)
	}
	if doc.NodeID == "" || node.NodeID != doc.NodeID {
		return NodeIdentity{}, fmt.Errorf("%w: node ID binding mismatch", ErrAttestation)
	}
	nodeTD, err := ParseTrustDomain(string(node.TrustDomain))
	if err != nil || nodeTD != id.TrustDomain {
		return NodeIdentity{}, fmt.Errorf("%w: attested node trust domain %q does not match %q", ErrAttestation, node.TrustDomain, id.TrustDomain)
	}
	if !doc.WorkloadID.Equal(id) {
		return NodeIdentity{}, fmt.Errorf("%w: attestation workload %q does not match %q", ErrAttestation, doc.WorkloadID, id)
	}

	if bindNonce {
		if len(expectedNonce) == 0 && !a.cfg.allowEmptyNonce {
			return NodeIdentity{}, fmt.Errorf("%w: connection nonce is required", ErrAttestation)
		}
		if subtle.ConstantTimeCompare(doc.Nonce, expectedNonce) != 1 {
			return NodeIdentity{}, fmt.Errorf("%w: attestation nonce mismatch", ErrAttestation)
		}
	} else if len(doc.Nonce) == 0 && !a.cfg.allowEmptyNonce {
		return NodeIdentity{}, fmt.Errorf("%w: attestation nonce is required", ErrAttestation)
	}

	if doc.IssuedAt.IsZero() || doc.ExpiresAt.IsZero() || !doc.ExpiresAt.After(doc.IssuedAt) {
		return NodeIdentity{}, fmt.Errorf("%w: invalid attestation validity interval", ErrAttestation)
	}
	if doc.IssuedAt.After(now.Add(a.cfg.clockSkew)) {
		return NodeIdentity{}, fmt.Errorf("%w: attestation is not yet valid", ErrAttestation)
	}
	if !now.Before(doc.ExpiresAt.Add(a.cfg.clockSkew)) {
		return NodeIdentity{}, fmt.Errorf("%w: attestation expired", ErrAttestation)
	}
	if now.Sub(doc.IssuedAt) > a.cfg.maxAttestationAge+a.cfg.clockSkew {
		return NodeIdentity{}, fmt.Errorf("%w: attestation is older than %s", ErrAttestation, a.cfg.maxAttestationAge)
	}
	if !node.ExpiresAt.IsZero() && !now.Before(node.ExpiresAt.Add(a.cfg.clockSkew)) {
		return NodeIdentity{}, fmt.Errorf("%w: attested node proof expired", ErrAttestation)
	}

	return node.clone(), nil
}

func certificateAllowsUse(cert *x509.Certificate, role PrincipalUse) bool {
	var required x509.ExtKeyUsage
	switch role {
	case UseClient:
		required = x509.ExtKeyUsageClientAuth
	case UseServer:
		required = x509.ExtKeyUsageServerAuth
	default:
		return false
	}
	for _, usage := range cert.ExtKeyUsage {
		if usage == required {
			return true
		}
	}
	return false
}

func fingerprintChain(chain []*x509.Certificate) [32]byte {
	h := sha256.New()
	writeHashString(h, "certificate-chain-v1")
	for _, cert := range chain {
		var length [8]byte
		binary.BigEndian.PutUint64(length[:], uint64(len(cert.Raw)))
		_, _ = h.Write(length[:])
		_, _ = h.Write(cert.Raw)
	}
	var out [32]byte
	copy(out[:], h.Sum(nil))
	return out
}
