package nameconstraint

import (
	"crypto/x509"
	"errors"
	"net"
	"testing"
)

func reasonOf(t *testing.T, err error) Reason {
	t.Helper()
	var ce *ConstraintError
	if !errors.As(err, &ce) {
		t.Fatalf("expected *ConstraintError, got %T: %v", err, err)
	}
	return ce.Reason
}

// TestAttackExternalDomain reproduces the original attack: a constrained
// intermediate is limited to internal.example; a leaf for an external domain
// must be rejected even though its own SAN is well-formed.
func TestAttackExternalDomain(t *testing.T) {
	rootKey := mustKey()
	root := makeCert(certSpec{subject: "root", isCA: true}, nil, rootKey, rootKey)

	intKey := mustKey()
	intermediate := makeCert(certSpec{
		subject: "constrained-intermediate",
		isCA:    true,
		constraints: &testConstraints{
			permittedDNS: []string{"internal.example"},
		},
	}, root, rootKey, intKey)

	goodLeaf := makeCert(certSpec{
		subject: "api.internal.example",
		dns:     []string{"api.internal.example"},
	}, intermediate, intKey, mustKey())

	evilLeaf := makeCert(certSpec{
		subject: "evil.com",
		dns:     []string{"evil.com"},
	}, intermediate, intKey, mustKey())

	if err := VerifyChain([]*x509.Certificate{goodLeaf, intermediate, root}); err != nil {
		t.Fatalf("internal leaf should pass: %v", err)
	}
	err := VerifyChain([]*x509.Certificate{evilLeaf, intermediate, root})
	if err == nil {
		t.Fatal("external leaf must be rejected by the intermediate constraint")
	}
	if got := reasonOf(t, err); got != ReasonNotPermitted {
		t.Fatalf("got reason %d, want ReasonNotPermitted", got)
	}
}

// TestExclusionWins ensures an excluded subtree overrides a broader permit.
func TestExclusionWins(t *testing.T) {
	rootKey := mustKey()
	root := makeCert(certSpec{subject: "root", isCA: true}, nil, rootKey, rootKey)
	intKey := mustKey()
	intermediate := makeCert(certSpec{
		subject: "ca",
		isCA:    true,
		constraints: &testConstraints{
			permittedDNS: []string{"example.com"},
			excludedDNS:  []string{"blocked.example.com"},
		},
	}, root, rootKey, intKey)

	cases := []struct {
		name    string
		dns     string
		wantErr Reason
		ok      bool
	}{
		{"permitted", "www.example.com", ReasonUnknown, true},
		{"exact permitted", "example.com", ReasonUnknown, true},
		{"excluded exact", "blocked.example.com", ReasonExcluded, false},
		{"excluded subdomain", "api.blocked.example.com", ReasonExcluded, false},
		{"outside permit", "evil.org", ReasonNotPermitted, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			leaf := makeCert(certSpec{subject: tc.dns, dns: []string{tc.dns}},
				intermediate, intKey, mustKey())
			err := VerifyChain([]*x509.Certificate{leaf, intermediate, root})
			if tc.ok && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !tc.ok {
				if err == nil {
					t.Fatal("expected error")
				}
				if got := reasonOf(t, err); got != tc.wantErr {
					t.Fatalf("got reason %d, want %d", got, tc.wantErr)
				}
			}
		})
	}
}

// TestLeadingDotConstraint checks that ".example.com" matches subdomains but
// not the apex.
func TestLeadingDotConstraint(t *testing.T) {
	rootKey := mustKey()
	root := makeCert(certSpec{subject: "root", isCA: true}, nil, rootKey, rootKey)
	intKey := mustKey()
	intermediate := makeCert(certSpec{
		subject: "ca",
		isCA:    true,
		constraints: &testConstraints{
			permittedDNS: []string{".example.com"},
		},
	}, root, rootKey, intKey)

	for _, tc := range []struct {
		dns string
		ok  bool
	}{
		{"host.example.com", true},
		{"a.b.example.com", true},
		{"example.com", false},
		{"notexample.com", false},
	} {
		leaf := makeCert(certSpec{subject: tc.dns, dns: []string{tc.dns}},
			intermediate, intKey, mustKey())
		err := VerifyChain([]*x509.Certificate{leaf, intermediate, root})
		if tc.ok && err != nil {
			t.Errorf("%s should pass: %v", tc.dns, err)
		}
		if !tc.ok && err == nil {
			t.Errorf("%s should fail", tc.dns)
		}
	}
}

