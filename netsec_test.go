package netsec

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"testing"
	"time"
)

type testAuthority struct {
	publicKey  ed25519.PublicKey
	privateKey ed25519.PrivateKey
}

func newTestAuthority(t testing.TB) testAuthority {
	t.Helper()
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate Ed25519 key: %v", err)
	}
	return testAuthority{publicKey: publicKey, privateKey: privateKey}
}

func issue(
	t testing.TB,
	issuer testAuthority,
	issuerID string,
	subject ed25519.PublicKey,
	serial uint64,
	operations []string,
	prefixes []string,
	depth uint32,
	notBefore, notAfter time.Time,
) Certificate {
	t.Helper()
	cert := NewCertificate(subject, issuerID, serial, operations, prefixes, depth, notBefore, notAfter)
	if err := cert.Sign(issuer.privateKey); err != nil {
		t.Fatalf("sign certificate: %v", err)
	}
	return *cert
}

func anchor(id string, authority testAuthority) TrustAnchor {
	return TrustAnchor{ID: id, PublicKey: authority.publicKey}
}

func mustFailNoChain(t *testing.T, args ...any) {
	t.Helper()
	chain, err := Verify(args...)
	if err == nil {
		t.Fatalf("verification unexpectedly succeeded with chain of length %d", len(chain))
	}
	if !errors.Is(err, ErrNoValidChain) {
		t.Fatalf("got error %v, want ErrNoValidChain", err)
	}
}

func TestValidChainAndEffectivePolicy(t *testing.T) {
	start := time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)
	end := start.Add(24 * time.Hour)
	root := newTestAuthority(t)
	middle := newTestAuthority(t)
	leaf := newTestAuthority(t)

	rootID := "root-issuer"
	parent := issue(t, root, rootID, middle.publicKey, 1,
		[]string{"read", "write"}, []string{"/srv", "/public"}, 2, start, end)
	child := issue(t, middle, parent.SubjectID(), leaf.publicKey, 2,
		[]string{"read"}, []string{"/srv/app"}, 1, start, end)

	request := Request{
		Operation:        "read",
		Resource:         "/srv/app/data/file.txt",
		At:               start.Add(time.Hour),
		SubjectPublicKey: leaf.publicKey,
	}
	result, err := VerifyResult(request, []Certificate{parent, child}, []TrustAnchor{anchor(rootID, root)})
	if err != nil {
		t.Fatalf("VerifyResult: %v", err)
	}
	if len(result.Chain) != 2 {
		t.Fatalf("chain length = %d, want 2", len(result.Chain))
	}
	if result.Chain[0].Serial != child.Serial || result.Chain[1].Serial != parent.Serial {
		t.Fatalf("chain is not ordered leaf to root: %d, %d", result.Chain[0].Serial, result.Chain[1].Serial)
	}
	if result.RootID != rootID {
		t.Fatalf("root ID = %q, want %q", result.RootID, rootID)
	}
	if len(result.Policy.Operations) != 1 || result.Policy.Operations[0] != "read" {
		t.Fatalf("effective operations = %v, want [read]", result.Policy.Operations)
	}
	if len(result.Policy.ResourcePrefixes) != 1 || result.Policy.ResourcePrefixes[0] != "/srv/app" {
		t.Fatalf("effective prefixes = %v, want [/srv/app]", result.Policy.ResourcePrefixes)
	}
	if result.Policy.RemainingDelegationDepth != 1 {
		t.Fatalf("remaining depth = %d, want 1", result.Policy.RemainingDelegationDepth)
	}

	chain, err := Verify(request, []Certificate{parent, child}, anchor(rootID, root))
	if err != nil {
		t.Fatalf("Verify with single anchor: %v", err)
	}
	if len(chain) != 2 {
		t.Fatalf("Verify chain length = %d, want 2", len(chain))
	}
}

func TestForkedPermissionsCannotBeSpliced(t *testing.T) {
	start := time.Date(2030, 2, 1, 0, 0, 0, 0, time.UTC)
	end := start.Add(24 * time.Hour)
	root := newTestAuthority(t)
	sharedKey := newTestAuthority(t)
	leafKey := newTestAuthority(t)
	rootID := "fork-root"

	readParent := issue(t, root, rootID, sharedKey.publicKey, 10,
		[]string{"read"}, []string{"/data"}, 2, start, end)
	writeParent := issue(t, root, rootID, sharedKey.publicKey, 11,
		[]string{"write"}, []string{"/other"}, 2, start, end)
	leaf := issue(t, sharedKey, readParent.SubjectID(), leafKey.publicKey, 12,
		[]string{"read", "write"}, []string{"/data", "/other"}, 0, start, end)
	certs := []Certificate{readParent, writeParent, leaf}
	roots := []TrustAnchor{anchor(rootID, root)}

	// Each branch has exactly one of the two required dimensions. A verifier
	// that unions operations from one branch with prefixes from the other
	// would incorrectly authorize these requests.
	mustFailNoChain(t, Request{
		Operation: "write", Resource: "/data", At: start.Add(time.Hour),
		SubjectPublicKey: leafKey.publicKey,
	}, certs, roots)
	mustFailNoChain(t, Request{
		Operation: "read", Resource: "/other", At: start.Add(time.Hour),
		SubjectPublicKey: leafKey.publicKey,
	}, certs, roots)

	readChain, err := Verify(Request{
		Operation: "read", Resource: "/data", At: start.Add(time.Hour),
		SubjectPublicKey: leafKey.publicKey,
	}, certs, roots)
	if err != nil {
		t.Fatalf("read branch: %v", err)
	}
	if len(readChain) != 2 || readChain[1].Serial != readParent.Serial {
		t.Fatalf("read chain used the wrong parent: %+v", readChain)
	}

	writeChain, err := Verify(Request{
		Operation: "write", Resource: "/other", At: start.Add(time.Hour),
		SubjectPublicKey: leafKey.publicKey,
	}, certs, roots)
	if err != nil {
		t.Fatalf("write branch: %v", err)
	}
	if len(writeChain) != 2 || writeChain[1].Serial != writeParent.Serial {
		t.Fatalf("write chain used the wrong parent: %+v", writeChain)
	}
}

