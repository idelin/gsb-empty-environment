package spiffefed_test

import (
	"crypto/x509"
	"errors"
	"testing"
	"time"

	spiffefed "formal.local/w27-6accbac653"
)

func TestX509SVIDFederationIsolation(t *testing.T) {
	t.Parallel()
	localCA := newTestAuthority(t, "local-ca")
	externalCA := newTestAuthority(t, "external-ca")

	localBundle, err := spiffefed.NewTrustBundle("local.example", localCA.cert)
	if err != nil {
		t.Fatalf("local bundle: %v", err)
	}
	externalBundle, err := spiffefed.NewTrustBundle("external.example", externalCA.cert)
	if err != nil {
		t.Fatalf("external bundle: %v", err)
	}
	bundles, err := spiffefed.NewBundleSet(localBundle, externalBundle)
	if err != nil {
		t.Fatalf("bundle set: %v", err)
	}
	policy := mustPolicy(t, "local.example", map[string]spiffefed.ServicePolicy{
		"core":    {FederatesWith: []string{"external.example"}},
		"private": {},
	})
	verifier, err := spiffefed.NewX509SVIDVerifier(bundles, policy)
	if err != nil {
		t.Fatalf("verifier: %v", err)
	}

	t.Run("local certificate", func(t *testing.T) {
		cert := makeTestSVID(t, localCA, []string{"spiffe://local.example/ns/core/sa/proxy"}, nil)
		id, err := verifier.Verify(cert, spiffefed.X509VerifyOptions{TargetService: "core"})
		if err != nil {
			t.Fatalf("verify local SVID: %v", err)
		}
		if id.String() != "spiffe://local.example/ns/core/sa/proxy" {
			t.Fatalf("unexpected ID %q", id)
		}
	})

	t.Run("explicitly federated certificate", func(t *testing.T) {
		cert := makeTestSVID(t, externalCA, []string{"spiffe://external.example/ns/app/sa/client"}, nil)
		if _, err := verifier.Verify(cert, spiffefed.X509VerifyOptions{TargetService: "core"}); err != nil {
			t.Fatalf("verify federated SVID: %v", err)
		}
	})

	t.Run("federated CA cannot issue local identity", func(t *testing.T) {
		cert := makeTestSVID(t, externalCA, []string{"spiffe://local.example/ns/core/sa/proxy"}, nil)
		if _, err := verifier.Verify(cert, spiffefed.X509VerifyOptions{TargetService: "core"}); !errors.Is(err, spiffefed.ErrCertificateNotTrusted) {
			t.Fatalf("cross-domain forgery error = %v, want ErrCertificateNotTrusted", err)
		}
	})

	t.Run("federation must be explicit for target service", func(t *testing.T) {
		cert := makeTestSVID(t, externalCA, []string{"spiffe://external.example/ns/app/sa/client"}, nil)
		if _, err := verifier.Verify(cert, spiffefed.X509VerifyOptions{TargetService: "private"}); !errors.Is(err, spiffefed.ErrFederationDenied) {
			t.Fatalf("error = %v, want ErrFederationDenied", err)
		}
	})

	t.Run("unknown target service", func(t *testing.T) {
		cert := makeTestSVID(t, localCA, []string{"spiffe://local.example/ns/core/sa/proxy"}, nil)
		if _, err := verifier.Verify(cert, spiffefed.X509VerifyOptions{TargetService: "missing"}); !errors.Is(err, spiffefed.ErrUnknownService) {
			t.Fatalf("error = %v, want ErrUnknownService", err)
		}
	})
}