// TestWildcardSAN exercises wildcard matching against both bare and
// leading-dot constraints, plus exclusion.
func TestWildcardSAN(t *testing.T) {
	rootKey := mustKey()
	root := makeCert(certSpec{subject: "root", isCA: true}, nil, rootKey, rootKey)

	for _, tc := range []struct {
		name       string
		constraint testConstraints
		ok         bool
	}{
		{"bare permit", testConstraints{permittedDNS: []string{"example.com"}}, true},
		{"dot permit", testConstraints{permittedDNS: []string{".example.com"}}, true},
		{"parent permit", testConstraints{permittedDNS: []string{"com"}}, true},
		{"sibling permit", testConstraints{permittedDNS: []string{"other.com"}}, false},
		{"narrow permit", testConstraints{permittedDNS: []string{"sub.example.com"}}, false},
		{"excluded wildcard base", testConstraints{
			permittedDNS: []string{"example.com"},
			excludedDNS:  []string{"example.com"},
		}, false},
		{"excluded subtree dot", testConstraints{
			permittedDNS: []string{"example.com"},
			excludedDNS:  []string{".example.com"},
		}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			intKey := mustKey()
			intermediate := makeCert(certSpec{
				subject:     "ca",
				isCA:        true,
				constraints: &tc.constraint,
			}, root, rootKey, intKey)
			leaf := makeCert(certSpec{subject: "wild", dns: []string{"*.example.com"}},
				intermediate, intKey, mustKey())
			err := VerifyChain([]*x509.Certificate{leaf, intermediate, root})
			if tc.ok && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !tc.ok && err == nil {
				t.Fatal("expected failure")
			}
		})
	}
}

// TestTrailingDot verifies normalization of the absolute-name marker on both
// SANs and constraints.
func TestTrailingDot(t *testing.T) {
	rootKey := mustKey()
	root := makeCert(certSpec{subject: "root", isCA: true}, nil, rootKey, rootKey)
	intKey := mustKey()
	intermediate := makeCert(certSpec{
		subject: "ca",
		isCA:    true,
		constraints: &testConstraints{
			permittedDNS: []string{"example.com."},
			excludedDNS:  []string{"blocked.example.com."},
		},
	}, root, rootKey, intKey)

	for _, tc := range []struct {
		dns string
		ok  bool
	}{
		{"api.example.com", true},
		{"api.example.com.", true},
		{"blocked.example.com", false},
		{"blocked.example.com.", false},
	} {
		leaf := makeCert(certSpec{subject: tc.dns, dns: []string{tc.dns}},
			intermediate, intKey, mustKey())
		err := VerifyChain([]*x509.Certificate{leaf, intermediate, root})
		if tc.ok && err != nil {
			t.Errorf("%s should pass: %v", tc.dns, err)
		}
		if !tc.ok && err == nil {
			t.Errorf("%s should fail", tc.dns)
		}
	}
}

// TestNoSANNoCNFallback ensures a certificate without SAN is not matched
// against constraints using its common name.
func TestNoSANNoCNFallback(t *testing.T) {
	rootKey := mustKey()
	root := makeCert(certSpec{subject: "root", isCA: true}, nil, rootKey, rootKey)
	intKey := mustKey()
	intermediate := makeCert(certSpec{
		subject: "ca",
		isCA:    true,
		constraints: &testConstraints{
			permittedDNS: []string{"allowed.example"},
		},
	}, root, rootKey, intKey)

	leaf := makeCert(certSpec{
		subject: "forbidden.evil.com",
		noSAN:   true,
	}, intermediate, intKey, mustKey())

	if err := VerifyChain([]*x509.Certificate{leaf, intermediate, root}); err != nil {
		t.Fatalf("cert without SAN must not be constrained via CN: %v", err)
	}
}

