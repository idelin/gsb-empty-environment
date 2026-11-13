package netsec

import (
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"testing"
	"time"
)

type testParty struct {
	name string
	priv ed25519.PrivateKey
	pub  ed25519.PublicKey
	did  string
}

func newTestParty(t *testing.T, name string) testParty {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate key for %s: %v", name, err)
	}
	return testParty{name: name, priv: priv, pub: pub, did: DIDFromPublicKey(pub)}
}

func mustSign(t *testing.T, issuer testParty, audience ed25519.PublicKey,
	exp time.Time, caps []Capability, proof string) *Token {
	t.Helper()
	token, err := Sign(issuer.priv, audience, exp, caps, proof)
	if err != nil {
		t.Fatalf("sign token by %s: %v", issuer.name, err)
	}
	return token
}

func buildValidChain(t *testing.T, root, alice, bob testParty, now time.Time) Chain {
	t.Helper()
	rootToken := mustSign(t, root, alice.pub, now.Add(2*time.Hour),
		[]Capability{{Resource: "fs:/home/alice/*", Action: "crud/*"}}, "")

	aliceToken := mustSign(t, alice, bob.pub, now.Add(time.Hour),
		[]Capability{{Resource: "fs:/home/alice/docs/report", Action: "crud/read"}},
		rootToken.Encode())

	return Chain{Tokens: []*Token{rootToken, aliceToken}}
}

func TestValidNarrowingChainAccepted(t *testing.T) {
	now := time.Date(2026, 11, 13, 10, 0, 0, 0, time.UTC)
	root := newTestParty(t, "root")
	alice := newTestParty(t, "alice")
	bob := newTestParty(t, "bob")

	chain := buildValidChain(t, root, alice, bob, now)
	opts := VerifyOptions{
		TrustedIssuers: map[string]bool{root.did: true},
		Now:            now,
	}

	want := Capability{Resource: "fs:/home/alice/docs/report", Action: "crud/read"}
	if err := VerifyAccess(chain, want, opts); err != nil {
		t.Fatalf("expected narrowing chain to be accepted, got %v", err)
	}
}

func TestSecondHopCapabilityEscalationRejected(t *testing.T) {
	now := time.Date(2026, 11, 13, 10, 0, 0, 0, time.UTC)
	root := newTestParty(t, "root")
	alice := newTestParty(t, "alice")
	bob := newTestParty(t, "bob")

	rootToken := mustSign(t, root, alice.pub, now.Add(2*time.Hour),
		[]Capability{{Resource: "fs:/home/alice/docs/report", Action: "crud/read"}}, "")

	// 第二跳：Alice 只拿到单个文件的只读权限，却试图把整个家目录的
	// 全部 CRUD 权限委托给 Bob —— 能力放大，必须拒绝。
	escalated := mustSign(t, alice, bob.pub, now.Add(time.Hour),
		[]Capability{{Resource: "fs:/home/alice/*", Action: "crud/*"}},
		rootToken.Encode())

	chain := Chain{Tokens: []*Token{rootToken, escalated}}
	opts := VerifyOptions{TrustedIssuers: map[string]bool{root.did: true}, Now: now}

	err := ValidateChain(chain, opts)
	if !errors.Is(err, ErrCapabilityEscalated) {
		t.Fatalf("expected ErrCapabilityEscalated, got %v", err)
	}
}

func TestSecondHopExpiryExtensionRejected(t *testing.T) {
	now := time.Date(2026, 11, 13, 10, 0, 0, 0, time.UTC)
	root := newTestParty(t, "root")
	alice := newTestParty(t, "alice")
	bob := newTestParty(t, "bob")

	rootExp := now.Add(time.Hour)
	rootToken := mustSign(t, root, alice.pub, rootExp,
		[]Capability{{Resource: "fs:/home/alice/*", Action: "crud/*"}}, "")

	// 第二跳：能力确实收窄了，但过期时间被改到比根凭证更晚 —— 拒绝。
	later := mustSign(t, alice, bob.pub, rootExp.Add(time.Hour),
		[]Capability{{Resource: "fs:/home/alice/docs/report", Action: "crud/read"}},
		rootToken.Encode())

	chain := Chain{Tokens: []*Token{rootToken, later}}
	opts := VerifyOptions{TrustedIssuers: map[string]bool{root.did: true}, Now: now}

	err := ValidateChain(chain, opts)
	if !errors.Is(err, ErrExpiryExtended) {
		t.Fatalf("expected ErrExpiryExtended, got %v", err)
	}
}