func TestPathComponentPrefixes(t *testing.T) {
	cases := []struct {
		prefix   string
		resource string
		want     bool
	}{
		{"/api", "/api", true},
		{"/api", "/api/v1/users", true},
		{"/api", "/apix", false},
		{"/api", "/api-v1", false},
		{"/srv/app", "/srv/application", false},
		{"/srv/app", "/srv/app/v1", true},
		{"/a/b", "/a/bc", false},
		{"/", "/anything/at/all", true},
		{"/", "/", true},
	}
	for _, testCase := range cases {
		got, err := PathPrefixMatches(testCase.prefix, testCase.resource)
		if err != nil {
			t.Fatalf("PathPrefixMatches(%q, %q): %v", testCase.prefix, testCase.resource, err)
		}
		if got != testCase.want {
			t.Errorf("PathPrefixMatches(%q, %q) = %v, want %v",
				testCase.prefix, testCase.resource, got, testCase.want)
		}
	}

	start := time.Date(2030, 3, 1, 0, 0, 0, 0, time.UTC)
	end := start.Add(time.Hour)
	root := newTestAuthority(t)
	middle := newTestAuthority(t)
	leaf := newTestAuthority(t)
	rootID := "path-root"
	parent := issue(t, root, rootID, middle.publicKey, 1,
		[]string{"read"}, []string{"/api"}, 2, start, end)
	child := issue(t, middle, parent.SubjectID(), leaf.publicKey, 2,
		[]string{"read"}, []string{"/api/v1"}, 0, start, end)
	certs := []Certificate{parent, child}
	roots := []TrustAnchor{anchor(rootID, root)}

	if _, err := Verify(Request{
		Operation: "read", Resource: "/api/v1/users", At: start.Add(time.Minute),
		SubjectPublicKey: leaf.publicKey,
	}, certs, roots); err != nil {
		t.Fatalf("component prefix should match: %v", err)
	}
	mustFailNoChain(t, Request{
		Operation: "read", Resource: "/api/v10", At: start.Add(time.Minute),
		SubjectPublicKey: leaf.publicKey,
	}, certs, roots)

	disjoint := issue(t, root, rootID, middle.publicKey, 3,
		[]string{"read"}, []string{"/srv/data"}, 2, start, end)
	disjointChild := issue(t, middle, disjoint.SubjectID(), leaf.publicKey, 4,
		[]string{"read"}, []string{"/srv/database"}, 0, start, end)
	mustFailNoChain(t, Request{
		Operation: "read", Resource: "/srv/database/row", At: start.Add(time.Minute),
		SubjectPublicKey: leaf.publicKey,
	}, []Certificate{disjoint, disjointChild}, roots)
}