// TestEmptyPermittedSubtrees covers an explicitly empty permitted set. The
// encoding cannot name a type, so per RFC 5280 semantics every constrained
// name type is denied; SAN-less certificates remain unaffected.
func TestEmptyPermittedSubtrees(t *testing.T) {
	rootKey := mustKey()
	root := makeCert(certSpec{subject: "root", isCA: true}, nil, rootKey, rootKey)
	intKey := mustKey()
	ext, err := buildNameConstraintsExtension(&testConstraints{permitEmptyDNS: true})
	if err != nil {
		t.Fatal(err)
	}
	intermediate := makeManualCert("ca", true, ext, root, rootKey, intKey)

	dnsLeaf := makeCert(certSpec{
		subject: "anything.example",
		dns:     []string{"anything.example"},
	}, intermediate, intKey, mustKey())
	err = VerifyChain([]*x509.Certificate{dnsLeaf, intermediate, root})
	if err == nil {
		t.Fatal("empty permitted set must deny all DNS names")
	}
	if reasonOf(t, err) != ReasonNotPermitted {
		t.Fatalf("got %v", err)
	}

	ipLeaf := makeCert(certSpec{
		subject: "ip-only",
		ips:     []net.IP{net.ParseIP("10.0.0.1")},
	}, intermediate, intKey, mustKey())
	if err := VerifyChain([]*x509.Certificate{ipLeaf, intermediate, root}); err == nil {
		t.Fatal("empty permitted set must also deny IP names")
	}

	emailLeaf := makeCert(certSpec{
		subject: "mail-only",
		emails:  []string{"ops@example.com"},
	}, intermediate, intKey, mustKey())
	if err := VerifyChain([]*x509.Certificate{emailLeaf, intermediate, root}); err == nil {
		t.Fatal("empty permitted set must also deny email names")
	}

	noSANLeaf := makeCert(certSpec{subject: "cn-only", noSAN: true},
		intermediate, intKey, mustKey())
	if err := VerifyChain([]*x509.Certificate{noSANLeaf, intermediate, root}); err != nil {
		t.Fatalf("SAN-less cert must not hit the empty DNS set: %v", err)
	}
}

// TestIPMaskBoundaries checks CIDR matching at network, broadcast and
// adjacent-address boundaries, including IPv6 and family mismatches.
func TestIPMaskBoundaries(t *testing.T) {
	rootKey := mustKey()
	root := makeCert(certSpec{subject: "root", isCA: true}, nil, rootKey, rootKey)
	intKey := mustKey()
	intermediate := makeCert(certSpec{
		subject: "ca",
		isCA:    true,
		constraints: &testConstraints{
			permittedIP: []*net.IPNet{
				mustCIDR("192.0.2.0/24"),
				mustCIDR("2001:db8::/32"),
			},
			excludedIP: []*net.IPNet{mustCIDR("192.0.2.128/25")},
		},
	}, root, rootKey, intKey)

	for _, tc := range []struct {
		name    string
		ip      string
		wantErr Reason
		ok      bool
	}{
		{"network address", "192.0.2.0", ReasonUnknown, true},
		{"last allowed before exclusion", "192.0.2.127", ReasonUnknown, true},
		{"first excluded", "192.0.2.128", ReasonExcluded, false},
		{"broadcast", "192.0.2.255", ReasonExcluded, false},
		{"adjacent network", "192.0.3.0", ReasonNotPermitted, false},
		{"ipv6 in range", "2001:db8::1", ReasonUnknown, true},
		{"ipv6 out of range", "2001:db9::1", ReasonNotPermitted, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			leaf := makeCert(certSpec{
				subject: "ip-leaf",
				ips:     []net.IP{net.ParseIP(tc.ip)},
			}, intermediate, intKey, mustKey())
			err := VerifyChain([]*x509.Certificate{leaf, intermediate, root})
			if tc.ok && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !tc.ok {
				if err == nil {
					t.Fatal("expected error")
				}
				if got := reasonOf(t, err); got != tc.wantErr {
					t.Fatalf("got reason %d, want %d", got, tc.wantErr)
				}
			}
		})
	}
}

