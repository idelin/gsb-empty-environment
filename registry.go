package netsec

import (
	"context"
	"crypto/x509"
	"encoding/asn1"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
	"unicode"
)

// Registration binds one exact certificate fingerprint to a tenant.
//
// Certificates are never mapped by subject name alone: every certificate must
// have a registration whose DER fingerprint matches. PolicyOIDs, when set,
// records additional policy OIDs that this particular certificate must carry.
type Registration struct {
	Tenant      string
	Certificate *x509.Certificate
	PolicyOIDs  []asn1.ObjectIdentifier
}

// RevocationChecker checks a certificate's revocation status. A nil error
// means the certificate is currently acceptable. Every non-nil error is
// treated as a rejection.
//
// The checker is called for each non-root certificate in the verified path,
// with the certificate and its direct issuer.
type RevocationChecker interface {
	CheckRevocation(ctx context.Context, cert, issuer *x509.Certificate) error
}

// RevocationFunc adapts a function to RevocationChecker.
type RevocationFunc func(ctx context.Context, cert, issuer *x509.Certificate) error

func (f RevocationFunc) CheckRevocation(ctx context.Context, cert, issuer *x509.Certificate) error {
	if f == nil {
		return ErrRevocationUnavailable
	}
	return f(ctx, cert, issuer)
}

// RevocationSet is a simple in-memory revocation checker keyed by certificate
// fingerprint. Certificates absent from the set are treated as not revoked.
type RevocationSet struct {
	mu      sync.RWMutex
	revoked map[Fingerprint]struct{}
}

// NewRevocationSet creates a revocation set containing the supplied
// certificates.
func NewRevocationSet(certs ...*x509.Certificate) *RevocationSet {
	s := &RevocationSet{revoked: make(map[Fingerprint]struct{})}
	for _, cert := range certs {
		s.Revoke(cert)
	}
	return s
}

// Revoke marks cert as revoked.
func (s *RevocationSet) Revoke(cert *x509.Certificate) {
	if s == nil || cert == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.revoked == nil {
		s.revoked = make(map[Fingerprint]struct{})
	}
	s.revoked[FingerprintOf(cert)] = struct{}{}
}

// Unrevoke removes cert from the revoked set.
func (s *RevocationSet) Unrevoke(cert *x509.Certificate) {
	if s == nil || cert == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.revoked, FingerprintOf(cert))
}

