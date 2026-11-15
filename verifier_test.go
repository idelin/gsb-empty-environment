package netsec

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"testing"
	"time"
)

func TestVerifyBuildsOneIntersectedChain(t *testing.T) {
	now := time.Date(2026, 11, 15, 12, 0, 0, 0, time.UTC)
	notBefore := now.Add(-time.Hour)
	notAfter := now.Add(time.Hour)

	rootPublic, rootPrivate := generateKey(t)
	intermediatePublic, intermediatePrivate := generateKey(t)
	leafPublic, _ := generateKey(t)

	intermediate := issue(t, "root", rootPrivate, intermediatePublic, 10,
		[]string{"read", "write", "admin"},
		[]string{"/data", "/public"},
		2, notBefore, notAfter)
	leaf := issue(t, "intermediate", intermediatePrivate, leafPublic, 20,
		[]string{"write", "read"},
		[]string{"/private", "/data/project"},
		0, notBefore, notAfter)

	roots := []TrustRoot{{ID: "root", PublicKey: rootPublic}}
	certificates := []Certificate{intermediate, leaf}

	chain, err := Verify(certificates, roots, Request{
		Operation:        "read",
		ResourcePath:     "/data/project/report",
		SubjectPublicKey: leafPublic,
	}, now, nil)
	if err != nil {
		t.Fatalf("Verify() error = %v", err)
	}
	if len(chain) != 2 {
		t.Fatalf("Verify() returned %d certificates, want 2", len(chain))
	}
	if chain[0].Serial != 10 || chain[1].Serial != 20 {
		t.Fatalf("Verify() chain serials = [%d %d], want [10 20]", chain[0].Serial, chain[1].Serial)
	}
	if err := VerifyCertificateSignature(chain[0], rootPublic); err != nil {
		t.Fatalf("first chain signature: %v", err)
	}
	if err := VerifyCertificateSignature(chain[1], intermediatePublic); err != nil {
		t.Fatalf("second chain signature: %v", err)
	}

	denied := []Request{
		{Operation: "admin", ResourcePath: "/data/project/report", SubjectPublicKey: leafPublic},
		{Operation: "read", ResourcePath: "/public/file", SubjectPublicKey: leafPublic},
		{Operation: "read", ResourcePath: "/database", SubjectPublicKey: leafPublic},
		{Operation: "read", ResourcePath: "/data/project2", SubjectPublicKey: leafPublic},
	}
	for _, request := range denied {
		_, err := Verify(certificates, roots, request, now, nil)
		if !errors.Is(err, ErrNoTrustedPath) {
			t.Errorf("Verify(%q %q) error = %v, want ErrNoTrustedPath", request.Operation, request.ResourcePath, err)
		}
	}

	chain, err = Verify(certificates, roots, Request{
		Operation:    "admin",
		ResourcePath: "/data",
	}, now, nil)
	if err != nil {
		t.Fatalf("Verify() without target error = %v", err)
	}
	if len(chain) != 1 || chain[0].Serial != 10 {
		t.Fatalf("Verify() without target = %v, want intermediate alone", chainSerials(chain))
	}
}