// TestEmailConstraints covers exact-mailbox permits, domain permits and
// leading-dot subdomain permits.
func TestEmailConstraints(t *testing.T) {
	rootKey := mustKey()
	root := makeCert(certSpec{subject: "root", isCA: true}, nil, rootKey, rootKey)
	intKey := mustKey()
	intermediate := makeCert(certSpec{
		subject: "ca",
		isCA:    true,
		constraints: &testConstraints{
			permittedEmail: []string{"alice@example.com", "mail.example.org", ".sub.example.net"},
			excludedEmail:  []string{"bob@example.com", "deny.example.org"},
		},
	}, root, rootKey, intKey)

	for _, tc := range []struct {
		name  string
		email string
		ok    bool
	}{
		{"exact mailbox", "alice@example.com", true},
		{"other mailbox same domain", "carol@example.com", false},
		{"excluded mailbox", "bob@example.com", false},
		{"domain permit apex", "x@mail.example.org", true},
		{"domain permit subdomain", "x@a.mail.example.org", true},
		{"excluded domain", "x@deny.example.org", false},
		{"excluded subdomain", "x@a.deny.example.org", false},
		{"dot permit apex rejected", "x@sub.example.net", false},
		{"dot permit subdomain", "x@host.sub.example.net", true},
		{"case insensitive domain", "alice@EXAMPLE.com", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			leaf := makeCert(certSpec{
				subject: "mail-leaf",
				emails:  []string{tc.email},
			}, intermediate, intKey, mustKey())
			err := VerifyChain([]*x509.Certificate{leaf, intermediate, root})
			if tc.ok && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !tc.ok && err == nil {
				t.Fatal("expected error")
			}
		})
	}
}

// TestNestedConstraints verifies that constraints of every ancestor are
// intersected down the chain, including constraints on the trust anchor.
func TestNestedConstraints(t *testing.T) {
	rootKey := mustKey()
	root := makeCert(certSpec{
		subject: "root",
		isCA:    true,
		constraints: &testConstraints{
			permittedDNS: []string{"example.com"},
		},
	}, nil, rootKey, rootKey)

	intKey := mustKey()
	intermediate := makeCert(certSpec{
		subject: "intermediate",
		isCA:    true,
		constraints: &testConstraints{
			permittedDNS: []string{"api.example.com"},
		},
	}, root, rootKey, intKey)

	good := makeCert(certSpec{subject: "host", dns: []string{"host.api.example.com"}},
		intermediate, intKey, mustKey())
	if err := VerifyChain([]*x509.Certificate{good, intermediate, root}); err != nil {
		t.Fatalf("intersection should pass: %v", err)
	}

	// Satisfies the intermediate but not the root constraint applied below it.
	bad := makeCert(certSpec{subject: "host", dns: []string{"api.other.com"}},
		intermediate, intKey, mustKey())
	if err := VerifyChain([]*x509.Certificate{bad, intermediate, root}); err == nil {
		t.Fatal("root constraint must apply to the leaf even when the intermediate is looser")
	}

	// An intermediate itself is also a subordinate of the root and must obey.
	badIntKey := mustKey()
	badInt := makeCert(certSpec{
		subject: "rogue.evil.com",
		isCA:    true,
		dns:     []string{"rogue.evil.com"},
	}, root, rootKey, badIntKey)
	leaf := makeCert(certSpec{subject: "x", dns: []string{"x.example.com"}},
		badInt, badIntKey, mustKey())
	err := VerifyChain([]*x509.Certificate{leaf, badInt, root})
	if err == nil {
		t.Fatal("intermediate's own SAN must be checked against root constraints")
	}
}