func TestDepthExhaustion(t *testing.T) {
	start := time.Date(2030, 4, 1, 0, 0, 0, 0, time.UTC)
	end := start.Add(time.Hour)
	root := newTestAuthority(t)
	firstKey := newTestAuthority(t)
	secondKey := newTestAuthority(t)
	thirdKey := newTestAuthority(t)
	fourthKey := newTestAuthority(t)
	rootID := "depth-root"

	makeChain := func(t *testing.T, firstDepth, secondDepth, thirdDepth uint32) []Certificate {
		t.Helper()
		first := issue(t, root, rootID, firstKey.publicKey, 1,
			[]string{"read"}, []string{"/"}, firstDepth, start, end)
		second := issue(t, firstKey, first.SubjectID(), secondKey.publicKey, 2,
			[]string{"read"}, []string{"/"}, secondDepth, start, end)
		third := issue(t, secondKey, second.SubjectID(), thirdKey.publicKey, 3,
			[]string{"read"}, []string{"/"}, thirdDepth, start, end)
		return []Certificate{first, second, third}
	}

	// A root-issued certificate with depth zero may authorize its own subject
	// directly, but it may not issue another certificate.
	zero := issue(t, root, rootID, firstKey.publicKey, 20,
		[]string{"read"}, []string{"/"}, 0, start, end)
	if _, err := Verify(Request{
		Operation: "read", Resource: "/x", At: start.Add(time.Minute),
		SubjectPublicKey: firstKey.publicKey,
	}, []Certificate{zero}, []TrustAnchor{anchor(rootID, root)}); err != nil {
		t.Fatalf("depth-zero leaf should authorize directly: %v", err)
	}
	zeroChild := issue(t, firstKey, zero.SubjectID(), secondKey.publicKey, 21,
		[]string{"read"}, []string{"/"}, 0, start, end)
	mustFailNoChain(t, Request{
		Operation: "read", Resource: "/x", At: start.Add(time.Minute),
		SubjectPublicKey: secondKey.publicKey,
	}, []Certificate{zero, zeroChild}, []TrustAnchor{anchor(rootID, root)})

	// first depth 1 permits exactly one delegation. The child's inflated
	// declared depth must not create more authority.
	chain := makeChain(t, 1, 5, 5)
	first, second, third := chain[0], chain[1], chain[2]
	if _, err := Verify(Request{
		Operation: "read", Resource: "/x", At: start.Add(time.Minute),
		SubjectPublicKey: secondKey.publicKey,
	}, []Certificate{first, second}, []TrustAnchor{anchor(rootID, root)}); err != nil {
		t.Fatalf("one delegation should succeed: %v", err)
	}
	mustFailNoChain(t, Request{
		Operation: "read", Resource: "/x", At: start.Add(time.Minute),
		SubjectPublicKey: thirdKey.publicKey,
	}, []Certificate{first, second, third}, []TrustAnchor{anchor(rootID, root)})

	// With budget two, the third certificate is reachable, but it receives
	// no budget to issue a fourth.
	chain = makeChain(t, 2, 5, 5)
	first, second, third = chain[0], chain[1], chain[2]
	if _, err := Verify(Request{
		Operation: "read", Resource: "/x", At: start.Add(time.Minute),
		SubjectPublicKey: thirdKey.publicKey,
	}, []Certificate{first, second, third}, []TrustAnchor{anchor(rootID, root)}); err != nil {
		t.Fatalf("two delegations should succeed: %v", err)
	}
	fourth := issue(t, thirdKey, third.SubjectID(), fourthKey.publicKey, 4,
		[]string{"read"}, []string{"/"}, 5, start, end)
	mustFailNoChain(t, Request{
		Operation: "read", Resource: "/x", At: start.Add(time.Minute),
		SubjectPublicKey: fourthKey.publicKey,
	}, []Certificate{first, second, third, fourth}, []TrustAnchor{anchor(rootID, root)})
}

func TestValidityIntervalIsHalfOpen(t *testing.T) {
	start := time.Date(2030, 5, 1, 12, 0, 0, 0, time.UTC)
	end := start.Add(2 * time.Hour)
	root := newTestAuthority(t)
	middle := newTestAuthority(t)
	leaf := newTestAuthority(t)
	rootID := "time-root"

	parent := issue(t, root, rootID, middle.publicKey, 1,
		[]string{"read"}, []string{"/"}, 2, start, end)
	child := issue(t, middle, parent.SubjectID(), leaf.publicKey, 2,
		[]string{"read"}, []string{"/"}, 0, start, end)
	certs := []Certificate{parent, child}
	roots := []TrustAnchor{anchor(rootID, root)}

	verifyAt := func(at time.Time) error {
		_, err := Verify(Request{
			Operation: "read", Resource: "/x", At: at,
			SubjectPublicKey: leaf.publicKey,
		}, certs, roots)
		return err
	}
	if err := verifyAt(start); err != nil {
		t.Fatalf("NotBefore must be inclusive: %v", err)
	}
	if err := verifyAt(end.Add(-time.Nanosecond)); err != nil {
		t.Fatalf("one nanosecond before NotAfter must be valid: %v", err)
	}
	if err := verifyAt(end); err == nil {
		t.Fatal("NotAfter must be exclusive")
	}
	if err := verifyAt(start.Add(-time.Nanosecond)); err == nil {
		t.Fatal("one nanosecond before NotBefore must be invalid")
	}

	shortParent := issue(t, root, rootID, middle.publicKey, 3,
		[]string{"read"}, []string{"/"}, 2, start, start.Add(30*time.Minute))
	shortChain := []Certificate{shortParent, child}
	if _, err := Verify(Request{
		Operation: "read", Resource: "/x", At: start.Add(45 * time.Minute),
		SubjectPublicKey: leaf.publicKey,
	}, shortChain, roots); err == nil {
		t.Fatal("expired parent must invalidate an otherwise valid leaf")
	}

	fromZero := issue(t, root, rootID, leaf.publicKey, 4,
		[]string{"read"}, []string{"/"}, 0, time.Time{}, end)
	if _, err := Verify(Request{
		Operation: "read", Resource: "/x", At: start,
		SubjectPublicKey: leaf.publicKey,
	}, []Certificate{fromZero}, roots); err != nil {
		t.Fatalf("zero NotBefore is the year-one instant and should be valid: %v", err)
	}
}

