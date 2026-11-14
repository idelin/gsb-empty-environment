package netsec

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/asn1"
	"errors"
	"math/big"
	"net/url"
	"sync/atomic"
	"testing"
	"time"
)

var (
	testSerial atomic.Int64
	testNow    = time.Date(2026, 11, 14, 12, 0, 0, 0, time.UTC)
)

type testCA struct {
	cert *x509.Certificate
	key  *ecdsa.PrivateKey
}

func newTestCA(t *testing.T, commonName string) *testCA {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate CA key: %v", err)
	}
	template := &x509.Certificate{
		SerialNumber:          big.NewInt(testSerial.Add(1)),
		Subject:               pkix.Name{CommonName: commonName, Organization: []string{"Example PKI"}},
		NotBefore:             testNow.Add(-time.Hour),
		NotAfter:              testNow.Add(24 * time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign | x509.KeyUsageDigitalSignature,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("create CA certificate: %v", err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatalf("parse CA certificate: %v", err)
	}
	return &testCA{cert: cert, key: key}
}

type leafOptions struct {
	commonName   string
	serialNumber string
	extraNames   []pkix.AttributeTypeAndValue
	dnsNames     []string
	emails       []string
	uris         []string
	policies     []asn1.ObjectIdentifier
	notBefore    time.Time
	notAfter     time.Time
}

func (ca *testCA) issue(t *testing.T, opts leafOptions) *x509.Certificate {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate leaf key: %v", err)
	}
	notBefore := opts.notBefore
	if notBefore.IsZero() {
		notBefore = testNow.Add(-time.Hour)
	}
	notAfter := opts.notAfter
	if notAfter.IsZero() {
		notAfter = testNow.Add(24 * time.Hour)
	}
	var uris []*url.URL
	for _, raw := range opts.uris {
		u, err := url.Parse(raw)
		if err != nil {
			t.Fatalf("parse URI %q: %v", raw, err)
		}
		uris = append(uris, u)
	}
	var policies []x509.OID
	for _, oid := range opts.policies {
		components := make([]uint64, len(oid))
		for i, component := range oid {
			components[i] = uint64(component)
		}
		policy, err := x509.OIDFromInts(components)
		if err != nil {
			t.Fatalf("convert policy OID %s: %v", oid, err)
		}
		policies = append(policies, policy)
	}
	template := &x509.Certificate{
		SerialNumber:   big.NewInt(testSerial.Add(1)),
		Subject:        pkix.Name{CommonName: opts.commonName, SerialNumber: opts.serialNumber, ExtraNames: opts.extraNames},
		NotBefore:      notBefore,
		NotAfter:       notAfter,
		KeyUsage:       x509.KeyUsageDigitalSignature,
		ExtKeyUsage:    []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
		DNSNames:       opts.dnsNames,
		EmailAddresses: opts.emails,
		URIs:           uris,
		Policies:       policies,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, ca.cert, &key.PublicKey, ca.key)
	if err != nil {
		t.Fatalf("create leaf certificate: %v", err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatalf("parse leaf certificate: %v", err)
	}
	return cert
}

func testRegistry(t *testing.T, ca *testCA, opts Options) *Registry {
	t.Helper()
	roots := x509.NewCertPool()
	roots.AddCert(ca.cert)
	opts.Roots = roots
	opts.CurrentTime = func() time.Time { return testNow }
	if opts.Revocation == nil {
		opts.Revocation = NewRevocationSet()
	}
	r, err := NewRegistry(opts)
	if err != nil {
		t.Fatalf("new registry: %v", err)
	}
	return r
}

func mustRegister(t *testing.T, r *Registry, tenant string, cert *x509.Certificate) {
	t.Helper()
	if _, err := r.Add(tenant, cert); err != nil {
		t.Fatalf("register %s: %v", tenant, err)
	}
}

func mustResolve(t *testing.T, r *Registry, cert *x509.Certificate) Mapping {
	t.Helper()
	m, err := r.Resolve(context.Background(), cert)
	if err != nil {
		t.Fatalf("resolve certificate: %v", err)
	}
	return m
}

func mustResolveSnapshot(t *testing.T, s *Snapshot, cert *x509.Certificate) Mapping {
	t.Helper()
	m, err := s.Resolve(context.Background(), cert)
	if err != nil {
		t.Fatalf("resolve certificate against snapshot: %v", err)
	}
	return m
}

func requireError(t *testing.T, err, target error) {
	t.Helper()
	if !errors.Is(err, target) {
		t.Fatalf("error = %v, want errors.Is(%v)", err, target)
	}
}

func TestSameCNUsesUniqueIdentifierAndSAN(t *testing.T) {
	ca := newTestCA(t, "Trusted Root")
	policy := asn1.ObjectIdentifier{1, 3, 6, 1, 4, 1, 55555, 1}
	victim := ca.issue(t, leafOptions{
		commonName:   "shared-gateway",
		serialNumber: "uid-victim",
		dnsNames:     []string{"victim.example"},
		policies:     []asn1.ObjectIdentifier{policy},
	})
	other := ca.issue(t, leafOptions{
		commonName:   "shared-gateway",
		serialNumber: "uid-other",
		dnsNames:     []string{"other.example"},
		policies:     []asn1.ObjectIdentifier{policy},
	})
	r := testRegistry(t, ca, Options{RequiredPolicyOIDs: []asn1.ObjectIdentifier{policy}})
	mustRegister(t, r, "victim", victim)
	mustRegister(t, r, "other", other)

	victimMapping := mustResolve(t, r, victim)
	if victimMapping.Tenant != "victim" {
		t.Fatalf("victim mapped to %q", victimMapping.Tenant)
	}
	if victimMapping.Source != IdentityByUniqueID {
		t.Fatalf("victim source = %q, want %q", victimMapping.Source, IdentityByUniqueID)
	}
	otherMapping := mustResolve(t, r, other)
	if otherMapping.Tenant != "other" {
		t.Fatalf("other mapped to %q", otherMapping.Tenant)
	}
}

func TestSANPrecedesCN(t *testing.T) {
	ca := newTestCA(t, "Trusted Root")
	a := ca.issue(t, leafOptions{
		commonName: "client",
		dnsNames:   []string{"a.example"},
	})
	b := ca.issue(t, leafOptions{
		commonName: "client",
		dnsNames:   []string{"b.example"},
	})
	r := testRegistry(t, ca, Options{})
	mustRegister(t, r, "tenant-a", a)
	mustRegister(t, r, "tenant-b", b)

	aMapping := mustResolve(t, r, a)
	if aMapping.Tenant != "tenant-a" || aMapping.Source != IdentityBySAN {
		t.Fatalf("a mapping = %+v", aMapping)
	}
	bMapping := mustResolve(t, r, b)
	if bMapping.Tenant != "tenant-b" || bMapping.Source != IdentityBySAN {
		t.Fatalf("b mapping = %+v", bMapping)
	}
}

func TestCustomUniqueIdentifierOID(t *testing.T) {
	ca := newTestCA(t, "Trusted Root")
	customOID := asn1.ObjectIdentifier{1, 3, 6, 1, 4, 1, 55555, 42}
	cert := ca.issue(t, leafOptions{
		commonName: "shared-cn",
		extraNames: []pkix.AttributeTypeAndValue{
			{Type: customOID, Value: "custom-subject-1"},
		},
	})
	r := testRegistry(t, ca, Options{UniqueIdentifierOIDs: []asn1.ObjectIdentifier{customOID}})
	mustRegister(t, r, "tenant", cert)

	mapping := mustResolve(t, r, cert)
	if mapping.Tenant != "tenant" || mapping.Source != IdentityByUniqueID {
		t.Fatalf("custom unique identifier mapping = %+v", mapping)
	}
	if mapping.Identity != customOID.String()+"=custom-subject-1" {
		t.Fatalf("custom identity = %q", mapping.Identity)
	}
}

func TestCNFallbackRequiresUniqueRegistration(t *testing.T) {
	ca := newTestCA(t, "Trusted Root")
	a := ca.issue(t, leafOptions{commonName: "same-cn"})
	b := ca.issue(t, leafOptions{commonName: "same-cn"})

	unique := testRegistry(t, ca, Options{})
	mustRegister(t, unique, "tenant-a", a)
	mapping := mustResolve(t, unique, a)
	if mapping.Tenant != "tenant-a" || mapping.Source != IdentityByCN {
		t.Fatalf("unique CN mapping = %+v", mapping)
	}

	ambiguous := testRegistry(t, ca, Options{})
	mustRegister(t, ambiguous, "tenant-a", a)
	mustRegister(t, ambiguous, "tenant-b", b)
	_, err := ambiguous.Resolve(context.Background(), a)
	requireError(t, err, ErrCNNotUnique)
	_, err = ambiguous.Resolve(context.Background(), b)
	requireError(t, err, ErrCNNotUnique)
}

func TestUnregisteredCertificateIsRejected(t *testing.T) {
	ca := newTestCA(t, "Trusted Root")
	victim := ca.issue(t, leafOptions{
		commonName:   "tenant-client",
		serialNumber: "victim-uid",
		dnsNames:     []string{"victim.example"},
	})
	attacker := ca.issue(t, leafOptions{
		commonName:   "tenant-client",
		serialNumber: "attacker-uid",
		dnsNames:     []string{"attacker.example"},
	})
	r := testRegistry(t, ca, Options{})
	mustRegister(t, r, "victim", victim)

	_, err := r.Resolve(context.Background(), attacker)
	requireError(t, err, ErrUnregisteredCertificate)
}

func TestForgedIssuerIsRejected(t *testing.T) {
	trusted := newTestCA(t, "Example Trusted Root")
	rogue := newTestCA(t, "Example Trusted Root")
	victim := trusted.issue(t, leafOptions{
		commonName:   "shared",
		serialNumber: "shared-uid",
		dnsNames:     []string{"shared.example"},
	})
	forged := rogue.issue(t, leafOptions{
		commonName:   "shared",
		serialNumber: "shared-uid",
		dnsNames:     []string{"shared.example"},
	})
	r := testRegistry(t, trusted, Options{})
	mustRegister(t, r, "victim", victim)
	mustRegister(t, r, "attacker", forged)

	_, err := r.Resolve(context.Background(), forged)
	requireError(t, err, ErrUntrustedIssuer)
}

func TestPolicyOIDIsChecked(t *testing.T) {
	ca := newTestCA(t, "Trusted Root")
	required := asn1.ObjectIdentifier{1, 3, 6, 1, 4, 1, 55555, 7}
	wrong := asn1.ObjectIdentifier{1, 3, 6, 1, 4, 1, 55555, 8}
	cert := ca.issue(t, leafOptions{
		commonName: "policy-client",
		dnsNames:   []string{"policy.example"},
		policies:   []asn1.ObjectIdentifier{wrong},
	})
	r := testRegistry(t, ca, Options{RequiredPolicyOIDs: []asn1.ObjectIdentifier{required}})
	mustRegister(t, r, "tenant", cert)

	_, err := r.Resolve(context.Background(), cert)
	requireError(t, err, ErrPolicyMissing)
}

func TestPolicyAllowListAndPerRegistrationPolicy(t *testing.T) {
	ca := newTestCA(t, "Trusted Root")
	allowed := asn1.ObjectIdentifier{1, 3, 6, 1, 4, 1, 55555, 9}
	other := asn1.ObjectIdentifier{1, 3, 6, 1, 4, 1, 55555, 10}
	cert := ca.issue(t, leafOptions{
		commonName: "policy-client",
		dnsNames:   []string{"policy.example"},
		policies:   []asn1.ObjectIdentifier{allowed},
	})

	allowListed := testRegistry(t, ca, Options{AllowedPolicyOIDs: []asn1.ObjectIdentifier{allowed}})
	mustRegister(t, allowListed, "tenant", cert)
	if _, err := allowListed.Resolve(context.Background(), cert); err != nil {
		t.Fatalf("allowed policy rejected: %v", err)
	}
	if _, err := allowListed.Register(Registration{
		Tenant:      "tenant",
		Certificate: cert,
		PolicyOIDs:  []asn1.ObjectIdentifier{other},
	}); !errors.Is(err, ErrRegistrationConflict) {
		t.Fatalf("conflicting registration metadata = %v", err)
	}

	notAllowed := testRegistry(t, ca, Options{AllowedPolicyOIDs: []asn1.ObjectIdentifier{other}})
	mustRegister(t, notAllowed, "tenant", cert)
	_, err := notAllowed.Resolve(context.Background(), cert)
	requireError(t, err, ErrPolicyNotAllowed)

	perRegistrationRegistry := testRegistry(t, ca, Options{})
	if _, err := perRegistrationRegistry.Register(Registration{
		Tenant:      "tenant",
		Certificate: cert,
		PolicyOIDs:  []asn1.ObjectIdentifier{other},
	}); err != nil {
		t.Fatalf("register with per-certificate policy: %v", err)
	}
	_, err = perRegistrationRegistry.Resolve(context.Background(), cert)
	if !errors.Is(err, ErrPolicyMissing) {
		t.Fatalf("per-registration policy result = %v", err)
	}
}

func TestExpiredCertificateIsRejected(t *testing.T) {
	ca := newTestCA(t, "Trusted Root")
	cert := ca.issue(t, leafOptions{
		commonName: "expired-client",
		dnsNames:   []string{"expired.example"},
		notBefore:  testNow.Add(-2 * time.Hour),
		notAfter:   testNow.Add(-time.Hour),
	})
	r := testRegistry(t, ca, Options{})
	mustRegister(t, r, "tenant", cert)

	_, err := r.Resolve(context.Background(), cert)
	requireError(t, err, ErrCertificateExpired)
}

func TestRevocationIsChecked(t *testing.T) {
	ca := newTestCA(t, "Trusted Root")
	cert := ca.issue(t, leafOptions{
		commonName: "revoked-client",
		dnsNames:   []string{"revoked.example"},
	})
	revoked := NewRevocationSet()
	r := testRegistry(t, ca, Options{Revocation: revoked})
	mustRegister(t, r, "tenant", cert)
	if mapping := mustResolve(t, r, cert); mapping.Tenant != "tenant" {
		t.Fatalf("mapping = %+v", mapping)
	}

	revoked.Revoke(cert)
	_, err := r.Resolve(context.Background(), cert)
	requireError(t, err, ErrRevoked)

	revoked.Unrevoke(cert)
	if _, err := r.Resolve(context.Background(), cert); err != nil {
		t.Fatalf("resolve after unrevoke: %v", err)
	}
}

func TestMissingRevocationCheckerFailsClosed(t *testing.T) {
	ca := newTestCA(t, "Trusted Root")
	cert := ca.issue(t, leafOptions{commonName: "client"})
	roots := x509.NewCertPool()
	roots.AddCert(ca.cert)
	noRevocation, err := NewRegistry(Options{
		Roots:       roots,
		CurrentTime: func() time.Time { return testNow },
	})
	if err != nil {
		t.Fatalf("new registry: %v", err)
	}
	mustRegister(t, noRevocation, "tenant", cert)

	_, err = noRevocation.Resolve(context.Background(), cert)
	requireError(t, err, ErrRevocationUnavailable)
}

func TestRotationPublishesNewGeneration(t *testing.T) {
	ca := newTestCA(t, "Trusted Root")
	oldCert := ca.issue(t, leafOptions{
		commonName:   "rotating-client",
		serialNumber: "rotation-uid",
		dnsNames:     []string{"rotation.example"},
	})
	newCert := ca.issue(t, leafOptions{
		commonName:   "rotating-client",
		serialNumber: "rotation-uid",
		dnsNames:     []string{"rotation.example"},
	})
	secondNewCert := ca.issue(t, leafOptions{
		commonName:   "rotating-client",
		serialNumber: "rotation-uid-2",
		dnsNames:     []string{"rotation-2.example"},
	})
	r := testRegistry(t, ca, Options{})
	mustRegister(t, r, "tenant", oldCert)
	first := mustResolve(t, r, oldCert)
	if first.Generation != 1 {
		t.Fatalf("first generation = %d, want 1", first.Generation)
	}
	oldSnapshot := r.Snapshot()

	generation, err := r.Rotate("tenant", newCert)
	if err != nil {
		t.Fatalf("rotate: %v", err)
	}
	if generation != 2 {
		t.Fatalf("rotation generation = %d, want 2", generation)
	}
	_, err = r.Resolve(context.Background(), oldCert)
	requireError(t, err, ErrUnregisteredCertificate)
	rotated := mustResolve(t, r, newCert)
	if rotated.Tenant != "tenant" || rotated.Generation != 2 {
		t.Fatalf("rotated mapping = %+v", rotated)
	}

	// An already-issued snapshot keeps its old, internally consistent view.
	oldView := mustResolveSnapshot(t, oldSnapshot, oldCert)
	if oldView.Generation != 1 || oldView.Tenant != "tenant" {
		t.Fatalf("old snapshot mapping = %+v", oldView)
	}

	generation, err = r.Rotate("tenant", newCert, secondNewCert)
	if err != nil {
		t.Fatalf("rotate multiple certs: %v", err)
	}
	if generation != 3 {
		t.Fatalf("multi-cert rotation generation = %d, want 3", generation)
	}
	if m := mustResolve(t, r, newCert); m.Generation != 3 {
		t.Fatalf("new cert generation = %d", m.Generation)
	}
	if m := mustResolve(t, r, secondNewCert); m.Generation != 3 {
		t.Fatalf("second new cert generation = %d", m.Generation)
	}
}

func TestStrongIdentityCollisionIsAmbiguous(t *testing.T) {
	ca := newTestCA(t, "Trusted Root")
	a := ca.issue(t, leafOptions{
		commonName:   "a",
		serialNumber: "uid-a",
		dnsNames:     []string{"shared.example"},
	})
	b := ca.issue(t, leafOptions{
		commonName:   "b",
		serialNumber: "uid-b",
		dnsNames:     []string{"shared.example"},
	})
	r := testRegistry(t, ca, Options{})
	mustRegister(t, r, "tenant-a", a)
	mustRegister(t, r, "tenant-b", b)

	_, err := r.Resolve(context.Background(), a)
	requireError(t, err, ErrAmbiguousIdentity)
	_, err = r.Resolve(context.Background(), b)
	requireError(t, err, ErrAmbiguousIdentity)
}

func TestBatchRegistrationReplaceAndRemoval(t *testing.T) {
	ca := newTestCA(t, "Trusted Root")
	a1 := ca.issue(t, leafOptions{commonName: "a", dnsNames: []string{"a1.example"}})
	a2 := ca.issue(t, leafOptions{commonName: "a", dnsNames: []string{"a2.example"}})
	b := ca.issue(t, leafOptions{commonName: "b", dnsNames: []string{"b.example"}})
	r := testRegistry(t, ca, Options{})

	generation, err := r.RegisterAll("tenant-a", a1, a2)
	if err != nil {
		t.Fatalf("register all: %v", err)
	}
	if generation != 1 || r.Len() != 2 {
		t.Fatalf("generation = %d, len = %d", generation, r.Len())
	}
	for _, cert := range []*x509.Certificate{a1, a2} {
		mapping, err := r.ResolveDER(context.Background(), cert.Raw)
		if err != nil {
			t.Fatalf("resolve registered cert: %v", err)
		}
		if mapping.Tenant != "tenant-a" || mapping.Generation != 1 {
			t.Fatalf("mapping = %+v", mapping)
		}
	}

	// A failing batch is atomic: the valid certificate in it is not published.
	failedCert := ca.issue(t, leafOptions{commonName: "failed", dnsNames: []string{"failed.example"}})
	if _, err := r.RegisterAll("tenant-a", failedCert, nil); !errors.Is(err, ErrCertificateRequired) {
		t.Fatalf("invalid batch error = %v", err)
	}
	if generation := r.Generation(); generation != 1 || r.Len() != 2 {
		t.Fatalf("invalid batch changed registry: generation = %d, len = %d", generation, r.Len())
	}
	if _, err := r.Resolve(context.Background(), failedCert); !errors.Is(err, ErrUnregisteredCertificate) {
		t.Fatalf("certificate from failed batch resolved: %v", err)
	}

	generation, err = r.Replace([]Registration{{Tenant: "tenant-b", Certificate: b}})
	if err != nil {
		t.Fatalf("replace: %v", err)
	}
	if generation != 2 || r.Len() != 1 {
		t.Fatalf("replace generation = %d, len = %d", generation, r.Len())
	}
	if _, err := r.Resolve(context.Background(), a1); !errors.Is(err, ErrUnregisteredCertificate) {
		t.Fatalf("old mapping survived replace: %v", err)
	}
	if mapping := mustResolve(t, r, b); mapping.Tenant != "tenant-b" || mapping.Generation != 2 {
		t.Fatalf("replacement mapping = %+v", mapping)
	}

	generation, err = r.RemoveCertificate(FingerprintOf(b))
	if err != nil {
		t.Fatalf("remove certificate: %v", err)
	}
	if generation != 3 || r.Len() != 0 {
		t.Fatalf("remove generation = %d, len = %d", generation, r.Len())
	}
}

func TestFingerprintConflictAndParsing(t *testing.T) {
	ca := newTestCA(t, "Trusted Root")
	cert := ca.issue(t, leafOptions{commonName: "client"})
	r := testRegistry(t, ca, Options{})
	mustRegister(t, r, "tenant-a", cert)
	_, err := r.Add("tenant-b", cert)
	requireError(t, err, ErrFingerprintConflict)

	fp := FingerprintOf(cert)
	parsed, err := ParseFingerprint("sha256:" + fp.String())
	if err != nil {
		t.Fatalf("parse fingerprint: %v", err)
	}
	if parsed != fp {
		t.Fatalf("parsed fingerprint = %s, want %s", parsed, fp)
	}
	colonized, err := ParseFingerprint(formatColonFingerprint(fp))
	if err != nil {
		t.Fatalf("parse colon fingerprint: %v", err)
	}
	if colonized != fp {
		t.Fatalf("colon fingerprint mismatch")
	}
}

func formatColonFingerprint(fp Fingerprint) string {
	raw := fp.String()
	out := make([]byte, 0, len(raw)+len(raw)/2)
	for i := 0; i < len(raw); i += 2 {
		if i > 0 {
			out = append(out, ':')
		}
		out = append(out, raw[i], raw[i+1])
	}
	return string(out)
}