// TestCrossSigningMultiplePaths builds two paths to the same leaf: one via a
// constrained cross-signed intermediate (rejected) and one via an
// unconstrained path (accepted). Any one valid path must make verification
// succeed; when every path fails, the error is surfaced.
func TestCrossSigningMultiplePaths(t *testing.T) {
	rootAKey := mustKey()
	rootA := makeCert(certSpec{subject: "root-a", isCA: true}, nil, rootAKey, rootAKey)
	rootBKey := mustKey()
	rootB := makeCert(certSpec{subject: "root-b", isCA: true}, nil, rootBKey, rootBKey)

	intKey := mustKey()
	intSpec := certSpec{subject: "shared-intermediate", isCA: true}

	// Path A: the same intermediate key is cross-signed with constraints.
	constrainedSpec := intSpec
	constrainedSpec.constraints = &testConstraints{permittedDNS: []string{"internal.example"}}
	intA := makeCert(constrainedSpec, rootA, rootAKey, intKey)

	// Path B: same key, different issuer, no constraints.
	intB := makeCert(intSpec, rootB, rootBKey, intKey)

	leafKey := mustKey()
	leafSpec := certSpec{subject: "public.example", dns: []string{"public.example"}}
	leafA := makeCert(leafSpec, intA, intKey, leafKey)
	leafB := makeCert(leafSpec, intB, intKey, leafKey)

	pathA := []*x509.Certificate{leafA, intA, rootA}
	pathB := []*x509.Certificate{leafB, intB, rootB}

	if err := VerifyChain(pathA); err == nil {
		t.Fatal("constrained path alone must reject the external name")
	}
	if err := VerifyChain(pathB); err != nil {
		t.Fatalf("unconstrained path should pass: %v", err)
	}
	if err := VerifyPeerCertificate(nil, [][]*x509.Certificate{pathA, pathB}); err != nil {
		t.Fatalf("one good path must make verification pass: %v", err)
	}

	// Both paths constrained -> failure.
	intBConstrained := makeCert(constrainedSpec, rootB, rootBKey, intKey)
	leafB2 := makeCert(leafSpec, intBConstrained, intKey, leafKey)
	pathB2 := []*x509.Certificate{leafB2, intBConstrained, rootB}
	if err := VerifyPeerCertificate(nil, [][]*x509.Certificate{pathA, pathB2}); err == nil {
		t.Fatal("verification must fail when every path violates constraints")
	}
}

// TestVerifyPeerCertificateRawFallback covers InsecureSkipVerify-style usage
// where only raw certificates are available.
func TestVerifyPeerCertificateRawFallback(t *testing.T) {
	rootKey := mustKey()
	root := makeCert(certSpec{subject: "root", isCA: true}, nil, rootKey, rootKey)
	intKey := mustKey()
	intermediate := makeCert(certSpec{
		subject: "ca",
		isCA:    true,
		constraints: &testConstraints{
			permittedDNS: []string{"internal.example"},
		},
	}, root, rootKey, intKey)

	leaf := makeCert(certSpec{subject: "bad", dns: []string{"external.evil.com"}},
		intermediate, intKey, mustKey())

	raw := [][]byte{leaf.Raw, intermediate.Raw, root.Raw}
	if err := VerifyPeerCertificate(raw, nil); err == nil {
		t.Fatal("raw-chain fallback must enforce constraints")
	}

	if err := VerifyPeerCertificate(nil, nil); err == nil {
		t.Fatal("no certificates at all must be an error")
	}
}

// TestErrorWiring checks that failures point at the right cert and issuer.
func TestErrorWiring(t *testing.T) {
	rootKey := mustKey()
	root := makeCert(certSpec{subject: "root", isCA: true}, nil, rootKey, rootKey)
	intKey := mustKey()
	intermediate := makeCert(certSpec{
		subject: "ca",
		isCA:    true,
		constraints: &testConstraints{
			excludedDNS: []string{"bad.example.com"},
		},
	}, root, rootKey, intKey)
	leaf := makeCert(certSpec{subject: "bad", dns: []string{"bad.example.com"}},
		intermediate, intKey, mustKey())

	err := VerifyChain([]*x509.Certificate{leaf, intermediate, root})
	var ce *ConstraintError
	if !errors.As(err, &ce) {
		t.Fatalf("expected ConstraintError, got %T", err)
	}
	if ce.Cert == nil || !ce.Cert.Equal(leaf) {
		t.Fatal("error should reference the offending leaf")
	}
	if ce.Issuer == nil || !ce.Issuer.Equal(intermediate) {
		t.Fatal("error should reference the constraining issuer")
	}
	if ce.Reason != ReasonExcluded || ce.NameType != "DNS" {
		t.Fatalf("unexpected reason/type: %d %s", ce.Reason, ce.NameType)
	}
}