func TestLegitimateCrossSigning(t *testing.T) {
	start := time.Date(2030, 6, 1, 0, 0, 0, 0, time.UTC)
	end := start.Add(time.Hour)
	rootA := newTestAuthority(t)
	rootB := newTestAuthority(t)
	sharedKey := newTestAuthority(t)
	leafKey := newTestAuthority(t)
	rootAID := "cross-root-a"
	rootBID := "cross-root-b"

	certA := issue(t, rootA, rootAID, sharedKey.publicKey, 1,
		[]string{"read"}, []string{"/shared", "/a"}, 2, start, end)
	certB := issue(t, rootB, rootBID, sharedKey.publicKey, 2,
		[]string{"write"}, []string{"/shared", "/b"}, 2, start, end)
	leaf := issue(t, sharedKey, certA.SubjectID(), leafKey.publicKey, 3,
		[]string{"read", "write"}, []string{"/shared", "/a", "/b"}, 0, start, end)
	certs := []Certificate{certA, certB, leaf}
	roots := []TrustAnchor{anchor(rootAID, rootA), anchor(rootBID, rootB)}

	chainA, err := Verify(Request{
		Operation: "read", Resource: "/shared/doc", At: start.Add(time.Minute),
		SubjectPublicKey: leafKey.publicKey,
	}, certs, roots)
	if err != nil {
		t.Fatalf("cross-signed read chain: %v", err)
	}
	if len(chainA) != 2 || chainA[1].Serial != certA.Serial {
		t.Fatalf("read chain did not use root A certificate: %+v", chainA)
	}

	chainB, err := Verify(Request{
		Operation: "write", Resource: "/b/doc", At: start.Add(time.Minute),
		SubjectPublicKey: leafKey.publicKey,
	}, certs, roots)
	if err != nil {
		t.Fatalf("cross-signed write chain: %v", err)
	}
	if len(chainB) != 2 || chainB[1].Serial != certB.Serial {
		t.Fatalf("write chain did not use root B certificate: %+v", chainB)
	}

	// The two cross-certificates are still separate, single chains. Their
	// permissions may not be combined across roots.
	mustFailNoChain(t, Request{
		Operation: "write", Resource: "/a/doc", At: start.Add(time.Minute),
		SubjectPublicKey: leafKey.publicKey,
	}, certs, roots)
}

func TestSamePublicKeyDifferentCertificatesRemainSeparate(t *testing.T) {
	start := time.Date(2030, 7, 1, 0, 0, 0, 0, time.UTC)
	end := start.Add(time.Hour)
	root := newTestAuthority(t)
	sharedKey := newTestAuthority(t)
	leafKey := newTestAuthority(t)
	rootID := "same-key-root"

	narrow := issue(t, root, rootID, sharedKey.publicKey, 1,
		[]string{"read"}, []string{"/data"}, 2, start, end)
	broad := issue(t, root, rootID, sharedKey.publicKey, 2,
		[]string{"read", "write"}, []string{"/data"}, 2, start, end)
	leaf := issue(t, sharedKey, narrow.SubjectID(), leafKey.publicKey, 3,
		[]string{"read", "write"}, []string{"/data"}, 0, start, end)
	request := Request{
		Operation: "write", Resource: "/data/file", At: start.Add(time.Minute),
		SubjectPublicKey: leafKey.publicKey,
	}
	roots := []TrustAnchor{anchor(rootID, root)}

	mustFailNoChain(t, request, []Certificate{narrow, leaf}, roots)
	chain, err := Verify(request, []Certificate{narrow, broad, leaf}, roots)
	if err != nil {
		t.Fatalf("broad same-key certificate should form its own valid chain: %v", err)
	}
	if len(chain) != 2 || chain[1].Serial != broad.Serial {
		t.Fatalf("verifier did not select the single broad certificate: %+v", chain)
	}
}

func TestRevocationUsesIssuerAndSerialPair(t *testing.T) {
	start := time.Date(2030, 8, 1, 0, 0, 0, 0, time.UTC)
	end := start.Add(time.Hour)
	rootA := newTestAuthority(t)
	rootB := newTestAuthority(t)
	sharedKey := newTestAuthority(t)
	leafKey := newTestAuthority(t)
	rootAID := "rev-root-a"
	rootBID := "rev-root-b"

	certA := issue(t, rootA, rootAID, sharedKey.publicKey, 7,
		[]string{"read"}, []string{"/data"}, 2, start, end)
	certB := issue(t, rootB, rootBID, sharedKey.publicKey, 7,
		[]string{"read"}, []string{"/data"}, 2, start, end)
	leaf := issue(t, sharedKey, certA.SubjectID(), leafKey.publicKey, 9,
		[]string{"read"}, []string{"/data"}, 0, start, end)
	certs := []Certificate{certA, certB, leaf}
	roots := []TrustAnchor{anchor(rootAID, rootA), anchor(rootBID, rootB)}
	request := Request{
		Operation: "read", Resource: "/data/file", At: start.Add(time.Minute),
		SubjectPublicKey: leafKey.publicKey,
	}

	chain, err := Verify(request, certs, roots, []Revocation{{IssuerID: rootAID, Serial: 7}})
	if err != nil {
		t.Fatalf("same serial under another issuer must not be revoked: %v", err)
	}
	if len(chain) != 2 || chain[1].IssuerID != rootBID {
		t.Fatalf("chain did not use the unrevoked issuer pair: %+v", chain)
	}

	mustFailNoChain(t, request, certs, roots, []Revocation{
		{IssuerID: rootAID, Serial: 7},
		{IssuerID: rootBID, Serial: 7},
	})

	mustFailNoChain(t, request, certs, roots,
		[]Revocation{{IssuerID: certB.SubjectID(), Serial: leaf.Serial}})

	// The same serial under a different issuer is unrelated to the leaf.
	if _, err := Verify(request, certs, roots,
		[]Revocation{{IssuerID: "some-other-issuer", Serial: leaf.Serial}}); err != nil {
		t.Fatalf("unrelated issuer/serial revocation was applied: %v", err)
	}
}

