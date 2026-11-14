package netsec_test

import (
	"context"
	"crypto/x509"
	"errors"
	"testing"
	"time"

	netsec "example.org/netsec/q0238"
)

func TestEvilProdWildcardDoesNotWidenPrincipal(t *testing.T) {
	mesh := newTestMesh(t, prodTrustDomain, nil)
	prodCA := newTestCA(t, "prod-root")
	testCA := newTestCA(t, "test-root")
	mesh.installBundle(t, 1, prodCA)
	if err := mesh.authority.UpdateBundle(netsec.TrustDomain(testTrustDomain), 1, []*x509.Certificate{testCA.cert}); err != nil {
		t.Fatal(err)
	}

	wildcard := netsec.WildcardDNSName("*.prod")
	if err := wildcard.Validate(); err != nil {
		t.Fatal(err)
	}
	if !wildcard.Match("evil.prod") {
		t.Fatal("test setup: wildcard should match the hostname label")
	}
	if wildcard.Use != netsec.SANUseHostname {
		t.Fatalf("wildcard use is %v, want hostname-only", wildcard.Use)
	}

	serverCert := prodCA.issue(t, leafSpec{
		spiffeID: serverID,
		dns:      []string{serverDNS},
		lifetime: time.Hour,
	})
	evilBackendID := "spiffe://prod.example.org/prod/evil"
	evilCert := prodCA.issue(t, leafSpec{
		spiffeID: evilBackendID,
		dns:      []string{"evil.prod"},
		lifetime: time.Hour,
	})

	policy := netsec.ConnectionPolicy{
		Caller: netsec.IdentityPolicy{
			TrustDomain:   prodTrustDomain,
			Paths:         []string{"/prod/backend"},
			DNSNames:      []netsec.DNSNameRule{wildcard},
			NodeSelectors: map[string]string{"env": "prod"},
			Uses:          []netsec.PrincipalUse{netsec.UseClient},
		},
		Target: identityPolicy(t, prodTrustDomain, serverID, exactRules(serverDNS), nil, map[string]string{"env": "prod"}, netsec.UseServer),
	}
	if err := mesh.authority.UpdatePolicies(1, map[string]netsec.ConnectionPolicy{"mesh-connect": policy}); err != nil {
		t.Fatal(err)
	}

	request := mesh.request("mesh-connect", evilBackendID, evilCert, serverID, serverCert, "connect")
	if _, err := mesh.authority.AuthorizeConnection(context.Background(), request); !errors.Is(err, netsec.ErrPolicyDenied) {
		t.Fatalf("wildcard DNS SAN widened the principal path: %v", err)
	}

	testID := "spiffe://test.example.org/prod/backend"
	testCert := testCA.issue(t, leafSpec{
		spiffeID: testID,
		dns:      []string{"evil.prod"},
		lifetime: time.Hour,
	})
	testCredential := netsec.PeerCredential{
		Leaf:        testCert,
		Attestation: mesh.attestIn(netsec.TrustDomain(testTrustDomain), testID, map[string]string{"env": "prod"}),
	}
	if _, err := mesh.authority.VerifyPeer(context.Background(), testCredential); !errors.Is(err, netsec.ErrForeignTrustDomain) {
		t.Fatalf("evil.prod test-domain certificate crossed into production: %v", err)
	}

	forgedProd := testCA.issue(t, leafSpec{
		spiffeID: "spiffe://prod.example.org/prod/backend",
		dns:      []string{"evil.prod"},
		lifetime: time.Hour,
	})
	forgedCredential := netsec.PeerCredential{
		Leaf:        forgedProd,
		Attestation: mesh.attest("spiffe://prod.example.org/prod/backend", map[string]string{"env": "prod"}),
	}
	if _, err := mesh.authority.VerifyPeer(context.Background(), forgedCredential); !errors.Is(err, netsec.ErrInvalidChain) {
		t.Fatalf("test-CA certificate with a production URI was accepted: %v", err)
	}
}