func TestSecondHopEscalationAndLaterExpiryTogetherRejected(t *testing.T) {
	now := time.Date(2026, 11, 13, 10, 0, 0, 0, time.UTC)
	root := newTestParty(t, "root")
	alice := newTestParty(t, "alice")
	bob := newTestParty(t, "bob")

	rootToken := mustSign(t, root, alice.pub, now.Add(time.Hour),
		[]Capability{{Resource: "fs:/home/alice/docs/report", Action: "crud/read"}}, "")

	// 题目场景：第二跳同时把能力改大、把过期时间改晚。
	bad := mustSign(t, alice, bob.pub, now.Add(3*time.Hour),
		[]Capability{{Resource: "fs:/home/alice/*", Action: "crud/*"}},
		rootToken.Encode())

	chain := Chain{Tokens: []*Token{rootToken, bad}}
	opts := VerifyOptions{TrustedIssuers: map[string]bool{root.did: true}, Now: now}

	err := ValidateChain(chain, opts)
	if !errors.Is(err, ErrCapabilityEscalated) && !errors.Is(err, ErrExpiryExtended) {
		t.Fatalf("expected escalation or expiry error, got %v", err)
	}
}

func TestAudienceMismatchRejected(t *testing.T) {
	now := time.Date(2026, 11, 13, 10, 0, 0, 0, time.UTC)
	root := newTestParty(t, "root")
	alice := newTestParty(t, "alice")
	mallory := newTestParty(t, "mallory")
	bob := newTestParty(t, "bob")

	rootToken := mustSign(t, root, alice.pub, now.Add(2*time.Hour),
		[]Capability{{Resource: "fs:/home/alice/*", Action: "crud/*"}}, "")

	// Mallory 并非根凭证的受众，却冒充链上主体签发下一跳。
	forged := mustSign(t, mallory, bob.pub, now.Add(time.Hour),
		[]Capability{{Resource: "fs:/home/alice/docs", Action: "crud/read"}},
		rootToken.Encode())

	chain := Chain{Tokens: []*Token{rootToken, forged}}
	opts := VerifyOptions{TrustedIssuers: map[string]bool{root.did: true}, Now: now}

	if err := ValidateChain(chain, opts); !errors.Is(err, ErrAudienceMismatch) {
		t.Fatalf("expected ErrAudienceMismatch, got %v", err)
	}
}

func TestExpiredTokenRejected(t *testing.T) {
	now := time.Date(2026, 11, 13, 10, 0, 0, 0, time.UTC)
	root := newTestParty(t, "root")
	alice := newTestParty(t, "alice")

	rootToken := mustSign(t, root, alice.pub, now.Add(-time.Minute),
		[]Capability{{Resource: "fs:/x", Action: "crud/read"}}, "")

	chain := Chain{Tokens: []*Token{rootToken}}
	opts := VerifyOptions{TrustedIssuers: map[string]bool{root.did: true}, Now: now}

	if err := ValidateChain(chain, opts); !errors.Is(err, ErrExpired) {
		t.Fatalf("expected ErrExpired, got %v", err)
	}
}

func TestUntrustedRootRejected(t *testing.T) {
	now := time.Date(2026, 11, 13, 10, 0, 0, 0, time.UTC)
	root := newTestParty(t, "root")
	alice := newTestParty(t, "alice")

	rootToken := mustSign(t, root, alice.pub, now.Add(time.Hour),
		[]Capability{{Resource: "fs:/x", Action: "crud/read"}}, "")

	chain := Chain{Tokens: []*Token{rootToken}}
	opts := VerifyOptions{TrustedIssuers: map[string]bool{}, Now: now}

	if err := ValidateChain(chain, opts); !errors.Is(err, ErrUntrustedRoot) {
		t.Fatalf("expected ErrUntrustedRoot, got %v", err)
	}
}

func TestRequestedCapabilityNotDelegated(t *testing.T) {
	now := time.Date(2026, 11, 13, 10, 0, 0, 0, time.UTC)
	root := newTestParty(t, "root")
	alice := newTestParty(t, "alice")
	bob := newTestParty(t, "bob")

	chain := buildValidChain(t, root, alice, bob, now)
	opts := VerifyOptions{TrustedIssuers: map[string]bool{root.did: true}, Now: now}

	// 末端只有 report 的读权限，请求写权限应当被拒。
	want := Capability{Resource: "fs:/home/alice/docs/report", Action: "crud/write"}
	if err := VerifyAccess(chain, want, opts); !errors.Is(err, ErrCapabilityNotDelegated) {
		t.Fatalf("expected ErrCapabilityNotDelegated, got %v", err)
	}
}