func TestRepeatedSubjectKeyUsesAcyclicShortcut(t *testing.T) {
	start := time.Date(2030, 9, 1, 0, 0, 0, 0, time.UTC)
	end := start.Add(time.Hour)
	root := newTestAuthority(t)
	key1 := newTestAuthority(t)
	key2 := newTestAuthority(t)
	leafKey := newTestAuthority(t)
	rootID := "cycle-root"

	first := issue(t, root, rootID, key1.publicKey, 1,
		[]string{"read"}, []string{"/"}, 8, start, end)
	second := issue(t, key1, first.SubjectID(), key2.publicKey, 2,
		[]string{"read"}, []string{"/"}, 8, start, end)
	// This certificate has the same subject key as first, under a different
	// (issuer, serial) identity. A naive key-only search could form the cycle
	// first -> second -> repeat -> fourth. Because the earlier certificate
	// holds the same key, the verifier must return the equivalent acyclic
	// shortcut rather than repeat a subject key.
	repeat := issue(t, key2, second.SubjectID(), key1.publicKey, 3,
		[]string{"read"}, []string{"/"}, 8, start, end)
	fourth := issue(t, key1, repeat.SubjectID(), leafKey.publicKey, 4,
		[]string{"read"}, []string{"/"}, 0, start, end)

	chain, err := Verify(Request{
		Operation: "read", Resource: "/x", At: start.Add(time.Minute),
		SubjectPublicKey: leafKey.publicKey,
	}, []Certificate{first, second, repeat, fourth}, []TrustAnchor{anchor(rootID, root)})
	if err != nil {
		t.Fatalf("acyclic shortcut: %v", err)
	}
	if len(chain) != 2 || chain[0].Serial != fourth.Serial || chain[1].Serial != first.Serial {
		t.Fatalf("expected direct acyclic shortcut, got %+v", chain)
	}
	if chain[0].SubjectID() == chain[1].SubjectID() {
		t.Fatal("returned chain repeats a subject key")
	}
}

func TestSignatureCoversEveryField(t *testing.T) {
	start := time.Date(2030, 10, 1, 0, 0, 0, 0, time.UTC)
	end := start.Add(time.Hour)
	root := newTestAuthority(t)
	subject := newTestAuthority(t)
	cert := issue(t, root, "encoding-root", subject.publicKey, 42,
		[]string{"read", "write"}, []string{"/data"}, 3, start, end)

	if err := cert.VerifySignature(root.publicKey); err != nil {
		t.Fatalf("original signature: %v", err)
	}
	marshaled, err := cert.MarshalBinary()
	if err != nil {
		t.Fatalf("MarshalBinary: %v", err)
	}
	parsed, err := ParseCertificate(marshaled)
	if err != nil {
		t.Fatalf("ParseCertificate: %v", err)
	}
	if !cert.Equal(*parsed) {
		t.Fatal("certificate changed across marshal/parse")
	}
	if err := parsed.VerifySignature(root.publicKey); err != nil {
		t.Fatalf("parsed certificate signature: %v", err)
	}

	mutations := []struct {
		name   string
		mutate func(*Certificate)
	}{
		{"subject", func(c *Certificate) { c.SubjectPublicKey = append(ed25519.PublicKey(nil), 1, 2, 3) }},
		{"issuer", func(c *Certificate) { c.IssuerID = "other-root" }},
		{"serial", func(c *Certificate) { c.Serial++ }},
		{"operations", func(c *Certificate) { c.Operations = []string{"read"} }},
		{"prefixes", func(c *Certificate) { c.ResourcePrefixes = []string{"/"} }},
		{"depth", func(c *Certificate) { c.MaxDelegationDepth++ }},
		{"notBefore", func(c *Certificate) { c.NotBefore = c.NotBefore.Add(-time.Second) }},
		{"notAfter", func(c *Certificate) { c.NotAfter = c.NotAfter.Add(time.Second) }},
	}
	for _, mutation := range mutations {
		t.Run(mutation.name, func(t *testing.T) {
			tampered := cert
			tampered.Operations = append([]string(nil), cert.Operations...)
			tampered.ResourcePrefixes = append([]string(nil), cert.ResourcePrefixes...)
			mutation.mutate(&tampered)
			if err := tampered.VerifySignature(root.publicKey); err == nil {
				t.Fatalf("tampering with %s did not invalidate signature", mutation.name)
			}
		})
	}

	tamperedSignature := cert
	tamperedSignature.Signature = append([]byte(nil), cert.Signature...)
	tamperedSignature.Signature[0] ^= 0xff
	if err := tamperedSignature.VerifySignature(root.publicKey); err == nil {
		t.Fatal("tampered signature verified")
	}

	// Redundant prefixes are still part of the signed field list even though
	// the verifier reduces them for policy evaluation.
	withRedundant := cert
	withRedundant.ResourcePrefixes = []string{"/data", "/data"}
	if err := withRedundant.Sign(root.privateKey); err != nil {
		t.Fatalf("sign duplicate prefix: %v", err)
	}
	changedRedundant := withRedundant
	changedRedundant.ResourcePrefixes = []string{"/data", "/elsewhere"}
	if err := changedRedundant.VerifySignature(root.publicKey); err == nil {
		t.Fatal("changing a redundant prefix entry did not invalidate signature")
	}
}

