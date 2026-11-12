package netsec

import (
	"net/netip"
	"testing"
	"time"
)

func testPolicy() IssuerPolicy {
	return IssuerPolicy{
		AllowedDomainSuffixes: map[string][]string{
			"team-a": {"team-a.svc.cluster.local", "example.com"},
			"team-b": {"team-b.svc.cluster.local"},
		},
		AllowedEmailDomains: map[string][]string{
			"team-a": {"example.com"},
		},
		AllowedIPPrefixes: map[string][]netip.Prefix{
			"team-a": {netip.MustParsePrefix("10.0.0.0/8")},
		},
		TrustDomains: map[string][]string{
			"team-a": {"example.org"},
		},
		AllowedURIPrefixes: map[string][]string{
			"team-a": {"https://example.com/"},
		},
		ForbidCA:             true,
		MaxDuration:          90 * 24 * time.Hour,
		MinRSAKeySize:        2048,
		AllowedECDSACurves:   []string{CurveP256, CurveP384},
		AllowedKeyAlgorithms: []string{KeyAlgorithmRSA, KeyAlgorithmECDSA},
		AllowedKeyUsages:     []string{ExtKeyUsageServerAuth, ExtKeyUsageClientAuth},
	}
}

func testRequest() Request {
	csr := CSRContent{
		CommonName:     "api.team-a.svc.cluster.local",
		DNSNames:       []string{"api.team-a.svc.cluster.local", "www.example.com"},
		IPAddresses:    []netip.Addr{netip.MustParseAddr("10.1.2.3")},
		URIs:           []string{"spiffe://example.org/ns/team-a/sa/api"},
		EmailAddresses: []string{"ops@example.com"},
		KeyUsages:      []string{ExtKeyUsageServerAuth},
		IsCA:           false,
		KeyAlgorithm:   KeyAlgorithmRSA,
		KeySize:        2048,
	}
	return Request{
		Requester: Requester{
			Username:  "system:serviceaccount:team-a:api",
			Groups:    []string{"system:serviceaccounts"},
			Namespace: "team-a",
		},
		CSR: csr,
		Declaration: CertificateDeclaration{
			CommonName:     csr.CommonName,
			DNSNames:       csr.DNSNames,
			IPAddresses:    csr.IPAddresses,
			URIs:           csr.URIs,
			EmailAddresses: csr.EmailAddresses,
			KeyUsages:      csr.KeyUsages,
			IsCA:           csr.IsCA,
			Duration:       30 * 24 * time.Hour,
		},
	}
}

func hasCode(d Decision, code string) bool {
	for _, v := range d.Violations {
		if v.Code == code {
			return true
		}
	}
	return false
}

func TestEvaluateCompliant(t *testing.T) {
	d := Evaluate(testPolicy(), testRequest())
	if !d.Allowed {
		t.Fatalf("expected request to be allowed, got violations: %v", d.Violations)
	}
}

func TestEvaluateSuffixImpersonation(t *testing.T) {
	req := testRequest()
	// "notexample.com" ends with the string "example.com" but sits outside
	// the label boundary, so it must not inherit the suffix grant.
	req.CSR.DNSNames = append(req.CSR.DNSNames, "notexample.com")
	req.Declaration.DNSNames = append(req.Declaration.DNSNames, "notexample.com")

	d := Evaluate(testPolicy(), req)
	if d.Allowed || !hasCode(d, ViolationSuffixNotAllowed) {
		t.Fatalf("expected SuffixNotAllowed, got: %+v", d)
	}
}

func TestEvaluateIsCAForbidden(t *testing.T) {
	req := testRequest()
	req.CSR.IsCA = true
	req.Declaration.IsCA = true

	d := Evaluate(testPolicy(), req)
	if d.Allowed || !hasCode(d, ViolationIsCAForbidden) {
		t.Fatalf("expected IsCAForbidden, got: %+v", d)
	}
}

func TestEvaluateCrossNamespaceSAN(t *testing.T) {
	req := testRequest()
	// A service name belonging to team-b injected into a team-a request.
	req.CSR.DNSNames = append(req.CSR.DNSNames, "db.team-b.svc.cluster.local")
	req.Declaration.DNSNames = append(req.Declaration.DNSNames, "db.team-b.svc.cluster.local")

	d := Evaluate(testPolicy(), req)
	if d.Allowed || !hasCode(d, ViolationSuffixNotAllowed) {
		t.Fatalf("expected cross-namespace SAN to be rejected, got: %+v", d)
	}
}

func TestEvaluateRSA1024(t *testing.T) {
	req := testRequest()
	req.CSR.KeySize = 1024

	d := Evaluate(testPolicy(), req)
	if d.Allowed || !hasCode(d, ViolationRSAKeyTooShort) {
		t.Fatalf("expected RSAKeyTooShort, got: %+v", d)
	}
}