func TestTamperedSignatureRejected(t *testing.T) {
	now := time.Date(2026, 11, 13, 10, 0, 0, 0, time.UTC)
	root := newTestParty(t, "root")
	alice := newTestParty(t, "alice")

	rootToken := mustSign(t, root, alice.pub, now.Add(time.Hour),
		[]Capability{{Resource: "fs:/x", Action: "crud/read"}}, "")

	encoded := rootToken.Encode()
	lastDot := len(encoded)
	for encoded[lastDot-1] != '.' {
		lastDot--
	}
	tampered := encoded[:lastDot]
	if encoded[lastDot] == 'A' {
		tampered += "B" + encoded[lastDot+1:]
	} else {
		tampered += "A" + encoded[lastDot+1:]
	}

	if _, err := Parse(tampered); !errors.Is(err, ErrInvalidSignature) {
		t.Fatalf("expected ErrInvalidSignature, got %v", err)
	}
}

func TestThreeHopChainStrictlyNarrows(t *testing.T) {
	now := time.Date(2026, 11, 13, 10, 0, 0, 0, time.UTC)
	root := newTestParty(t, "root")
	alice := newTestParty(t, "alice")
	bob := newTestParty(t, "bob")
	carol := newTestParty(t, "carol")

	rootToken := mustSign(t, root, alice.pub, now.Add(3*time.Hour),
		[]Capability{{Resource: "fs:/home/*", Action: "crud/*"}}, "")
	aliceToken := mustSign(t, alice, bob.pub, now.Add(2*time.Hour),
		[]Capability{{Resource: "fs:/home/alice/*", Action: "crud/read"}},
		rootToken.Encode())
	bobToken := mustSign(t, bob, carol.pub, now.Add(time.Hour),
		[]Capability{{Resource: "fs:/home/alice/docs/a", Action: "crud/read"}},
		aliceToken.Encode())

	chain := Chain{Tokens: []*Token{rootToken, aliceToken, bobToken}}
	opts := VerifyOptions{TrustedIssuers: map[string]bool{root.did: true}, Now: now}

	want := Capability{Resource: "fs:/home/alice/docs/a", Action: "crud/read"}
	if err := VerifyAccess(chain, want, opts); err != nil {
		t.Fatalf("expected three-hop narrowing chain accepted, got %v", err)
	}
}

func TestCapabilitiesSubsetAndCovers(t *testing.T) {
	parent := []Capability{{Resource: "fs:/home/*", Action: "crud/*"}}

	narrower := []Capability{
		{Resource: "fs:/home/alice/doc", Action: "crud/read"},
		{Resource: "fs:/home/alice/pic", Action: "crud/*"},
	}
	if !CapabilitiesSubset(narrower, parent) {
		t.Fatal("narrower capabilities should be a subset")
	}

	wider := []Capability{{Resource: "fs:/etc/passwd", Action: "crud/read"}}
	if CapabilitiesSubset(wider, parent) {
		t.Fatal("out-of-scope resource must not be a subset")
	}

	if !Covers(Capability{Resource: "*", Action: "*"}, Capability{Resource: "x", Action: "y"}) {
		t.Fatal("wildcard capability should cover any concrete capability")
	}
	if Covers(Capability{Resource: "fs:/a", Action: "crud/read"},
		Capability{Resource: "fs:/a", Action: "crud/write"}) {
		t.Fatal("exact action match must not cover a different action")
	}
}

func TestDIDRoundTrip(t *testing.T) {
	party := newTestParty(t, "party")
	did := DIDFromPublicKey(party.pub)
	recovered, err := PublicKeyFromDID(did)
	if err != nil {
		t.Fatalf("decode DID: %v", err)
	}
	if !recovered.Equal(party.pub) {
		t.Fatal("DID round trip produced a different key")
	}
	if _, err := PublicKeyFromDID("did:web:example.com"); !errors.Is(err, ErrMalformedToken) {
		t.Fatalf("expected ErrMalformedToken for foreign DID, got %v", err)
	}
}

func TestEmptyChainRejected(t *testing.T) {
	if err := ValidateChain(Chain{}, VerifyOptions{}); !errors.Is(err, ErrEmptyChain) {
		t.Fatalf("expected ErrEmptyChain, got %v", err)
	}
}