func TestParseRejectsMalformedEncodings(t *testing.T) {
	start := time.Date(2030, 11, 1, 0, 0, 0, 0, time.UTC)
	end := start.Add(time.Hour)
	root := newTestAuthority(t)
	subject := newTestAuthority(t)
	cert := issue(t, root, "parse-root", subject.publicKey, 1,
		[]string{"read"}, []string{"/data"}, 1, start, end)
	valid, err := cert.MarshalBinary()
	if err != nil {
		t.Fatalf("MarshalBinary: %v", err)
	}

	for _, cut := range []int{0, 1, 5, len(certificateMagic), len(valid) - 1} {
		if _, err := ParseCertificate(valid[:cut]); err == nil {
			t.Fatalf("parse succeeded for truncated certificate at %d", cut)
		}
	}

	signed, err := cert.SigningBytes()
	if err != nil {
		t.Fatalf("SigningBytes: %v", err)
	}
	var duplicateSignature bytes.Buffer
	duplicateSignature.Write(signed)
	writeField(&duplicateSignature, fieldSignature, cert.Signature)
	writeField(&duplicateSignature, fieldSignature, cert.Signature)
	if _, err := ParseCertificate(duplicateSignature.Bytes()); err == nil {
		t.Fatal("duplicate field was accepted")
	}

	var unknownField bytes.Buffer
	unknownField.Write(signed)
	writeField(&unknownField, fieldSignature, cert.Signature)
	writeField(&unknownField, 999, []byte("x"))
	if _, err := ParseCertificate(unknownField.Bytes()); err == nil {
		t.Fatal("unknown field was accepted")
	}

	var badLength bytes.Buffer
	badLength.Write(valid)
	badLength.Write([]byte{0, 99})
	if _, err := ParseCertificate(badLength.Bytes()); err == nil {
		t.Fatal("malformed field header was accepted")
	}
}

func TestCertificateLimit(t *testing.T) {
	start := time.Date(2031, 1, 1, 0, 0, 0, 0, time.UTC)
	end := start.Add(time.Hour)
	root := newTestAuthority(t)
	subject := newTestAuthority(t)
	cert := issue(t, root, "limit-root", subject.publicKey, 1,
		[]string{"read"}, []string{"/"}, 32, start, end)
	certs := make([]Certificate, 33)
	for i := range certs {
		certs[i] = cert
	}
	_, err := Verify(Request{
		Operation: "read", Resource: "/x", At: start.Add(time.Minute),
		SubjectPublicKey: subject.publicKey,
	}, certs, []TrustAnchor{anchor("limit-root", root)})
	if !errors.Is(err, ErrTooManyCertificates) {
		t.Fatalf("got %v, want ErrTooManyCertificates", err)
	}
}

func TestUntrustedOrForgedIssuerIsRejected(t *testing.T) {
	start := time.Date(2031, 2, 1, 0, 0, 0, 0, time.UTC)
	end := start.Add(time.Hour)
	root := newTestAuthority(t)
	attacker := newTestAuthority(t)
	subject := newTestAuthority(t)
	request := Request{
		Operation: "read", Resource: "/x", At: start.Add(time.Minute),
		SubjectPublicKey: subject.publicKey,
	}

	// The certificate names the trusted root but is signed by another key.
	forged := issue(t, attacker, "trusted-root", subject.publicKey, 1,
		[]string{"read"}, []string{"/"}, 0, start, end)
	mustFailNoChain(t, request, []Certificate{forged},
		[]TrustAnchor{anchor("trusted-root", root)})

	// A validly signed certificate with an unknown issuer is not rooted.
	unknown := issue(t, root, "unknown-root", subject.publicKey, 2,
		[]string{"read"}, []string{"/"}, 0, start, end)
	mustFailNoChain(t, request, []Certificate{unknown},
		[]TrustAnchor{anchor("trusted-root", root)})

	// A root anchor with an empty ID still requires a signature by that root.
	if _, err := Verify(request, []Certificate{unknown},
		[]TrustAnchor{{PublicKey: root.publicKey}}); err != nil {
		t.Fatalf("empty anchor ID should accept its own signature: %v", err)
	}
}