func TestVerifyDoesNotSpliceSiblingPermissions(t *testing.T) {
	now := time.Date(2026, 11, 15, 12, 0, 0, 0, time.UTC)
	notBefore := now.Add(-time.Hour)
	notAfter := now.Add(time.Hour)

	rootPublic, rootPrivate := generateKey(t)
	delegatePublic, delegatePrivate := generateKey(t)
	leafPublic, _ := generateKey(t)

	readOnly := issue(t, "root", rootPrivate, delegatePublic, 1,
		[]string{"read"}, []string{"/x"}, 1, notBefore, notAfter)
	writeOnly := issue(t, "root", rootPrivate, delegatePublic, 2,
		[]string{"write"}, []string{"/y"}, 1, notBefore, notAfter)
	leaf := issue(t, "delegate", delegatePrivate, leafPublic, 3,
		[]string{"read", "write"}, []string{"/x", "/y"}, 0, notBefore, notAfter)

	roots := []TrustRoot{{ID: "root", PublicKey: rootPublic}}
	certificates := []Certificate{readOnly, writeOnly, leaf}

	_, err := Verify(certificates, roots, Request{
		Operation:        "read",
		ResourcePath:     "/y/file",
		SubjectPublicKey: leafPublic,
	}, now, nil)
	if !errors.Is(err, ErrNoTrustedPath) {
		t.Fatalf("spliced request error = %v, want ErrNoTrustedPath", err)
	}

	chain, err := Verify(certificates, roots, Request{
		Operation:        "write",
		ResourcePath:     "/y/file",
		SubjectPublicKey: leafPublic,
	}, now, nil)
	if err != nil {
		t.Fatalf("write-only chain error = %v", err)
	}
	if got := chainSerials(chain); len(got) != 2 || got[0] != 2 || got[1] != 3 {
		t.Fatalf("write-only chain = %v, want [2 3]", got)
	}
}

func TestVerifyPrefixesUsePathComponents(t *testing.T) {
	now := time.Date(2026, 11, 15, 12, 0, 0, 0, time.UTC)
	notBefore := now.Add(-time.Hour)
	notAfter := now.Add(time.Hour)

	rootPublic, rootPrivate := generateKey(t)
	leafPublic, _ := generateKey(t)
	leaf := issue(t, "root", rootPrivate, leafPublic, 1,
		[]string{"read"}, []string{"/data", "/safe/"}, 0, notBefore, notAfter)

	roots := []TrustRoot{{ID: "root", PublicKey: rootPublic}}
	certificates := []Certificate{leaf}

	allowed := []string{"/data", "/data/child", "/safe", "/safe/"}
	for _, resource := range allowed {
		_, err := Verify(certificates, roots, Request{
			Operation:        "read",
			ResourcePath:     resource,
			SubjectPublicKey: leafPublic,
		}, now, nil)
		if err != nil {
			t.Errorf("Verify(%q) error = %v, want success", resource, err)
		}
	}

	denied := []string{"/database", "/data2", "/safely", "/data/../secret"}
	for _, resource := range denied {
		_, err := Verify(certificates, roots, Request{
			Operation:        "read",
			ResourcePath:     resource,
			SubjectPublicKey: leafPublic,
		}, now, nil)
		if !errors.Is(err, ErrNoTrustedPath) && !errors.Is(err, ErrInvalidRequest) {
			t.Errorf("Verify(%q) error = %v, want rejection", resource, err)
		}
	}
}