// IsRevoked reports whether cert is in the set.
func (s *RevocationSet) IsRevoked(cert *x509.Certificate) bool {
	if s == nil || cert == nil {
		return false
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	_, ok := s.revoked[FingerprintOf(cert)]
	return ok
}

func (s *RevocationSet) CheckRevocation(_ context.Context, cert, _ *x509.Certificate) error {
	if s == nil {
		return ErrRevocationUnavailable
	}
	if s.IsRevoked(cert) {
		return ErrRevoked
	}
	return nil
}

// Options configures certificate verification and subject extraction.
type Options struct {
	// Roots is the explicit trust anchor pool. It is required; the registry
	// never silently uses the operating system trust store.
	Roots *x509.CertPool

	// Intermediates is an optional pool of issuer certificates that may be
	// used in addition to intermediates supplied at resolution time.
	Intermediates *x509.CertPool

	// Revocation is required at resolution time. If it is nil, resolution
	// fails closed with ErrRevocationUnavailable.
	Revocation RevocationChecker

	// RequiredPolicyOIDs must all be present in the leaf certificate.
	RequiredPolicyOIDs []asn1.ObjectIdentifier

	// AllowedPolicyOIDs, when non-empty, requires at least one of these OIDs
	// to be present in the leaf certificate.
	AllowedPolicyOIDs []asn1.ObjectIdentifier

	// UniqueIdentifierOIDs adds subject attribute OIDs that should be treated
	// as unique identifiers in addition to serialNumber, organizationIdentifier
	// and UID.
	UniqueIdentifierOIDs []asn1.ObjectIdentifier

	// KeyUsages is passed to x509.VerifyOptions. A nil slice defaults to
	// clientAuth. A non-nil empty slice requests any extended key usage.
	KeyUsages []x509.ExtKeyUsage

	// CurrentTime supplies verification time. It defaults to time.Now.
	CurrentTime func() time.Time

	// MaxChainDepth limits the number of certificates in a verified path.
	// Values <= 0 default to 8.
	MaxChainDepth int
}

type verifierConfig struct {
	roots            *x509.CertPool
	intermediates    *x509.CertPool
	revocation       RevocationChecker
	requiredPolicies []asn1.ObjectIdentifier
	allowedPolicies  []asn1.ObjectIdentifier
	uniqueIDOIDs     []asn1.ObjectIdentifier
	keyUsages        []x509.ExtKeyUsage
	now              func() time.Time
	maxChainDepth    int
}

// Registry publishes immutable mapping snapshots. Mutations are serialized
// and atomically install a new generation; a resolution always uses exactly
// one generation.
type Registry struct {
	mu       sync.RWMutex
	snapshot *Snapshot
	config   *verifierConfig
}

// Mapper is an alias for Registry.
type Mapper = Registry

// SubjectMapper is an alias for Registry.
type SubjectMapper = Registry

type entry struct {
	tenant           string
	fingerprint      Fingerprint
	cert             *x509.Certificate
	identity         certIdentity
	requiredPolicies []asn1.ObjectIdentifier
}

// Snapshot is an immutable generation of registrations.
type Snapshot struct {
	generation  uint64
	entries     map[Fingerprint]*entry
	strongIndex map[string]map[string]struct{}
	cnIndex     map[string]map[string]struct{}
	config      *verifierConfig
}

// Mapping is the unambiguous result of resolving a certificate.
type Mapping struct {
	Tenant            string
	Generation        uint64
	Fingerprint       Fingerprint
	IssuerFingerprint Fingerprint
	Source            IdentitySource
	// Identity is the readable claim used for the mapping, for example
	// "2.5.4.5=uid-123", "dns=client.example", or "cn=legacy-client".
	Identity string
}

// NewRegistry creates an empty registry at generation zero.
func NewRegistry(opts Options) (*Registry, error) {
	if opts.Roots == nil {
		return nil, ErrTrustRootsRequired
	}
	intermediates := x509.NewCertPool()
	if opts.Intermediates != nil {
		intermediates = opts.Intermediates.Clone()
	}
	keyUsages := opts.KeyUsages
	if keyUsages == nil {
		keyUsages = []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}
	}
	now := opts.CurrentTime
	if now == nil {
		now = time.Now
	}
	maxDepth := opts.MaxChainDepth
	if maxDepth <= 0 {
		maxDepth = 8
	}
	cfg := &verifierConfig{
		roots:            opts.Roots.Clone(),
		intermediates:    intermediates,
		revocation:       opts.Revocation,
		requiredPolicies: cloneOIDs(opts.RequiredPolicyOIDs),
		allowedPolicies:  cloneOIDs(opts.AllowedPolicyOIDs),
		uniqueIDOIDs:     cloneOIDs(opts.UniqueIdentifierOIDs),
		keyUsages:        append([]x509.ExtKeyUsage(nil), keyUsages...),
		now:              now,
		maxChainDepth:    maxDepth,
	}
	r := &Registry{config: cfg}
	r.snapshot = buildSnapshot(0, map[Fingerprint]*entry{}, cfg)
	return r, nil
}

// New is an alias for NewRegistry.
func New(opts Options) (*Registry, error) {
	return NewRegistry(opts)
}

// NewMapper is an alias for NewRegistry.
func NewMapper(opts Options) (*Mapper, error) {
	return NewRegistry(opts)
}

// NewSubjectMapper is an alias for NewRegistry.
func NewSubjectMapper(opts Options) (*SubjectMapper, error) {
	return NewRegistry(opts)
}

// MustNewRegistry is like NewRegistry but panics on configuration errors. It
// is intended for package initialization in applications where configuration
// errors are fatal.
func MustNewRegistry(opts Options) *Registry {
	r, err := NewRegistry(opts)
	if err != nil {
		panic(err)
	}
	return r
}