func TestProvidedLeafAndCanonicalKeyID(t *testing.T) {
	start := time.Date(2031, 3, 1, 0, 0, 0, 0, time.UTC)
	end := start.Add(time.Hour)
	root := newTestAuthority(t)
	middle := newTestAuthority(t)
	leaf := newTestAuthority(t)
	rootID := "leaf-root"

	parent := issue(t, root, rootID, middle.publicKey, 1,
		[]string{"read"}, []string{"/"}, 2, start, end)
	child := issue(t, middle, KeyID(parent.SubjectPublicKey), leaf.publicKey, 2,
		[]string{"read"}, []string{"/"}, 0, start, end)
	request := Request{
		Operation: "read", Resource: "/x", At: start.Add(time.Minute),
		Leaf: &child,
	}
	chain, err := Verify(request, []Certificate{parent, child},
		[]TrustAnchor{anchor(rootID, root)})
	if err != nil {
		t.Fatalf("provided leaf verification: %v", err)
	}
	if len(chain) != 2 || !chain[0].Equal(child) {
		t.Fatalf("provided leaf is not chain[0]: %+v", chain)
	}
	if chain[0] != &child {
		t.Fatal("provided leaf pointer was not preserved in the returned chain")
	}

	missing := child
	missing.Serial = 999
	mustFailNoChain(t, Request{
		Operation: "read", Resource: "/x", At: start.Add(time.Minute),
		Leaf: &missing,
	}, []Certificate{parent, child}, []TrustAnchor{anchor(rootID, root)})
}

func TestPointerPoolPreservesIdentity(t *testing.T) {
	start := time.Date(2031, 3, 15, 0, 0, 0, 0, time.UTC)
	end := start.Add(time.Hour)
	root := newTestAuthority(t)
	middle := newTestAuthority(t)
	leaf := newTestAuthority(t)
	rootID := "pointer-root"

	parent := issue(t, root, rootID, middle.publicKey, 1,
		[]string{"read"}, []string{"/"}, 2, start, end)
	child := issue(t, middle, parent.SubjectID(), leaf.publicKey, 2,
		[]string{"read"}, []string{"/"}, 0, start, end)
	pool := []*Certificate{&parent, &child}
	chain, err := Verify(Request{
		Operation: "read", Resource: "/x", At: start.Add(time.Minute),
		Leaf: &child,
	}, pool, []TrustAnchor{anchor(rootID, root)})
	if err != nil {
		t.Fatalf("pointer pool verification: %v", err)
	}
	if chain[0] != &child || chain[1] != &parent {
		t.Fatal("pointer pool chain does not preserve certificate identity")
	}
}

func TestAcceptedConfigurationAliases(t *testing.T) {
	start := time.Date(2031, 4, 15, 0, 0, 0, 0, time.UTC)
	end := start.Add(time.Hour)
	root := newTestAuthority(t)
	subject := newTestAuthority(t)
	rootID := "alias-root"
	cert := issue(t, root, rootID, subject.publicKey, 1,
		[]string{"read"}, []string{"/alias"}, 0, start, end)

	request := Request{
		Action:  "read",
		Path:    "/alias/file",
		Time:    start.Add(time.Minute),
		Subject: subject.publicKey,
	}
	rootAlias := TrustAnchor{Name: rootID, Key: root.publicKey}
	chain, err := Verify(request, []Certificate{cert}, []TrustAnchor{rootAlias})
	if err != nil {
		t.Fatalf("alias configuration: %v", err)
	}
	if len(chain) != 1 {
		t.Fatalf("alias chain length = %d, want 1", len(chain))
	}
	if _, err := Verify(request, []Certificate{cert}, root.publicKey); err != nil {
		t.Fatalf("bare root key argument: %v", err)
	}
	if _, err := Verify(Request{
		Operation: "read", Resource: "/alias/file", At: start.Add(time.Minute),
	}, []Certificate{cert}, subject.publicKey, []TrustAnchor{rootAlias}); err != nil {
		t.Fatalf("bare subject key argument: %v", err)
	}

	revoked := []Revocation{{Issuer: rootID, Serial: cert.Serial}}
	mustFailNoChain(t, request, []Certificate{cert}, []TrustAnchor{rootAlias}, revoked)
}

func TestNoChainWhenOperationOrResourceIsEmptySet(t *testing.T) {
	start := time.Date(2031, 4, 1, 0, 0, 0, 0, time.UTC)
	end := start.Add(time.Hour)
	root := newTestAuthority(t)
	subject := newTestAuthority(t)
	rootID := "empty-root"

	noOperations := issue(t, root, rootID, subject.publicKey, 1,
		nil, []string{"/"}, 0, start, end)
	mustFailNoChain(t, Request{
		Operation: "read", Resource: "/x", At: start.Add(time.Minute),
		SubjectPublicKey: subject.publicKey,
	}, []Certificate{noOperations}, []TrustAnchor{anchor(rootID, root)})

	noResources := issue(t, root, rootID, subject.publicKey, 2,
		[]string{"read"}, nil, 0, start, end)
	mustFailNoChain(t, Request{
		Operation: "read", Resource: "/x", At: start.Add(time.Minute),
		SubjectPublicKey: subject.publicKey,
	}, []Certificate{noResources}, []TrustAnchor{anchor(rootID, root)})
}