func TestVerifyEnforcesRemainingDelegationDepth(t *testing.T) {
	now := time.Date(2026, 11, 15, 12, 0, 0, 0, time.UTC)
	notBefore := now.Add(-time.Hour)
	notAfter := now.Add(time.Hour)

	rootPublic, rootPrivate := generateKey(t)
	firstPublic, firstPrivate := generateKey(t)
	secondPublic, secondPrivate := generateKey(t)
	thirdPublic, _ := generateKey(t)

	roots := []TrustRoot{{ID: "root", PublicKey: rootPublic}}

	t.Run("parent exhausted", func(t *testing.T) {
		first := issue(t, "root", rootPrivate, firstPublic, 1,
			[]string{"read"}, []string{"/data"}, 1, notBefore, notAfter)
		second := issue(t, "first", firstPrivate, secondPublic, 2,
			[]string{"read"}, []string{"/data"}, 5, notBefore, notAfter)
		third := issue(t, "second", secondPrivate, thirdPublic, 3,
			[]string{"read"}, []string{"/data"}, 0, notBefore, notAfter)

		_, err := Verify([]Certificate{first, second, third}, roots, Request{
			Operation:        "read",
			ResourcePath:     "/data/file",
			SubjectPublicKey: thirdPublic,
		}, now, nil)
		if !errors.Is(err, ErrNoTrustedPath) {
			t.Fatalf("depth exhaustion error = %v, want ErrNoTrustedPath", err)
		}
	})

	t.Run("extra parent depth reaches leaf", func(t *testing.T) {
		first := issue(t, "root", rootPrivate, firstPublic, 11,
			[]string{"read"}, []string{"/data"}, 2, notBefore, notAfter)
		second := issue(t, "first", firstPrivate, secondPublic, 12,
			[]string{"read"}, []string{"/data"}, 5, notBefore, notAfter)
		third := issue(t, "second", secondPrivate, thirdPublic, 13,
			[]string{"read"}, []string{"/data"}, 0, notBefore, notAfter)

		chain, err := Verify([]Certificate{first, second, third}, roots, Request{
			Operation:        "read",
			ResourcePath:     "/data/file",
			SubjectPublicKey: thirdPublic,
		}, now, nil)
		if err != nil {
			t.Fatalf("depth=2 chain error = %v", err)
		}
		if got := chainSerials(chain); len(got) != 3 {
			t.Fatalf("depth=2 chain = %v, want three certificates", got)
		}
	})

	t.Run("zero depth cannot issue", func(t *testing.T) {
		first := issue(t, "root", rootPrivate, firstPublic, 21,
			[]string{"read"}, []string{"/data"}, 0, notBefore, notAfter)
		second := issue(t, "first", firstPrivate, secondPublic, 22,
			[]string{"read"}, []string{"/data"}, 0, notBefore, notAfter)

		_, err := Verify([]Certificate{first, second}, roots, Request{
			Operation:        "read",
			ResourcePath:     "/data/file",
			SubjectPublicKey: secondPublic,
		}, now, nil)
		if !errors.Is(err, ErrNoTrustedPath) {
			t.Fatalf("zero-depth error = %v, want ErrNoTrustedPath", err)
		}
	})
}

func TestVerifyValidityIntervalIsHalfOpen(t *testing.T) {
	notBefore := time.Date(2026, 11, 15, 12, 0, 0, 123, time.UTC)
	notAfter := notBefore.Add(time.Hour)

	rootPublic, rootPrivate := generateKey(t)
	leafPublic, _ := generateKey(t)
	leaf := issue(t, "root", rootPrivate, leafPublic, 1,
		[]string{"read"}, []string{"/data"}, 1, notBefore, notAfter)

	roots := []TrustRoot{{ID: "root", PublicKey: rootPublic}}
	certificates := []Certificate{leaf}
	request := Request{Operation: "read", ResourcePath: "/data", SubjectPublicKey: leafPublic}

	cases := []struct {
		name string
		at   time.Time
		ok   bool
	}{
		{name: "before", at: notBefore.Add(-time.Nanosecond), ok: false},
		{name: "left endpoint", at: notBefore, ok: true},
		{name: "inside", at: notAfter.Add(-time.Nanosecond), ok: true},
		{name: "right endpoint", at: notAfter, ok: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Verify(certificates, roots, request, tc.at, nil)
			if tc.ok && err != nil {
				t.Fatalf("Verify(at=%s) error = %v", tc.at, err)
			}
			if !tc.ok && !errors.Is(err, ErrNoTrustedPath) {
				t.Fatalf("Verify(at=%s) error = %v, want ErrNoTrustedPath", tc.at, err)
			}
		})
	}
}