// Generation returns the active mapping generation.
func (r *Registry) Generation() uint64 {
	if r == nil {
		return 0
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	if r.snapshot == nil {
		return 0
	}
	return r.snapshot.generation
}

// Snapshot returns the active immutable snapshot.
func (r *Registry) Snapshot() *Snapshot {
	if r == nil {
		return nil
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.snapshot
}

// Len returns the number of registered certificate fingerprints in the active
// generation.
func (r *Registry) Len() int {
	s := r.Snapshot()
	if s == nil {
		return 0
	}
	return len(s.entries)
}

func (r *Registry) newEntry(reg Registration) (*entry, error) {
	if r == nil || r.config == nil {
		return nil, ErrInvalidCertificate
	}
	if !validTenant(reg.Tenant) {
		return nil, ErrInvalidTenant
	}
	if reg.Certificate == nil || len(reg.Certificate.Raw) == 0 {
		return nil, ErrCertificateRequired
	}

	der := append([]byte(nil), reg.Certificate.Raw...)
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrInvalidCertificate, err)
	}
	identity, err := extractIdentity(cert, r.config.uniqueIDOIDs)
	if err != nil {
		return nil, err
	}
	return &entry{
		tenant:           reg.Tenant,
		fingerprint:      FingerprintOfDER(der),
		cert:             cert,
		identity:         identity,
		requiredPolicies: cloneOIDs(reg.PolicyOIDs),
	}, nil
}