func TestEvaluateCSRTamperingMismatch(t *testing.T) {
	t.Run("extra DNS SAN only in CSR", func(t *testing.T) {
		req := testRequest()
		req.CSR.DNSNames = append(req.CSR.DNSNames, "stolen.team-b.svc.cluster.local")
		d := Evaluate(testPolicy(), req)
		if d.Allowed || !hasCode(d, ViolationCSRMismatch) {
			t.Fatalf("expected CSRMismatch, got: %+v", d)
		}
	})

	t.Run("isCA flipped only in CSR", func(t *testing.T) {
		req := testRequest()
		req.CSR.IsCA = true
		d := Evaluate(testPolicy(), req)
		if d.Allowed || !hasCode(d, ViolationCSRMismatch) {
			t.Fatalf("expected CSRMismatch, got: %+v", d)
		}
	})

	t.Run("key usage escalated only in CSR", func(t *testing.T) {
		req := testRequest()
		req.CSR.KeyUsages = []string{ExtKeyUsageServerAuth, ExtKeyUsageClientAuth}
		d := Evaluate(testPolicy(), req)
		if d.Allowed || !hasCode(d, ViolationCSRMismatch) {
			t.Fatalf("expected CSRMismatch, got: %+v", d)
		}
	})

	t.Run("extra URI SAN only in CSR", func(t *testing.T) {
		req := testRequest()
		req.CSR.URIs = append(req.CSR.URIs, "spiffe://example.org/ns/team-a/sa/backdoor")
		d := Evaluate(testPolicy(), req)
		if d.Allowed || !hasCode(d, ViolationCSRMismatch) {
			t.Fatalf("expected CSRMismatch, got: %+v", d)
		}
	})

	t.Run("IP SAN mismatch", func(t *testing.T) {
		req := testRequest()
		req.CSR.IPAddresses = append(req.CSR.IPAddresses, netip.MustParseAddr("10.9.9.9"))
		d := Evaluate(testPolicy(), req)
		if d.Allowed || !hasCode(d, ViolationCSRMismatch) {
			t.Fatalf("expected CSRMismatch, got: %+v", d)
		}
	})
}

func TestEvaluateWildcards(t *testing.T) {
	cases := []struct {
		name      string
		dns       string
		wantCode  string
		wantAllow bool
	}{
		{"leftmost wildcard allowed", "*.example.com", "", true},
		{"leftmost wildcard on cluster suffix", "*.team-a.svc.cluster.local", "", true},
		{"multi-level wildcard rejected", "*.*.example.com", ViolationInvalidDNSName, false},
		{"partial-label wildcard rejected", "www*.example.com", ViolationInvalidDNSName, false},
		{"bare wildcard rejected", "*", ViolationInvalidDNSName, false},
		{"wildcard outside suffix rejected", "*.evil.com", ViolationSuffixNotAllowed, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := testRequest()
			req.CSR.DNSNames = []string{tc.dns}
			req.Declaration.DNSNames = []string{tc.dns}
			d := Evaluate(testPolicy(), req)
			if tc.wantAllow && !d.Allowed {
				t.Fatalf("expected allow, got %+v", d.Violations)
			}
			if !tc.wantAllow && (d.Allowed || !hasCode(d, tc.wantCode)) {
				t.Fatalf("expected %s, got %+v", tc.wantCode, d)
			}
		})
	}
}

func TestEvaluateSPIFFE(t *testing.T) {
	t.Run("untrusted trust domain", func(t *testing.T) {
		req := testRequest()
		req.CSR.URIs = []string{"spiffe://evil.org/ns/team-a/sa/api"}
		req.Declaration.URIs = req.CSR.URIs
		d := Evaluate(testPolicy(), req)
		if d.Allowed || !hasCode(d, ViolationSPIFFETrustDomain) {
			t.Fatalf("expected SPIFFETrustDomain, got %+v", d)
		}
	})

	t.Run("malformed SPIFFE ID with query", func(t *testing.T) {
		req := testRequest()
		req.CSR.URIs = []string{"spiffe://example.org/path?x=1"}
		req.Declaration.URIs = req.CSR.URIs
		d := Evaluate(testPolicy(), req)
		if d.Allowed || !hasCode(d, ViolationSPIFFEInvalid) {
			t.Fatalf("expected SPIFFEInvalid, got %+v", d)
		}
	})

	t.Run("malformed SPIFFE ID with port", func(t *testing.T) {
		req := testRequest()
		req.CSR.URIs = []string{"spiffe://example.org:8443/path"}
		req.Declaration.URIs = req.CSR.URIs
		d := Evaluate(testPolicy(), req)
		if d.Allowed || !hasCode(d, ViolationSPIFFEInvalid) {
			t.Fatalf("expected SPIFFEInvalid, got %+v", d)
		}
	})
}

func TestEvaluateOtherURIs(t *testing.T) {
	t.Run("allowed prefix", func(t *testing.T) {
		req := testRequest()
		req.CSR.URIs = []string{"https://example.com/app/id"}
		req.Declaration.URIs = req.CSR.URIs
		d := Evaluate(testPolicy(), req)
		if !d.Allowed {
			t.Fatalf("expected allow, got %+v", d.Violations)
		}
	})

	t.Run("disallowed URI", func(t *testing.T) {
		req := testRequest()
		req.CSR.URIs = []string{"https://evil.com/app/id"}
		req.Declaration.URIs = req.CSR.URIs
		d := Evaluate(testPolicy(), req)
		if d.Allowed || !hasCode(d, ViolationURINotAllowed) {
			t.Fatalf("expected URINotAllowed, got %+v", d)
		}
	})
}