func TestVerifyLegalCrossSigningAndPairedRevocation(t *testing.T) {
	now := time.Date(2026, 11, 15, 12, 0, 0, 0, time.UTC)
	notBefore := now.Add(-time.Hour)
	notAfter := now.Add(time.Hour)

	rootAPublic, rootAPrivate := generateKey(t)
	rootBPublic, rootBPrivate := generateKey(t)
	subjectPublic, _ := generateKey(t)

	issuedByA := issue(t, "root-a", rootAPrivate, subjectPublic, 7,
		[]string{"read"}, []string{"/alpha"}, 0, notBefore, notAfter)
	issuedByB := issue(t, "root-b", rootBPrivate, subjectPublic, 7,
		[]string{"read"}, []string{"/beta"}, 0, notBefore, notAfter)

	roots := []TrustRoot{
		{ID: "root-a", PublicKey: rootAPublic},
		{ID: "root-b", PublicKey: rootBPublic},
	}
	certificates := []Certificate{issuedByA, issuedByB}

	beta := Request{Operation: "read", ResourcePath: "/beta/file", SubjectPublicKey: subjectPublic}
	alpha := Request{Operation: "read", ResourcePath: "/alpha/file", SubjectPublicKey: subjectPublic}

	chain, err := Verify(certificates, roots, beta, now, []Revocation{
		{IssuerID: "root-a", Serial: 7},
	})
	if err != nil {
		t.Fatalf("cross-signed beta error = %v", err)
	}
	if len(chain) != 1 || chain[0].IssuerID != "root-b" {
		t.Fatalf("beta chain issuer = %q, want root-b", chain[0].IssuerID)
	}

	_, err = Verify(certificates, roots, alpha, now, []Revocation{
		{IssuerID: "root-a", Serial: 7},
	})
	if !errors.Is(err, ErrNoTrustedPath) {
		t.Fatalf("revoked root-a issuance error = %v, want ErrNoTrustedPath", err)
	}

	_, err = Verify(certificates, roots, beta, now, []Revocation{
		{IssuerID: "root-a", Serial: 8},
	})
	if err != nil {
		t.Fatalf("non-matching revocation serial error = %v", err)
	}

	_, err = Verify(certificates, roots, Request{
		Operation:        "read",
		ResourcePath:     "/alpha/file",
		SubjectPublicKey: subjectPublic,
	}, now, []Revocation{{IssuerID: "root-b", Serial: 7}})
	if err != nil {
		t.Fatalf("root-b revocation should not revoke root-a issuance: %v", err)
	}
}

func TestVerifyRevokedIntermediateBreaksChain(t *testing.T) {
	now := time.Date(2026, 11, 15, 12, 0, 0, 0, time.UTC)
	notBefore := now.Add(-time.Hour)
	notAfter := now.Add(time.Hour)

	rootPublic, rootPrivate := generateKey(t)
	intermediatePublic, intermediatePrivate := generateKey(t)
	leafPublic, _ := generateKey(t)

	intermediate := issue(t, "root", rootPrivate, intermediatePublic, 5,
		[]string{"read"}, []string{"/data"}, 2, notBefore, notAfter)
	leaf := issue(t, "intermediate", intermediatePrivate, leafPublic, 6,
		[]string{"read"}, []string{"/data/file"}, 0, notBefore, notAfter)

	_, err := Verify([]Certificate{intermediate, leaf},
		[]TrustRoot{{ID: "root", PublicKey: rootPublic}},
		Request{Operation: "read", ResourcePath: "/data/file/1", SubjectPublicKey: leafPublic},
		now,
		[]Revocation{{IssuerID: "root", Serial: 5}})
	if !errors.Is(err, ErrNoTrustedPath) {
		t.Fatalf("revoked intermediate error = %v, want ErrNoTrustedPath", err)
	}
}

