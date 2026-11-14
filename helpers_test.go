package netsec_test

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"math/big"
	"net"
	"net/netip"
	"net/url"
	"testing"
	"time"

	netsec "example.org/netsec/q0238"
)

var testTime = time.Date(2026, 11, 14, 3, 0, 0, 0, time.UTC)

type testCA struct {
	cert   *x509.Certificate
	key    ed25519.PrivateKey
	serial int64
}

type leafSpec struct {
	spiffeID  string
	dns       []string
	ips       []string
	extraURI  []string
	emails    []string
	ekus      []x509.ExtKeyUsage
	lifetime  time.Duration
	serial    *big.Int
	notBefore time.Time
}

func newTestCA(t *testing.T, name string) *testCA {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: name},
		NotBefore:             testTime.Add(-24 * time.Hour),
		NotAfter:              testTime.Add(10 * 365 * 24 * time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, pub, priv)
	if err != nil {
		t.Fatal(err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	return &testCA{cert: cert, key: priv, serial: 1000}
}

func (ca *testCA) issue(t *testing.T, spec leafSpec) *x509.Certificate {
	t.Helper()
	pub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}

	ca.serial++
	serial := big.NewInt(ca.serial)
	if spec.serial != nil {
		serial = spec.serial
	}
	notBefore := testTime.Add(-time.Minute)
	if !spec.notBefore.IsZero() {
		notBefore = spec.notBefore
	}
	lifetime := spec.lifetime
	if lifetime == 0 {
		lifetime = time.Hour
	}

	var uris []*url.URL
	if spec.spiffeID != "" {
		parsed, parseErr := url.Parse(spec.spiffeID)
		if parseErr != nil {
			t.Fatal(parseErr)
		}
		uris = append(uris, parsed)
	}
	for _, raw := range spec.extraURI {
		parsed, parseErr := url.Parse(raw)
		if parseErr != nil {
			t.Fatal(parseErr)
		}
		uris = append(uris, parsed)
	}

	var addresses []net.IP
	for _, raw := range spec.ips {
		addr, parseErr := netip.ParseAddr(raw)
		if parseErr != nil {
			t.Fatal(parseErr)
		}
		addresses = append(addresses, net.IP(addr.AsSlice()))
	}

	ekus := spec.ekus
	if len(ekus) == 0 {
		ekus = []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth, x509.ExtKeyUsageServerAuth}
	}

	template := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: spec.spiffeID},
		NotBefore:             notBefore,
		NotAfter:              notBefore.Add(lifetime),
		KeyUsage:              x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           ekus,
		BasicConstraintsValid: true,
		DNSNames:              spec.dns,
		IPAddresses:           addresses,
		URIs:                  uris,
		EmailAddresses:        spec.emails,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, ca.cert, pub, ca.key)
	if err != nil {
		t.Fatal(err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	return cert
}

func (ca *testCA) intermediate(t *testing.T, name string) *testCA {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	ca.serial++
	template := &x509.Certificate{
		SerialNumber:          big.NewInt(ca.serial),
		Subject:               pkix.Name{CommonName: name},
		NotBefore:             testTime.Add(-24 * time.Hour),
		NotAfter:              testTime.Add(5 * 365 * 24 * time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, ca.cert, pub, ca.key)
	if err != nil {
		t.Fatal(err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	return &testCA{cert: cert, key: priv, serial: 2000}
}

type testMesh struct {
	t         *testing.T
	now       time.Time
	trust     netsec.TrustDomain
	authority *netsec.Authority
	nodeID    string
	nodeKey   ed25519.PrivateKey
	nonce     []byte
}

func newTestMesh(t *testing.T, trustDomain string, configure func(*netsec.Config)) *testMesh {
	t.Helper()
	mesh := &testMesh{
		t:      t,
		now:    testTime,
		trust:  netsec.TrustDomain(trustDomain),
		nodeID: "node-" + trustDomain,
		nonce:  []byte("connection-challenge"),
	}
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	mesh.nodeKey = priv

	cfg := netsec.Config{
		TrustDomain:     mesh.trust,
		AttestationKeys: map[string]ed25519.PublicKey{mesh.nodeID: pub},
		Clock:           func() time.Time { return mesh.now },
	}
	if configure != nil {
		configure(&cfg)
	}
	authority, err := netsec.NewAuthority(cfg)
	if err != nil {
		t.Fatal(err)
	}
	mesh.authority = authority
	return mesh
}

func (m *testMesh) attest(workloadID string, selectors map[string]string) *netsec.NodeAttestation {
	return m.attestIn(m.trust, workloadID, selectors)
}

func (m *testMesh) attestIn(trustDomain netsec.TrustDomain, workloadID string, selectors map[string]string) *netsec.NodeAttestation {
	m.t.Helper()
	id, err := netsec.ParseSPIFFEID(workloadID)
	if err != nil {
		m.t.Fatal(err)
	}
	doc := &netsec.NodeAttestation{
		TrustDomain: trustDomain,
		NodeID:      m.nodeID,
		WorkloadID:  id,
		Nonce:       append([]byte(nil), m.nonce...),
		IssuedAt:    m.now.Add(-time.Minute),
		ExpiresAt:   m.now.Add(5 * time.Minute),
		Selectors:   cloneMap(selectors),
	}
	if err := doc.Sign(m.nodeKey); err != nil {
		m.t.Fatal(err)
	}
	return doc
}

func (m *testMesh) credential(workloadID string, cert *x509.Certificate) netsec.PeerCredential {
	return m.credentialWith(workloadID, cert, map[string]string{"env": "prod", "node": m.nodeID})
}

func (m *testMesh) credentialWith(workloadID string, cert *x509.Certificate, selectors map[string]string) netsec.PeerCredential {
	m.t.Helper()
	return netsec.PeerCredential{
		Leaf:        cert,
		Attestation: m.attest(workloadID, selectors),
	}
}

func (m *testMesh) installBundle(t *testing.T, generation netsec.Generation, ca *testCA) {
	t.Helper()
	if err := m.authority.UpdateBundle(m.trust, generation, []*x509.Certificate{ca.cert}); err != nil {
		t.Fatal(err)
	}
}

func (m *testMesh) request(policy, callerID string, caller *x509.Certificate, targetID string, target *x509.Certificate, action string) netsec.ConnectionRequest {
	return netsec.ConnectionRequest{
		Policy: policy,
		Caller: m.credential(callerID, caller),
		Target: m.credential(targetID, target),
		Action: action,
		Nonce:  append([]byte(nil), m.nonce...),
	}
}

func identityPolicy(t *testing.T, trustDomain, workloadID string, dnsRules []netsec.DNSNameRule, ipPrefixes []string, selectors map[string]string, uses ...netsec.PrincipalUse) netsec.IdentityPolicy {
	t.Helper()
	id, err := netsec.ParseSPIFFEID(workloadID)
	if err != nil {
		t.Fatal(err)
	}
	var prefixes []netip.Prefix
	for _, raw := range ipPrefixes {
		prefix, parseErr := netip.ParsePrefix(raw)
		if parseErr != nil {
			t.Fatal(parseErr)
		}
		prefixes = append(prefixes, prefix)
	}
	return netsec.IdentityPolicy{
		TrustDomain:   trustDomain,
		Paths:         []string{string(id.Path)},
		DNSNames:      dnsRules,
		IPPrefixes:    prefixes,
		NodeSelectors: cloneMap(selectors),
		Uses:          uses,
	}
}

func exactRules(names ...string) []netsec.DNSNameRule {
	rules := make([]netsec.DNSNameRule, 0, len(names))
	for _, name := range names {
		rules = append(rules, netsec.ExactDNSName(name))
	}
	return rules
}

func cloneMap(in map[string]string) map[string]string {
	out := make(map[string]string, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}