// TestMixedSANs ensures every SAN of a multi-SAN leaf must satisfy the
// constraints, even when other names are fine.
func TestMixedSANs(t *testing.T) {
	rootKey := mustKey()
	root := makeCert(certSpec{subject: "root", isCA: true}, nil, rootKey, rootKey)
	intKey := mustKey()
	intermediate := makeCert(certSpec{
		subject: "ca",
		isCA:    true,
		constraints: &testConstraints{
			permittedDNS: []string{"example.com"},
			permittedIP:  []*net.IPNet{mustCIDR("10.0.0.0/8")},
		},
	}, root, rootKey, intKey)

	good := makeCert(certSpec{
		subject: "multi",
		dns:     []string{"api.example.com"},
		ips:     []net.IP{net.ParseIP("10.1.2.3")},
	}, intermediate, intKey, mustKey())
	if err := VerifyChain([]*x509.Certificate{good, intermediate, root}); err != nil {
		t.Fatalf("fully permitted leaf should pass: %v", err)
	}

	bad := makeCert(certSpec{
		subject: "multi",
		dns:     []string{"api.example.com", "leak.evil.com"},
		ips:     []net.IP{net.ParseIP("10.1.2.3")},
	}, intermediate, intKey, mustKey())
	if err := VerifyChain([]*x509.Certificate{bad, intermediate, root}); err == nil {
		t.Fatal("one bad DNS SAN must reject the whole leaf")
	}

	badIP := makeCert(certSpec{
		subject: "multi",
		dns:     []string{"api.example.com"},
		ips:     []net.IP{net.ParseIP("192.0.2.9")},
	}, intermediate, intKey, mustKey())
	if err := VerifyChain([]*x509.Certificate{badIP, intermediate, root}); err == nil {
		t.Fatal("out-of-range IP SAN must reject the leaf")
	}
}

// TestWildcardForms checks that only a leftmost "*" label is treated as a
// wildcard; other patterns are literal labels.
func TestWildcardForms(t *testing.T) {
	rootKey := mustKey()
	root := makeCert(certSpec{subject: "root", isCA: true}, nil, rootKey, rootKey)
	intKey := mustKey()
	intermediate := makeCert(certSpec{
		subject: "ca",
		isCA:    true,
		constraints: &testConstraints{
			permittedDNS: []string{"example.com"},
		},
	}, root, rootKey, intKey)

	for _, dns := range []string{"*.example.com", "a.example.com"} {
		leaf := makeCert(certSpec{subject: "x", dns: []string{dns}},
			intermediate, intKey, mustKey())
		if err := VerifyChain([]*x509.Certificate{leaf, intermediate, root}); err != nil {
			t.Errorf("%s should pass: %v", dns, err)
		}
	}

	// "a*.example.com" is not a full-label wildcard: "a*" is treated as a
	// literal label, and the name still falls inside the example.com
	// subtree, so it passes. A label-wildcard pattern outside the subtree
	// must fail instead of being glob-expanded.
	literal := makeCert(certSpec{subject: "x", dns: []string{"a*.example.com"}},
		intermediate, intKey, mustKey())
	if err := VerifyChain([]*x509.Certificate{literal, intermediate, root}); err != nil {
		t.Fatalf("literal a* label inside subtree should pass: %v", err)
	}

	outside := makeCert(certSpec{subject: "x", dns: []string{"*.evil.com"}},
		intermediate, intKey, mustKey())
	if err := VerifyChain([]*x509.Certificate{outside, intermediate, root}); err == nil {
		t.Fatal("wildcard for an outside subtree must be rejected")
	}

	// "*" alone cannot be a wildcard for the apex.
	apex := makeCert(certSpec{subject: "x", dns: []string{"*"}},
		intermediate, intKey, mustKey())
	if err := VerifyChain([]*x509.Certificate{apex, intermediate, root}); err == nil {
		t.Fatal("bare \"*\" SAN must not match a constrained subtree")
	}
}