func TestVerifyRejectsUntrustedOrAmbiguousRoots(t *testing.T) {
	now := time.Date(2026, 11, 15, 12, 0, 0, 0, time.UTC)
	notBefore := now.Add(-time.Hour)
	notAfter := now.Add(time.Hour)

	rootPublic, rootPrivate := generateKey(t)
	wrongPublic, _ := generateKey(t)
	leafPublic, _ := generateKey(t)
	leaf := issue(t, "root", rootPrivate, leafPublic, 1,
		[]string{"read"}, []string{"/data"}, 0, notBefore, notAfter)

	_, err := Verify([]Certificate{leaf},
		[]TrustRoot{{ID: "root", PublicKey: wrongPublic}},
		Request{Operation: "read", ResourcePath: "/data", SubjectPublicKey: leafPublic},
		now, nil)
	if !errors.Is(err, ErrNoTrustedPath) {
		t.Fatalf("wrong root key error = %v, want ErrNoTrustedPath", err)
	}

	wrongIssuer := issue(t, "other-root", rootPrivate, leafPublic, 2,
		[]string{"read"}, []string{"/data"}, 0, notBefore, notAfter)
	_, err = Verify([]Certificate{wrongIssuer},
		[]TrustRoot{{ID: "root", PublicKey: rootPublic}},
		Request{Operation: "read", ResourcePath: "/data", SubjectPublicKey: leafPublic},
		now, nil)
	if !errors.Is(err, ErrNoTrustedPath) {
		t.Fatalf("untrusted issuer ID error = %v, want ErrNoTrustedPath", err)
	}

	_, err = Verify([]Certificate{leaf},
		[]TrustRoot{
			{ID: "root", PublicKey: rootPublic},
			{ID: "root", PublicKey: wrongPublic},
		},
		Request{Operation: "read", ResourcePath: "/data", SubjectPublicKey: leafPublic},
		now, nil)
	if !errors.Is(err, ErrInvalidTrustRoot) {
		t.Fatalf("conflicting root IDs error = %v, want ErrInvalidTrustRoot", err)
	}
}

func TestVerifyEnforcesCertificateLimit(t *testing.T) {
	_, err := Verify(make([]Certificate, MaxChainCertificates+1), nil, Request{
		Operation:    "read",
		ResourcePath: "/data",
	}, time.Now(), nil)
	if !errors.Is(err, ErrTooManyCertificates) {
		t.Fatalf("33-certificate error = %v, want ErrTooManyCertificates", err)
	}

	verifier := Verifier{MaxCertificates: 5}
	_, err = verifier.VerifyAt(make([]Certificate, 6), Request{
		Operation:    "read",
		ResourcePath: "/data",
	}, time.Now())
	if !errors.Is(err, ErrTooManyCertificates) {
		t.Fatalf("configured limit error = %v, want ErrTooManyCertificates", err)
	}
}

func TestVerifyAcceptsChainAtCertificateLimit(t *testing.T) {
	now := time.Date(2026, 11, 15, 12, 0, 0, 0, time.UTC)
	notBefore := now.Add(-time.Hour)
	notAfter := now.Add(time.Hour)

	rootPublic, rootPrivate := generateKey(t)
	certificates := make([]Certificate, 0, MaxChainCertificates)
	issuerPrivate := rootPrivate
	var leafPublic ed25519.PublicKey
	for i := 0; i < MaxChainCertificates; i++ {
		subjectPublic, subjectPrivate := generateKey(t)
		certificates = append(certificates, issue(t, "issuer", issuerPrivate, subjectPublic, uint64(i+1),
			[]string{"read"}, []string{"/data"}, uint32(MaxChainCertificates),
			notBefore, notAfter))
		issuerPrivate = subjectPrivate
		leafPublic = subjectPublic
	}
	certificates[0].IssuerID = "root"
	if err := SignCertificate(&certificates[0], rootPrivate); err != nil {
		t.Fatalf("resign first certificate: %v", err)
	}

	chain, err := Verify(certificates,
		[]TrustRoot{{ID: "root", PublicKey: rootPublic}},
		Request{Operation: "read", ResourcePath: "/data/file", SubjectPublicKey: leafPublic},
		now, nil)
	if err != nil {
		t.Fatalf("32-certificate chain error = %v", err)
	}
	if len(chain) != MaxChainCertificates {
		t.Fatalf("32-certificate chain length = %d", len(chain))
	}
}