func TestPublicPolicyHelpers(t *testing.T) {
	if got := IntersectOperations([]string{"read", "write", "read"}, []string{"read", "list"}); len(got) != 1 || got[0] != "read" {
		t.Fatalf("IntersectOperations = %v, want [read]", got)
	}

	prefixes, err := IntersectResourcePrefixes(
		[]string{"/srv", "/shared"},
		[]string{"/srv/app", "/shared/docs"},
	)
	if err != nil {
		t.Fatalf("IntersectResourcePrefixes: %v", err)
	}
	if len(prefixes) != 2 || prefixes[0] != "/shared/docs" || prefixes[1] != "/srv/app" {
		t.Fatalf("prefix intersection = %v", prefixes)
	}

	allowed, err := ResourceAllowed([]string{"/srv/app"}, "/srv/app/v1")
	if err != nil || !allowed {
		t.Fatalf("ResourceAllowed = %v, %v", allowed, err)
	}
	allowed, err = ResourceAllowed([]string{"/srv/app"}, "/srv/application")
	if err != nil || allowed {
		t.Fatalf("similar prefix ResourceAllowed = %v, %v", allowed, err)
	}

	policy := Policy{
		Operations:               []string{"read"},
		ResourcePrefixes:         []string{"/srv/app"},
		RemainingDelegationDepth: 1,
	}
	if !policy.Allows("read", "/srv/app/file") {
		t.Fatal("policy should allow matching request")
	}
	if policy.Allows("write", "/srv/app/file") || policy.Allows("read", "/srv/application") {
		t.Fatal("policy allowed a non-matching request")
	}
}

func TestNoSubjectReturnsCompleteLeafChain(t *testing.T) {
	start := time.Date(2031, 5, 1, 0, 0, 0, 0, time.UTC)
	end := start.Add(time.Hour)
	root := newTestAuthority(t)
	middle := newTestAuthority(t)
	leaf := newTestAuthority(t)
	rootID := "complete-root"

	parent := issue(t, root, rootID, middle.publicKey, 1,
		[]string{"read"}, []string{"/"}, 2, start, end)
	child := issue(t, middle, parent.SubjectID(), leaf.publicKey, 2,
		[]string{"read"}, []string{"/"}, 0, start, end)
	chain, err := Verify(Request{
		Operation: "read", Resource: "/x", At: start.Add(time.Minute),
	}, []Certificate{parent, child}, []TrustAnchor{anchor(rootID, root)})
	if err != nil {
		t.Fatalf("verify without subject: %v", err)
	}
	if len(chain) != 2 || chain[0].Serial != child.Serial || chain[1].Serial != parent.Serial {
		t.Fatalf("expected complete leaf chain, got %+v", chain)
	}
}

func TestMaximumChainLength(t *testing.T) {
	start := time.Date(2031, 6, 1, 0, 0, 0, 0, time.UTC)
	end := start.Add(time.Hour)
	const certificateCount = 32

	authorities := make([]testAuthority, certificateCount)
	for i := range authorities {
		authorities[i] = newTestAuthority(t)
	}
	root := newTestAuthority(t)
	rootID := "long-root"
	certs := make([]Certificate, certificateCount)
	for i := 0; i < certificateCount; i++ {
		issuer := root
		issuerID := rootID
		if i > 0 {
			issuer = authorities[i-1]
			issuerID = KeyID(authorities[i-1].publicKey)
		}
		certs[i] = issue(t, issuer, issuerID, authorities[i].publicKey, uint64(i+1),
			[]string{"read"}, []string{"/"}, 64, start, end)
	}

	chain, err := Verify(Request{
		Operation: "read", Resource: "/x", At: start.Add(time.Minute),
		SubjectPublicKey: authorities[certificateCount-1].publicKey,
	}, certs, []TrustAnchor{anchor(rootID, root)})
	if err != nil {
		t.Fatalf("32-certificate chain: %v", err)
	}
	if len(chain) != certificateCount {
		t.Fatalf("chain length = %d, want %d", len(chain), certificateCount)
	}
}

func FuzzParseCertificate(f *testing.F) {
	start := time.Date(2031, 7, 1, 0, 0, 0, 0, time.UTC)
	root := newTestAuthority(f)
	subject := newTestAuthority(f)
	cert := issue(f, root, "fuzz-root", subject.publicKey, 1,
		[]string{"read"}, []string{"/data"}, 1, start, start.Add(time.Hour))
	seed, err := cert.MarshalBinary()
	if err != nil {
		f.Fatalf("marshal seed: %v", err)
	}
	f.Add(seed)

	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) > maxCertificateSize+1 {
			return
		}
		parsed, err := ParseCertificate(data)
		if err != nil {
			return
		}
		if _, err := parsed.MarshalBinary(); err != nil {
			t.Fatalf("parsed certificate cannot be marshaled: %v", err)
		}
	})
}