func TestMultiLevelWildcardIsRejectedInPolicy(t *testing.T) {
	rule := netsec.DNSNameRule{Pattern: "*.*.prod", Use: netsec.SANUseHostname, Depth: 1}
	if err := rule.Validate(); !errors.Is(err, netsec.ErrMultiLevelWildcard) {
		t.Fatalf("multi-level wildcard: got %v", err)
	}
	if rule.Match("a.b.prod") {
		t.Fatal("multi-level wildcard rule matched a name")
	}

	mesh := newTestMesh(t, prodTrustDomain, nil)
	policy := netsec.ConnectionPolicy{
		Caller: netsec.IdentityPolicy{
			TrustDomain: prodTrustDomain,
			Paths:       []string{"/prod/backend"},
			DNSNames:    []netsec.DNSNameRule{rule},
		},
		Target: netsec.IdentityPolicy{
			TrustDomain: prodTrustDomain,
			Paths:       []string{"/prod/frontend"},
		},
	}
	if err := (netsec.IdentityPolicy{TrustDomain: prodTrustDomain, Paths: []string{"/prod/backend"}, DNSNames: []netsec.DNSNameRule{rule}}).Validate(); !errors.Is(err, netsec.ErrMultiLevelWildcard) {
		t.Fatalf("identity policy accepted multi-level wildcard: %v", err)
	}
	if err := mesh.authority.UpdatePolicies(1, map[string]netsec.ConnectionPolicy{"bad": policy}); !errors.Is(err, netsec.ErrInvalidConfig) {
		t.Fatalf("policy update accepted multi-level wildcard: %v", err)
	}
}

func TestWildcardCertificateSANIsRejected(t *testing.T) {
	mesh := newTestMesh(t, prodTrustDomain, nil)
	ca := newTestCA(t, "prod-root")
	mesh.installBundle(t, 1, ca)

	for _, dnsName := range []string{"*.prod", "*.*.prod", "api.*.prod"} {
		cert := ca.issue(t, leafSpec{
			spiffeID: clientID,
			dns:      []string{dnsName},
			lifetime: time.Hour,
		})
		credential := netsec.PeerCredential{
			Leaf:        cert,
			Attestation: mesh.attest(clientID, map[string]string{"env": "prod"}),
		}
		if _, err := mesh.authority.VerifyPeer(context.Background(), credential); !errors.Is(err, netsec.ErrWildcardNotAllowed) {
			t.Fatalf("certificate DNS SAN %q: got %v, want ErrWildcardNotAllowed", dnsName, err)
		}
	}
}

func TestURIImpersonationIsRejected(t *testing.T) {
	mesh := newTestMesh(t, prodTrustDomain, nil)
	ca := newTestCA(t, "prod-root")
	mesh.installBundle(t, 1, ca)

	tests := []struct {
		name string
		spec leafSpec
		want error
	}{
		{
			name: "https URI alongside SPIFFE",
			spec: leafSpec{
				spiffeID: clientID,
				extraURI: []string{"https://prod.example.org/prod/backend"},
				lifetime: time.Hour,
			},
			want: netsec.ErrSANTypeSubstitution,
		},
		{
			name: "https URI without SPIFFE",
			spec: leafSpec{
				extraURI: []string{"https://prod.example.org/prod/backend"},
				dns:      []string{clientDNS},
				lifetime: time.Hour,
			},
			want: netsec.ErrSANTypeSubstitution,
		},
		{
			name: "DNS SAN without SPIFFE",
			spec: leafSpec{
				dns:      []string{clientDNS},
				lifetime: time.Hour,
			},
			want: netsec.ErrMissingSPIFFEID,
		},
		{
			name: "URI userinfo",
			spec: leafSpec{
				extraURI: []string{"spiffe://prod.example.org@evil.example.org/prod/backend"},
				lifetime: time.Hour,
			},
			want: netsec.ErrSANTypeSubstitution,
		},
		{
			name: "URI port",
			spec: leafSpec{
				extraURI: []string{"spiffe://prod.example.org:443/prod/backend"},
				lifetime: time.Hour,
			},
			want: netsec.ErrSANTypeSubstitution,
		},
		{
			name: "multiple identities",
			spec: leafSpec{
				spiffeID: clientID,
				extraURI: []string{"spiffe://prod.example.org/prod/admin"},
				dns:      []string{clientDNS},
				lifetime: time.Hour,
			},
			want: netsec.ErrMultipleIdentities,
		},
		{
			name: "IP literal encoded as DNS",
			spec: leafSpec{
				spiffeID: clientID,
				dns:      []string{"10.0.0.11"},
				lifetime: time.Hour,
			},
			want: netsec.ErrSANTypeSubstitution,
		},
		{
			name: "SPIFFE URI encoded as DNS",
			spec: leafSpec{
				spiffeID: clientID,
				dns:      []string{"spiffe://prod.example.org/prod/backend"},
				lifetime: time.Hour,
			},
			want: netsec.ErrInvalidSAN,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			cert := ca.issue(t, test.spec)
			workloadID := test.spec.spiffeID
			if workloadID == "" {
				workloadID = clientID
			}
			credential := netsec.PeerCredential{
				Leaf:        cert,
				Attestation: mesh.attest(workloadID, map[string]string{"env": "prod"}),
			}
			if _, err := mesh.authority.VerifyPeer(context.Background(), credential); !errors.Is(err, test.want) {
				t.Fatalf("got %v, want %v", err, test.want)
			}
		})
	}
}