// TestMalformedConstraintMask checks that non-contiguous IP masks are
// rejected rather than silently applied.
func TestMalformedConstraintMask(t *testing.T) {
	nc := &nameConstraints{
		permittedIPSet: true,
		permittedIP: []*net.IPNet{{
			IP:   net.IPv4(10, 0, 0, 0),
			Mask: net.IPv4Mask(255, 0, 255, 0),
		}},
	}
	leaf := &x509.Certificate{IPAddresses: []net.IP{net.ParseIP("10.0.0.1")}}
	// Build the malformed-constraint error path directly through the parser
	// by crafting the raw extension bytes.
	ext := pkixExtensionForIPMask(t, net.IPv4(10, 0, 0, 0), net.IPv4Mask(255, 0, 255, 0))
	rootKey := mustKey()
	root := makeCert(certSpec{subject: "root", isCA: true}, nil, rootKey, rootKey)
	intKey := mustKey()
	intermediate := makeManualCert("ca", true, ext, root, rootKey, intKey)

	_, _, err := parseNameConstraints(intermediate)
	if err == nil {
		t.Fatal("non-contiguous mask must be rejected")
	}

	// Sanity: well-formed direct struct path still works.
	if err := enforceCertificate(leaf, nc); err != nil {
		t.Fatalf("direct enforcement failed: %v", err)
	}
}

// TestMalformedSANValues exercises the error branches for names that cannot
// be parsed, and exercises the lenient parser directly.
func TestMalformedSANValues(t *testing.T) {
	rootKey := mustKey()
	root := makeCert(certSpec{subject: "root", isCA: true}, nil, rootKey, rootKey)
	intKey := mustKey()
	intermediate := makeCert(certSpec{
		subject: "ca",
		isCA:    true,
		constraints: &testConstraints{
			permittedDNS:   []string{"example.com"},
			permittedEmail: []string{"example.com"},
		},
	}, root, rootKey, intKey)

	leaf := makeCert(certSpec{
		subject: "bad-mail",
		emails:  []string{"no-at-sign"},
	}, intermediate, intKey, mustKey())
	err := VerifyChain([]*x509.Certificate{leaf, intermediate, root})
	if err == nil {
		t.Fatal("malformed email SAN must error")
	}
	if reasonOf(t, err) != ReasonMalformedName {
		t.Fatalf("got %v", err)
	}

	// The lenient parser round-trips a normal certificate and keeps its SANs.
	parsed, err := parseCertificateAny(leaf.Raw)
	if err != nil {
		t.Fatalf("lenient parse failed: %v", err)
	}
	if len(parsed.EmailAddresses) != 1 || parsed.EmailAddresses[0] != "no-at-sign" {
		t.Fatalf("SAN not preserved: %+v", parsed.EmailAddresses)
	}

	if err := VerifyChain(nil); err == nil {
		t.Fatal("nil chain must error")
	}
	if err := VerifyPeerCertificate([][]byte{{0x30, 0x00}}, nil); err == nil {
		t.Fatal("garbage raw cert must error")
	}
}

// TestErrorStringAndReasons checks the error's user-facing message.
func TestErrorStringAndReasons(t *testing.T) {
	e := &ConstraintError{Reason: ReasonExcluded, Detail: "boom"}
	if e.Error() != "nameconstraint: boom" {
		t.Fatalf("unexpected message: %q", e.Error())
	}
	plain := &ConstraintError{}
	if plain.Error() == "" {
		t.Fatal("default error message should be non-empty")
	}
}