func TestSigningBytesCoverEveryField(t *testing.T) {
	notBefore := time.Date(2026, 11, 15, 12, 0, 0, 0, time.UTC)
	notAfter := notBefore.Add(time.Hour)
	_, rootPrivate := generateKey(t)
	subjectPublic, _ := generateKey(t)
	certificate := issue(t, "root", rootPrivate, subjectPublic, 42,
		[]string{"read", "write"}, []string{"/data", "/safe"}, 4, notBefore, notAfter)

	message, err := certificate.SigningBytes()
	if err != nil {
		t.Fatalf("SigningBytes() error = %v", err)
	}
	again, err := certificate.SigningBytes()
	if err != nil {
		t.Fatalf("SigningBytes() second call error = %v", err)
	}
	if !bytes.Equal(message, again) {
		t.Fatal("SigningBytes() is not deterministic")
	}
	withoutSignature := certificate
	withoutSignature.Signature = nil
	withoutSignatureMessage, err := withoutSignature.SigningBytes()
	if err != nil {
		t.Fatalf("SigningBytes() without signature error = %v", err)
	}
	if !bytes.Equal(message, withoutSignatureMessage) {
		t.Fatal("SigningBytes() depends on Signature")
	}

	mutations := []struct {
		name   string
		mutate func(*Certificate)
	}{
		{name: "subject key", mutate: func(c *Certificate) { c.SubjectPublicKey[0] ^= 1 }},
		{name: "issuer", mutate: func(c *Certificate) { c.IssuerID += "x" }},
		{name: "serial", mutate: func(c *Certificate) { c.Serial++ }},
		{name: "operation", mutate: func(c *Certificate) { c.AllowedOperations = append(c.AllowedOperations, "delete") }},
		{name: "prefix", mutate: func(c *Certificate) { c.ResourcePrefixes = append(c.ResourcePrefixes, "/other") }},
		{name: "depth", mutate: func(c *Certificate) { c.MaxDelegationDepth++ }},
		{name: "not before", mutate: func(c *Certificate) { c.NotBefore = c.NotBefore.Add(time.Nanosecond) }},
		{name: "not after", mutate: func(c *Certificate) { c.NotAfter = c.NotAfter.Add(time.Nanosecond) }},
		{name: "signature", mutate: func(c *Certificate) { c.Signature[0] ^= 1 }},
	}

	for _, tc := range mutations {
		t.Run(tc.name, func(t *testing.T) {
			changed := cloneCertificate(certificate)
			tc.mutate(&changed)
			changedMessage, err := changed.SigningBytes()
			if err != nil {
				t.Fatalf("changed SigningBytes() error = %v", err)
			}
			if tc.name != "signature" && bytes.Equal(message, changedMessage) {
				t.Fatal("mutation did not change signing bytes")
			}
			if err := VerifyCertificateSignature(changed, mustPublic(rootPrivate)); err == nil {
				t.Fatal("mutation did not invalidate signature")
			}
		})
	}

	_, wrongPrivate := generateKey(t)
	if err := VerifyCertificateSignature(certificate, mustPublic(wrongPrivate)); !errors.Is(err, ErrInvalidSignature) {
		t.Fatalf("wrong issuer key error = %v, want ErrInvalidSignature", err)
	}
}

func TestCertificateSetCanonicalizationIsSemantic(t *testing.T) {
	notBefore := time.Date(2026, 11, 15, 12, 0, 0, 0, time.UTC)
	notAfter := notBefore.Add(time.Hour)
	rootPublic, rootPrivate := generateKey(t)
	subjectPublic, _ := generateKey(t)

	certificate := issue(t, "root", rootPrivate, subjectPublic, 1,
		[]string{"read", "write"}, []string{"/data", "/safe"}, 0, notBefore, notAfter)
	reordered := cloneCertificate(certificate)
	reordered.AllowedOperations = []string{"write", "read", "write"}
	reordered.ResourcePrefixes = []string{"/safe/", "/data", "/data"}

	if err := VerifyCertificateSignature(reordered, rootPublic); err != nil {
		t.Fatalf("set reordering/trailing slash invalidated signature: %v", err)
	}
}