func TestX509SVIDRejectsInvalidProfiles(t *testing.T) {
	t.Parallel()
	ca := newTestAuthority(t, "local-ca")
	bundle, err := spiffefed.NewTrustBundle("local.example", ca.cert)
	if err != nil {
		t.Fatalf("bundle: %v", err)
	}
	bundles, err := spiffefed.NewBundleSet(bundle)
	if err != nil {
		t.Fatalf("bundle set: %v", err)
	}
	policy := mustPolicy(t, "local.example", map[string]spiffefed.ServicePolicy{"core": {}})
	verifier, err := spiffefed.NewX509SVIDVerifier(bundles, policy)
	if err != nil {
		t.Fatalf("verifier: %v", err)
	}
	opts := spiffefed.X509VerifyOptions{TargetService: "core", Now: time.Now()}

	cases := []struct {
		name   string
		uris   []string
		mutate func(*x509.Certificate)
	}{
		{
			name: "two URI SANs",
			uris: []string{"spiffe://local.example/ns/core/sa/proxy", "spiffe://local.example/ns/core/sa/admin"},
		},
		{
			name: "DNS SAN",
			uris: []string{"spiffe://local.example/ns/core/sa/proxy"},
			mutate: func(cert *x509.Certificate) {
				cert.DNSNames = []string{"core.local.example"}
			},
		},
		{
			name: "CA leaf",
			uris: []string{"spiffe://local.example/ns/core/sa/proxy"},
			mutate: func(cert *x509.Certificate) {
				cert.IsCA = true
			},
		},
		{
			name: "certificate signing key usage",
			uris: []string{"spiffe://local.example/ns/core/sa/proxy"},
			mutate: func(cert *x509.Certificate) {
				cert.KeyUsage |= x509.KeyUsageCertSign
			},
		},
		{
			name: "missing digital signature usage",
			uris: []string{"spiffe://local.example/ns/core/sa/proxy"},
			mutate: func(cert *x509.Certificate) {
				cert.KeyUsage = x509.KeyUsageKeyEncipherment
			},
		},
		{
			name: "code signing EKU",
			uris: []string{"spiffe://local.example/ns/core/sa/proxy"},
			mutate: func(cert *x509.Certificate) {
				cert.ExtKeyUsage = []x509.ExtKeyUsage{x509.ExtKeyUsageCodeSigning}
			},
		},
		{
			name: "uppercase trust domain",
			uris: []string{"spiffe://LOCAL.EXAMPLE/ns/core/sa/proxy"},
		},
		{
			name: "dot-segment path",
			uris: []string{"spiffe://local.example/ns/../core/admin"},
		},
		{
			name: "percent-encoded dot-segment path",
			uris: []string{"spiffe://local.example/ns/%2e%2e/core/admin"},
		},
		{
			name: "empty path segment",
			uris: []string{"spiffe://local.example/ns//core/admin"},
		},
		{
			name: "query in SPIFFE URI",
			uris: []string{"spiffe://local.example/ns/core/admin?role=root"},
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			cert := makeTestSVID(t, ca, tc.uris, tc.mutate)
			if _, err := verifier.Verify(cert, opts); !errors.Is(err, spiffefed.ErrInvalidCertificate) {
				t.Fatalf("error = %v, want ErrInvalidCertificate", err)
			}
		})
	}
}

func TestBundleSetIsDomainIsolated(t *testing.T) {
	t.Parallel()
	a := newTestAuthority(t, "a")
	b := newTestAuthority(t, "b")
	bundleA, err := spiffefed.NewTrustBundle("a.example", a.cert)
	if err != nil {
		t.Fatalf("bundle A: %v", err)
	}
	bundleB, err := spiffefed.NewTrustBundle("b.example", b.cert)
	if err != nil {
		t.Fatalf("bundle B: %v", err)
	}
	set, err := spiffefed.NewBundleSet(bundleA, bundleB)
	if err != nil {
		t.Fatalf("bundle set: %v", err)
	}
	if _, ok := set.Bundle("missing.example"); ok {
		t.Fatal("unexpected bundle fallback")
	}
	got, ok := set.Bundle("a.example")
	if !ok || got.TrustDomain() != "a.example" {
		t.Fatal("domain bundle lookup was not exact")
	}
	if _, err := spiffefed.NewBundleSet(bundleA, bundleA); err == nil {
		t.Fatal("duplicate domain bundle should be rejected")
	}
}