func TestSANTypesDoNotSubstitute(t *testing.T) {
	setup := setupLegit(t, nil)
	ipOnly := setup.ca.issue(t, leafSpec{
		spiffeID: clientID,
		ips:      []string{"10.0.0.11"},
		lifetime: time.Hour,
	})
	request := setup.mesh.request("mesh-connect", clientID, ipOnly, serverID, setup.serverCert, "connect")

	dnsOnly := setup.policy
	dnsOnly.Caller = identityPolicy(t, prodTrustDomain, clientID, exactRules(clientDNS), nil, map[string]string{"env": "prod"}, netsec.UseClient)
	if err := setup.mesh.authority.UpdatePolicies(2, map[string]netsec.ConnectionPolicy{"mesh-connect": dnsOnly}); err != nil {
		t.Fatal(err)
	}
	if _, err := setup.mesh.authority.AuthorizeConnection(context.Background(), request); !errors.Is(err, netsec.ErrPolicyDenied) {
		t.Fatalf("IP SAN was accepted by a DNS-only policy: %v", err)
	}
	if err := netsec.ExactDNSName("10.0.0.11").Validate(); !errors.Is(err, netsec.ErrSANTypeSubstitution) {
		t.Fatalf("IP literal DNS rule: got %v", err)
	}

	ipPolicy := setup.policy
	ipPolicy.Caller = identityPolicy(t, prodTrustDomain, clientID, nil, []string{"10.0.0.0/24"}, map[string]string{"env": "prod"}, netsec.UseClient)
	if err := setup.mesh.authority.UpdatePolicies(3, map[string]netsec.ConnectionPolicy{"mesh-connect": ipPolicy}); err != nil {
		t.Fatal(err)
	}
	if _, err := setup.mesh.authority.AuthorizeConnection(context.Background(), request); err != nil {
		t.Fatalf("matching IP SAN policy rejected: %v", err)
	}
}

func TestExtraSANsCannotAmplifyIdentity(t *testing.T) {
	setup := setupLegit(t, nil)
	extraDNS := setup.ca.issue(t, leafSpec{
		spiffeID: clientID,
		dns:      []string{clientDNS, "admin.prod.example.org"},
		ips:      []string{"10.0.0.11"},
		lifetime: time.Hour,
	})
	request := setup.mesh.request("mesh-connect", clientID, extraDNS, serverID, setup.serverCert, "connect")
	if _, err := setup.mesh.authority.AuthorizeConnection(context.Background(), request); !errors.Is(err, netsec.ErrPolicyDenied) {
		t.Fatalf("unlisted extra DNS SAN was accepted: %v", err)
	}

	extraIP := setup.ca.issue(t, leafSpec{
		spiffeID: clientID,
		dns:      []string{clientDNS},
		ips:      []string{"10.0.0.11", "192.0.2.10"},
		lifetime: time.Hour,
	})
	request = setup.mesh.request("mesh-connect", clientID, extraIP, serverID, setup.serverCert, "connect")
	if _, err := setup.mesh.authority.AuthorizeConnection(context.Background(), request); !errors.Is(err, netsec.ErrPolicyDenied) {
		t.Fatalf("unlisted extra IP SAN was accepted: %v", err)
	}
}

func TestNodeAttestationCannotReplaceSignatureChain(t *testing.T) {
	mesh := newTestMesh(t, prodTrustDomain, nil)
	trustedCA := newTestCA(t, "prod-root")
	untrustedCA := newTestCA(t, "test-root")
	mesh.installBundle(t, 1, trustedCA)

	untrusted := untrustedCA.issue(t, leafSpec{spiffeID: clientID, lifetime: time.Hour})
	credential := mesh.credential(clientID, untrusted)
	if _, err := mesh.authority.VerifyPeer(context.Background(), credential); !errors.Is(err, netsec.ErrInvalidChain) {
		t.Fatalf("node attestation replaced an untrusted chain: %v", err)
	}
}