func TestEvaluateKeyPolicy(t *testing.T) {
	t.Run("ECDSA disallowed curve", func(t *testing.T) {
		req := testRequest()
		req.CSR.KeyAlgorithm = KeyAlgorithmECDSA
		req.CSR.ECDSACurve = CurveP521
		req.CSR.KeySize = 0
		d := Evaluate(testPolicy(), req)
		if d.Allowed || !hasCode(d, ViolationECDSACurveNotAllowed) {
			t.Fatalf("expected ECDSACurveNotAllowed, got %+v", d)
		}
	})

	t.Run("ECDSA allowed curve", func(t *testing.T) {
		req := testRequest()
		req.CSR.KeyAlgorithm = KeyAlgorithmECDSA
		req.CSR.ECDSACurve = CurveP256
		req.CSR.KeySize = 0
		d := Evaluate(testPolicy(), req)
		if !d.Allowed {
			t.Fatalf("expected allow, got %+v", d.Violations)
		}
	})

	t.Run("Ed25519 not allowed", func(t *testing.T) {
		req := testRequest()
		req.CSR.KeyAlgorithm = KeyAlgorithmEd25519
		req.CSR.KeySize = 0
		d := Evaluate(testPolicy(), req)
		if d.Allowed || !hasCode(d, ViolationKeyAlgorithmNotAllowed) {
			t.Fatalf("expected KeyAlgorithmNotAllowed, got %+v", d)
		}
	})

	t.Run("disallowed key usage", func(t *testing.T) {
		req := testRequest()
		req.CSR.KeyUsages = []string{"codeSigning"}
		req.Declaration.KeyUsages = []string{"codeSigning"}
		d := Evaluate(testPolicy(), req)
		if d.Allowed || !hasCode(d, ViolationKeyUsageNotAllowed) {
			t.Fatalf("expected KeyUsageNotAllowed, got %+v", d)
		}
	})
}

func TestEvaluateDuration(t *testing.T) {
	req := testRequest()
	req.Declaration.Duration = 365 * 24 * time.Hour
	d := Evaluate(testPolicy(), req)
	if d.Allowed || !hasCode(d, ViolationDurationTooLong) {
		t.Fatalf("expected DurationTooLong, got %+v", d)
	}
}

func TestEvaluateIPAndEmail(t *testing.T) {
	t.Run("IP outside granted prefixes", func(t *testing.T) {
		req := testRequest()
		req.CSR.IPAddresses = []netip.Addr{netip.MustParseAddr("8.8.8.8")}
		req.Declaration.IPAddresses = req.CSR.IPAddresses
		d := Evaluate(testPolicy(), req)
		if d.Allowed || !hasCode(d, ViolationIPNotAllowed) {
			t.Fatalf("expected IPNotAllowed, got %+v", d)
		}
	})

	t.Run("email outside granted domains", func(t *testing.T) {
		req := testRequest()
		req.CSR.EmailAddresses = []string{"ops@evil.com"}
		req.Declaration.EmailAddresses = req.CSR.EmailAddresses
		d := Evaluate(testPolicy(), req)
		if d.Allowed || !hasCode(d, ViolationEmailDomainNotAllowed) {
			t.Fatalf("expected EmailDomainNotAllowed, got %+v", d)
		}
	})

	t.Run("email suffix impersonation rejected", func(t *testing.T) {
		req := testRequest()
		req.CSR.EmailAddresses = []string{"ops@notexample.com"}
		req.Declaration.EmailAddresses = req.CSR.EmailAddresses
		d := Evaluate(testPolicy(), req)
		if d.Allowed || !hasCode(d, ViolationEmailDomainNotAllowed) {
			t.Fatalf("expected EmailDomainNotAllowed, got %+v", d)
		}
	})
}

func TestEvaluateUnknownNamespace(t *testing.T) {
	req := testRequest()
	req.Requester.Namespace = "no-grants"
	d := Evaluate(testPolicy(), req)
	if d.Allowed || !hasCode(d, ViolationUnknownNamespace) {
		t.Fatalf("expected UnknownNamespace, got %+v", d)
	}
}

func TestHasLabelSuffixBoundary(t *testing.T) {
	if !hasLabelSuffix("api.example.com", "example.com") {
		t.Fatal("exact-boundary subdomain should match")
	}
	if hasLabelSuffix("notexample.com", "example.com") {
		t.Fatal("suffix without label boundary must not match")
	}
	if !hasLabelSuffix("example.com", "example.com") {
		t.Fatal("apex should match its own suffix")
	}
}

func TestPolicyValidate(t *testing.T) {
	bad := testPolicy()
	bad.MinRSAKeySize = 1024
	if err := bad.Validate(); err == nil {
		t.Fatal("expected policy with weak MinRSAKeySize to fail validation")
	}
}