// Register atomically adds or idempotently confirms one registration.
func (r *Registry) Register(reg Registration) (uint64, error) {
	e, err := r.newEntry(reg)
	if err != nil {
		return r.Generation(), err
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	if old, ok := r.snapshot.entries[e.fingerprint]; ok {
		if old.tenant != e.tenant {
			return r.snapshot.generation, fmt.Errorf("%w: %s", ErrFingerprintConflict, e.fingerprint)
		}
		if !sameOIDSet(old.requiredPolicies, e.requiredPolicies) {
			return r.snapshot.generation, fmt.Errorf("%w: %s", ErrRegistrationConflict, e.fingerprint)
		}
		return r.snapshot.generation, nil
	}

	entries := cloneEntries(r.snapshot.entries)
	entries[e.fingerprint] = e
	return r.commitLocked(entries), nil
}

// Add registers one certificate for tenant. It is a convenience wrapper around
// Register.
func (r *Registry) Add(tenant string, cert *x509.Certificate) (uint64, error) {
	return r.Register(Registration{Tenant: tenant, Certificate: cert})
}

// RegisterCertificate is an alias for Add.
func (r *Registry) RegisterCertificate(tenant string, cert *x509.Certificate) (uint64, error) {
	return r.Add(tenant, cert)
}

// RegisterAll atomically adds several certificates for the same tenant. It is
// useful when a tenant has multiple concurrently valid certificates.
func (r *Registry) RegisterAll(tenant string, certs ...*x509.Certificate) (uint64, error) {
	regs := make([]Registration, 0, len(certs))
	for _, cert := range certs {
		regs = append(regs, Registration{Tenant: tenant, Certificate: cert})
	}
	return r.registerBatch(regs, false)
}

// Replace atomically replaces the entire mapping with regs.
func (r *Registry) Replace(regs []Registration) (uint64, error) {
	return r.registerBatch(regs, true)
}

func (r *Registry) registerBatch(regs []Registration, replace bool) (uint64, error) {
	if len(regs) == 0 && !replace {
		return r.Generation(), nil
	}
	next := make(map[Fingerprint]*entry, len(regs))
	for _, reg := range regs {
		e, err := r.newEntry(reg)
		if err != nil {
			return r.Generation(), err
		}
		if old, ok := next[e.fingerprint]; ok {
			if old.tenant != e.tenant {
				return r.Generation(), fmt.Errorf("%w: %s", ErrFingerprintConflict, e.fingerprint)
			}
			if !sameOIDSet(old.requiredPolicies, e.requiredPolicies) {
				return r.Generation(), fmt.Errorf("%w: %s", ErrRegistrationConflict, e.fingerprint)
			}
			continue
		}
		next[e.fingerprint] = e
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	if replace {
		return r.commitLocked(next), nil
	}
	entries := cloneEntries(r.snapshot.entries)
	changed := false
	for fp, e := range next {
		if old, ok := entries[fp]; ok {
			if old.tenant != e.tenant {
				return r.snapshot.generation, fmt.Errorf("%w: %s", ErrFingerprintConflict, fp)
			}
			if !sameOIDSet(old.requiredPolicies, e.requiredPolicies) {
				return r.snapshot.generation, fmt.Errorf("%w: %s", ErrRegistrationConflict, fp)
			}
			continue
		}
		entries[fp] = e
		changed = true
	}
	if !changed {
		return r.snapshot.generation, nil
	}
	return r.commitLocked(entries), nil
}

// Rotate atomically replaces all certificates registered to tenant with certs.
// Other tenants are unchanged. The old certificates stop resolving as soon as
// the new generation is published.
func (r *Registry) Rotate(tenant string, certs ...*x509.Certificate) (uint64, error) {
	if !validTenant(tenant) {
		return r.Generation(), ErrInvalidTenant
	}
	if len(certs) == 0 {
		return r.Generation(), fmt.Errorf("%w: rotation requires at least one certificate", ErrCertificateRequired)
	}
	next := make(map[Fingerprint]*entry, len(certs))
	for _, cert := range certs {
		e, err := r.newEntry(Registration{Tenant: tenant, Certificate: cert})
		if err != nil {
			return r.Generation(), err
		}
		if _, ok := next[e.fingerprint]; ok {
			continue
		}
		next[e.fingerprint] = e
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	entries := cloneEntries(r.snapshot.entries)
	for fp, e := range entries {
		if e.tenant == tenant {
			delete(entries, fp)
		}
	}
	for fp, e := range next {
		if old, ok := entries[fp]; ok && old.tenant != tenant {
			return r.snapshot.generation, fmt.Errorf("%w: %s", ErrFingerprintConflict, fp)
		}
		entries[fp] = e
	}
	return r.commitLocked(entries), nil
}

// RotateCertificates is an alias for Rotate.
func (r *Registry) RotateCertificates(tenant string, certs ...*x509.Certificate) (uint64, error) {
	return r.Rotate(tenant, certs...)
}

// RemoveTenant atomically removes all certificates registered to tenant.
func (r *Registry) RemoveTenant(tenant string) (uint64, error) {
	if r == nil {
		return 0, ErrInvalidCertificate
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.snapshot == nil {
		return 0, ErrInvalidCertificate
	}
	entries := cloneEntries(r.snapshot.entries)
	changed := false
	for fp, e := range entries {
		if e.tenant == tenant {
			delete(entries, fp)
			changed = true
		}
	}
	if !changed {
		return r.snapshot.generation, nil
	}
	return r.commitLocked(entries), nil
}

// RemoveCertificate atomically removes one certificate fingerprint.
func (r *Registry) RemoveCertificate(fp Fingerprint) (uint64, error) {
	if r == nil {
		return 0, ErrInvalidCertificate
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.snapshot == nil {
		return 0, ErrInvalidCertificate
	}
	if _, ok := r.snapshot.entries[fp]; !ok {
		return r.snapshot.generation, nil
	}
	entries := cloneEntries(r.snapshot.entries)
	delete(entries, fp)
	return r.commitLocked(entries), nil
}

func (r *Registry) commitLocked(entries map[Fingerprint]*entry) uint64 {
	generation := r.snapshot.generation + 1
	r.snapshot = buildSnapshot(generation, entries, r.config)
	return generation
}

func cloneEntries(src map[Fingerprint]*entry) map[Fingerprint]*entry {
	dst := make(map[Fingerprint]*entry, len(src))
	for fp, e := range src {
		dst[fp] = e
	}
	return dst
}

func buildSnapshot(generation uint64, entries map[Fingerprint]*entry, cfg *verifierConfig) *Snapshot {
	s := &Snapshot{
		generation:  generation,
		entries:     make(map[Fingerprint]*entry, len(entries)),
		strongIndex: make(map[string]map[string]struct{}),
		cnIndex:     make(map[string]map[string]struct{}),
		config:      cfg,
	}
	for fp, e := range entries {
		s.entries[fp] = e
		for _, claim := range e.identity.strongClaims() {
			addTenant(s.strongIndex, claim.key, e.tenant)
		}
		if e.identity.cn != "" {
			addTenant(s.cnIndex, e.identity.cn, e.tenant)
		}
	}
	return s
}

func addTenant(index map[string]map[string]struct{}, key, tenant string) {
	tenants := index[key]
	if tenants == nil {
		tenants = make(map[string]struct{})
		index[key] = tenants
	}
	tenants[tenant] = struct{}{}
}

// Generation returns the snapshot generation.
func (s *Snapshot) Generation() uint64 {
	if s == nil {
		return 0
	}
	return s.generation
}

// Resolve uses the snapshot's fixed generation to resolve cert.
func (r *Registry) Resolve(ctx context.Context, cert *x509.Certificate, intermediates ...*x509.Certificate) (Mapping, error) {
	s := r.Snapshot()
	if s == nil {
		return Mapping{}, ErrInvalidCertificate
	}
	return s.Resolve(ctx, cert, intermediates...)
}

// Map is an alias for Resolve.
func (r *Registry) Map(ctx context.Context, cert *x509.Certificate, intermediates ...*x509.Certificate) (Mapping, error) {
	return r.Resolve(ctx, cert, intermediates...)
}

// MapCertificate resolves cert with a background context.
func (r *Registry) MapCertificate(cert *x509.Certificate, intermediates ...*x509.Certificate) (Mapping, error) {
	return r.Resolve(context.Background(), cert, intermediates...)
}

// ResolveCertificate resolves cert with a background context.
func (r *Registry) ResolveCertificate(cert *x509.Certificate, intermediates ...*x509.Certificate) (Mapping, error) {
	return r.Resolve(context.Background(), cert, intermediates...)
}

// ResolveDER parses a DER-encoded leaf certificate and resolves it.
func (r *Registry) ResolveDER(ctx context.Context, der []byte, intermediates ...*x509.Certificate) (Mapping, error) {
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		return Mapping{}, fmt.Errorf("%w: %w", ErrInvalidCertificate, err)
	}
	return r.Resolve(ctx, cert, intermediates...)
}

// Resolve resolves cert against this immutable snapshot.
func (s *Snapshot) Resolve(ctx context.Context, cert *x509.Certificate, intermediates ...*x509.Certificate) (Mapping, error) {
	if s == nil || s.config == nil {
		return Mapping{}, ErrInvalidCertificate
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if cert == nil || len(cert.Raw) == 0 {
		return Mapping{}, ErrCertificateRequired
	}
	fp := FingerprintOf(cert)
	e, ok := s.entries[fp]
	if !ok {
		return Mapping{}, fmt.Errorf("%w: %s", ErrUnregisteredCertificate, fp)
	}

	chain, err := s.config.verify(e.cert, intermediates)
	if err != nil {
		return Mapping{}, err
	}
	if err := s.checkPolicies(e); err != nil {
		return Mapping{}, err
	}
	if err := s.checkRevocation(ctx, chain); err != nil {
		return Mapping{}, err
	}

	source, identity, err := s.selectedIdentity(e)
	if err != nil {
		return Mapping{}, err
	}
	return Mapping{
		Tenant:            e.tenant,
		Generation:        s.generation,
		Fingerprint:       fp,
		IssuerFingerprint: FingerprintOf(chain[1]),
		Source:            source,
		Identity:          identity,
	}, nil
}

func (c *verifierConfig) verify(leaf *x509.Certificate, supplied []*x509.Certificate) ([]*x509.Certificate, error) {
	intermediates := c.intermediates.Clone()
	for _, cert := range supplied {
		if cert != nil && !cert.Equal(leaf) {
			intermediates.AddCert(cert)
		}
	}
	chains, err := leaf.Verify(x509.VerifyOptions{
		Roots:         c.roots,
		Intermediates: intermediates,
		CurrentTime:   c.now(),
		KeyUsages:     append([]x509.ExtKeyUsage(nil), c.keyUsages...),
	})
	if err != nil {
		var invalid x509.CertificateInvalidError
		if errors.As(err, &invalid) && invalid.Reason == x509.Expired {
			return nil, fmt.Errorf("%w: %w", ErrCertificateExpired, err)
		}
		var unknown x509.UnknownAuthorityError
		if errors.As(err, &unknown) {
			return nil, fmt.Errorf("%w: %w", ErrUntrustedIssuer, err)
		}
		return nil, fmt.Errorf("%w: %w", ErrCertificateVerification, err)
	}
	if len(chains) == 0 {
		return nil, fmt.Errorf("%w: no verified chain", ErrUntrustedIssuer)
	}
	chain := chains[0]
	if len(chain) < 2 {
		return nil, fmt.Errorf("%w: a client certificate must be issued by a configured CA", ErrUntrustedIssuer)
	}
	if len(chain) > c.maxChainDepth {
		return nil, fmt.Errorf("%w: chain depth %d exceeds limit %d", ErrCertificateVerification, len(chain), c.maxChainDepth)
	}
	if leaf.IsCA {
		return nil, fmt.Errorf("%w: CA certificate cannot be used as a client identity", ErrInvalidCertificate)
	}
	if leaf.KeyUsage != 0 && leaf.KeyUsage&x509.KeyUsageDigitalSignature == 0 {
		return nil, fmt.Errorf("%w: certificate lacks digitalSignature key usage", ErrInvalidCertificate)
	}
	return chain, nil
}

func (s *Snapshot) checkPolicies(e *entry) error {
	for _, oid := range s.config.requiredPolicies {
		if !certificateHasPolicy(e.cert, oid) {
			return fmt.Errorf("%w: %s", ErrPolicyMissing, oid)
		}
	}
	for _, oid := range e.requiredPolicies {
		if !certificateHasPolicy(e.cert, oid) {
			return fmt.Errorf("%w: %s", ErrPolicyMissing, oid)
		}
	}
	if len(s.config.allowedPolicies) > 0 {
		allowed := false
		for _, oid := range s.config.allowedPolicies {
			if certificateHasPolicy(e.cert, oid) {
				allowed = true
				break
			}
		}
		if !allowed {
			return ErrPolicyNotAllowed
		}
	}
	return nil
}

func (s *Snapshot) checkRevocation(ctx context.Context, chain []*x509.Certificate) error {
	if s.config.revocation == nil {
		return ErrRevocationUnavailable
	}
	for i := 0; i < len(chain)-1; i++ {
		if err := s.config.revocation.CheckRevocation(ctx, chain[i], chain[i+1]); err != nil {
			if errors.Is(err, ErrRevoked) {
				return fmt.Errorf("%w: %s", ErrRevoked, FingerprintOf(chain[i]))
			}
			if errors.Is(err, ErrRevocationUnavailable) {
				return ErrRevocationUnavailable
			}
			return fmt.Errorf("%w: %w", ErrRevocationCheckFailed, err)
		}
	}
	return nil
}

func (s *Snapshot) selectedIdentity(e *entry) (IdentitySource, string, error) {
	id := e.identity
	if len(id.uniqueIDs) > 0 || len(id.sans) > 0 {
		for _, claim := range id.strongClaims() {
			tenants := s.strongIndex[claim.key]
			for tenant := range tenants {
				if tenant != e.tenant {
					return "", "", fmt.Errorf("%w: %s is registered to both %q and %q", ErrAmbiguousIdentity, claim.label(), e.tenant, tenant)
				}
			}
		}
		if len(id.uniqueIDs) > 0 {
			return IdentityByUniqueID, id.uniqueIDs[0].label(), nil
		}
		return IdentityBySAN, id.sans[0].label(), nil
	}

	if id.hasSANExt {
		return "", "", ErrUnsupportedSAN
	}
	tenants := s.cnIndex[id.cn]
	if len(tenants) > 1 {
		return "", "", fmt.Errorf("%w: %q", ErrCNNotUnique, id.cn)
	}
	if _, ok := tenants[e.tenant]; !ok || len(tenants) == 0 {
		return "", "", fmt.Errorf("%w: %q", ErrCNNotUnique, id.cn)
	}
	return IdentityByCN, "cn=" + id.cn, nil
}

func certificateHasPolicy(cert *x509.Certificate, want asn1.ObjectIdentifier) bool {
	if cert == nil {
		return false
	}
	for _, got := range cert.PolicyIdentifiers {
		if got.Equal(want) {
			return true
		}
	}
	for _, got := range cert.Policies {
		if got.EqualASN1OID(want) {
			return true
		}
	}
	return false
}

func cloneOIDs(src []asn1.ObjectIdentifier) []asn1.ObjectIdentifier {
	if len(src) == 0 {
		return nil
	}
	dst := make([]asn1.ObjectIdentifier, len(src))
	for i, oid := range src {
		dst[i] = append(asn1.ObjectIdentifier(nil), oid...)
	}
	return dst
}

func sameOIDSet(a, b []asn1.ObjectIdentifier) bool {
	setA := make(map[string]struct{}, len(a))
	setB := make(map[string]struct{}, len(b))
	for _, oid := range a {
		setA[oid.String()] = struct{}{}
	}
	for _, oid := range b {
		setB[oid.String()] = struct{}{}
	}
	if len(setA) != len(setB) {
		return false
	}
	for oid := range setA {
		if _, ok := setB[oid]; !ok {
			return false
		}
	}
	return true
}

func validTenant(tenant string) bool {
	if tenant == "" || strings.TrimSpace(tenant) != tenant || len(tenant) > 256 {
		return false
	}
	return strings.IndexFunc(tenant, unicode.IsControl) < 0
}