func TestCanonicalResourcePath(t *testing.T) {
	valid := map[string]string{
		"/":        "/",
		"/data":    "/data",
		"/data/":   "/data",
		"/a/b/c/":  "/a/b/c",
		"/x.y/z-1": "/x.y/z-1",
	}
	for input, expected := range valid {
		actual, err := CanonicalResourcePath(input)
		if err != nil {
			t.Errorf("CanonicalResourcePath(%q) error = %v", input, err)
			continue
		}
		if actual != expected {
			t.Errorf("CanonicalResourcePath(%q) = %q, want %q", input, actual, expected)
		}
	}

	invalid := []string{"", "data", "//", "/data//child", "/data/../secret", "/data/./file", "/data\x00"}
	for _, input := range invalid {
		if _, err := CanonicalResourcePath(input); err == nil {
			t.Errorf("CanonicalResourcePath(%q) succeeded, want error", input)
		}
	}
}

func TestIntersectPrefixes(t *testing.T) {
	cases := []struct {
		name string
		a    []string
		b    []string
		want []string
	}{
		{name: "narrow child", a: []string{"/data"}, b: []string{"/data/project"}, want: []string{"/data/project"}},
		{name: "narrow parent", a: []string{"/data/project"}, b: []string{"/data"}, want: []string{"/data/project"}},
		{name: "similar but distinct", a: []string{"/data"}, b: []string{"/database"}, want: nil},
		{name: "branches", a: []string{"/a", "/b"}, b: []string{"/a/x", "/b/y"}, want: []string{"/a/x", "/b/y"}},
		{name: "covered prefix removed", a: []string{"/a", "/a/x"}, b: []string{"/a"}, want: []string{"/a"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := intersectPrefixes(tc.a, tc.b)
			if len(got) != len(tc.want) {
				t.Fatalf("intersectPrefixes() = %v, want %v", got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Fatalf("intersectPrefixes() = %v, want %v", got, tc.want)
				}
			}
		})
	}
}

func generateKey(t *testing.T) (ed25519.PublicKey, ed25519.PrivateKey) {
	t.Helper()
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("ed25519.GenerateKey() error = %v", err)
	}
	return publicKey, privateKey
}

func mustPublic(privateKey ed25519.PrivateKey) ed25519.PublicKey {
	return privateKey.Public().(ed25519.PublicKey)
}

func issue(
	t *testing.T,
	issuerID string,
	issuerPrivate ed25519.PrivateKey,
	subjectPublic ed25519.PublicKey,
	serial uint64,
	operations []string,
	prefixes []string,
	depth uint32,
	notBefore time.Time,
	notAfter time.Time,
) Certificate {
	t.Helper()
	certificate := Certificate{
		SubjectPublicKey:   append(ed25519.PublicKey(nil), subjectPublic...),
		IssuerID:           issuerID,
		Serial:             serial,
		AllowedOperations:  append([]string(nil), operations...),
		ResourcePrefixes:   append([]string(nil), prefixes...),
		MaxDelegationDepth: depth,
		NotBefore:          notBefore,
		NotAfter:           notAfter,
	}
	if err := SignCertificate(&certificate, issuerPrivate); err != nil {
		t.Fatalf("SignCertificate() error = %v", err)
	}
	return certificate
}

func cloneCertificate(certificate Certificate) Certificate {
	clone := certificate
	clone.SubjectPublicKey = append(ed25519.PublicKey(nil), certificate.SubjectPublicKey...)
	clone.AllowedOperations = append([]string(nil), certificate.AllowedOperations...)
	clone.ResourcePrefixes = append([]string(nil), certificate.ResourcePrefixes...)
	clone.Signature = append([]byte(nil), certificate.Signature...)
	return clone
}

func chainSerials(chain []Certificate) []uint64 {
	serials := make([]uint64, len(chain))
	for i, certificate := range chain {
		serials[i] = certificate.Serial
	}
	return serials
}
