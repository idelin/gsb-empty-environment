package netsec

import (
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"reflect"
	"testing"
	"time"
)

var (
	testNow     = time.Date(2026, 11, 13, 12, 0, 0, 0, time.UTC)
	testExpires = testNow.Add(24 * time.Hour)
)

type testKey struct {
	id   string
	pub  ed25519.PublicKey
	priv ed25519.PrivateKey
}

func newTestKey(t *testing.T, id string) testKey {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	return testKey{id: id, pub: pub, priv: priv}
}

func (k testKey) layoutKey() Key {
	return Key{KeyID: k.id, Type: KeyTypeEd25519, Value: k.pub}
}

func (k testKey) sign(link Link) Signature {
	return Signature{KeyID: k.id, Value: ed25519.Sign(k.priv, link.signingBytes())}
}

func buildLink() Link {
	return Link{
		Name:      "build",
		Materials: map[string]string{"git://repo": "sha256:aaa"},
		Products:  map[string]string{"bin/app": "sha256:bbb"},
	}
}

func TestValidMultiSignature(t *testing.T) {
	k1 := newTestKey(t, "alice")
	k2 := newTestKey(t, "bob")
	k3 := newTestKey(t, "carol")
	layout := Layout{
		StepName:  "build",
		Keys:      []Key{k1.layoutKey(), k2.layoutKey(), k3.layoutKey()},
		Threshold: 3,
		Expires:   testExpires,
	}
	link := buildLink()
	link.Signatures = []Signature{k1.sign(link), k2.sign(link), k3.sign(link)}

	got, err := VerifyStep(layout, []Link{link}, testNow)
	if err != nil {
		t.Fatalf("VerifyStep: %v", err)
	}
	want := []string{"alice", "bob", "carol"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("signers = %v, want %v", got, want)
	}
}

func TestDuplicateSignaturesCountOnce(t *testing.T) {
	k1 := newTestKey(t, "alice")
	k2 := newTestKey(t, "bob")
	layout := Layout{
		StepName:  "build",
		Keys:      []Key{k1.layoutKey(), k2.layoutKey()},
		Threshold: 2,
		Expires:   testExpires,
	}
	link := buildLink()
	// Attacker replays alice's signature many times; only bob adds a second.
	sig := k1.sign(link)
	link.Signatures = []Signature{sig, sig, sig, sig}

	if _, err := VerifyStep(layout, []Link{link}, testNow); !errors.Is(err, ErrThresholdNotMet) {
		t.Fatalf("err = %v, want ErrThresholdNotMet", err)
	}

	link.Signatures = append(link.Signatures, k2.sign(link))
	if _, err := VerifyStep(layout, []Link{link}, testNow); err != nil {
		t.Fatalf("VerifyStep with second distinct key: %v", err)
	}
}

func TestAliasKeysMergedByFingerprint(t *testing.T) {
	k1 := newTestKey(t, "alice")
	k2 := newTestKey(t, "bob")
	// Alias: different KeyID, same public key material as alice.
	alias := Key{KeyID: "alice-alias", Type: KeyTypeEd25519, Value: k1.pub}
	layout := Layout{
		StepName:  "build",
		Keys:      []Key{k1.layoutKey(), alias, k2.layoutKey()},
		Threshold: 2,
		Expires:   testExpires,
	}
	link := buildLink()
	// Same private key signs under both KeyIDs.
	link.Signatures = []Signature{
		k1.sign(link),
		{KeyID: "alice-alias", Value: ed25519.Sign(k1.priv, link.signingBytes())},
	}

	if _, err := VerifyStep(layout, []Link{link}, testNow); !errors.Is(err, ErrThresholdNotMet) {
		t.Fatalf("err = %v, want ErrThresholdNotMet (alias must not count twice)", err)
	}

	link.Signatures = append(link.Signatures, k2.sign(link))
	got, err := VerifyStep(layout, []Link{link}, testNow)
	if err != nil {
		t.Fatalf("VerifyStep: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("signers = %v, want 2 distinct", got)
	}
}

func TestDigestDivergenceDoesNotCount(t *testing.T) {
	k1 := newTestKey(t, "alice")
	k2 := newTestKey(t, "bob")
	k3 := newTestKey(t, "carol")
	layout := Layout{
		StepName:  "build",
		Keys:      []Key{k1.layoutKey(), k2.layoutKey(), k3.layoutKey()},
		Threshold: 3,
		Expires:   testExpires,
	}
	good := buildLink()
	divergent := buildLink()
	divergent.Products["bin/app"] = "sha256:evil"

	links := []Link{
		{Name: good.Name, Materials: good.Materials, Products: good.Products,
			Signatures: []Signature{k1.sign(good), k2.sign(good)}},
		{Name: divergent.Name, Materials: divergent.Materials, Products: divergent.Products,
			Signatures: []Signature{k3.sign(divergent)}},
	}
	if _, err := VerifyStep(layout, links, testNow); !errors.Is(err, ErrThresholdNotMet) {
		t.Fatalf("err = %v, want ErrThresholdNotMet (divergent digest must not凑数)", err)
	}

	// Carol re-attests to the agreeing digest: now 3 agree.
	good.Signatures = []Signature{k1.sign(good), k2.sign(good), k3.sign(good)}
	got, err := VerifyStep(layout, []Link{good}, testNow)
	if err != nil {
		t.Fatalf("VerifyStep: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("signers = %v, want 3", got)
	}
}

func TestThresholdBoundary(t *testing.T) {
	k1 := newTestKey(t, "alice")
	k2 := newTestKey(t, "bob")
	k3 := newTestKey(t, "carol")
	layout := Layout{
		StepName:  "build",
		Keys:      []Key{k1.layoutKey(), k2.layoutKey(), k3.layoutKey()},
		Threshold: 2,
		Expires:   testExpires,
	}
	link := buildLink()

	// k-1 signers: reject.
	link.Signatures = []Signature{k1.sign(link)}
	if _, err := VerifyStep(layout, []Link{link}, testNow); !errors.Is(err, ErrThresholdNotMet) {
		t.Fatalf("k-1: err = %v, want ErrThresholdNotMet", err)
	}

	// exactly k signers: accept.
	link.Signatures = append(link.Signatures, k2.sign(link))
	got, err := VerifyStep(layout, []Link{link}, testNow)
	if err != nil {
		t.Fatalf("exactly k: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("signers = %v, want 2", got)
	}
}

func TestExpiredLayoutRejected(t *testing.T) {
	k1 := newTestKey(t, "alice")
	k2 := newTestKey(t, "bob")
	layout := Layout{
		StepName:  "build",
		Keys:      []Key{k1.layoutKey(), k2.layoutKey()},
		Threshold: 2,
		Expires:   testExpires,
	}
	link := buildLink()
	link.Signatures = []Signature{k1.sign(link), k2.sign(link)}

	// Even with enough valid signatures, an expired layout rejects all.
	if _, err := VerifyStep(layout, []Link{link}, testExpires); !errors.Is(err, ErrLayoutExpired) {
		t.Fatalf("at expiry: err = %v, want ErrLayoutExpired", err)
	}
	if _, err := VerifyStep(layout, []Link{link}, testExpires.Add(time.Hour)); !errors.Is(err, ErrLayoutExpired) {
		t.Fatalf("past expiry: err = %v, want ErrLayoutExpired", err)
	}
}

func TestUnauthorizedAndInvalidSignaturesIgnored(t *testing.T) {
	k1 := newTestKey(t, "alice")
	k2 := newTestKey(t, "bob")
	outsider := newTestKey(t, "mallory")
	layout := Layout{
		StepName:  "build",
		Keys:      []Key{k1.layoutKey(), k2.layoutKey()},
		Threshold: 2,
		Expires:   testExpires,
	}
	link := buildLink()
	forged := k1.sign(link)
	forged.Value[0] ^= 0xff // corrupt the signature
	link.Signatures = []Signature{
		outsider.sign(link), // not in the layout's key set
		forged,              // invalid signature bytes
		k1.sign(link),
	}

	if _, err := VerifyStep(layout, []Link{link}, testNow); !errors.Is(err, ErrThresholdNotMet) {
		t.Fatalf("err = %v, want ErrThresholdNotMet", err)
	}

	link.Signatures = append(link.Signatures, k2.sign(link))
	if _, err := VerifyStep(layout, []Link{link}, testNow); err != nil {
		t.Fatalf("VerifyStep: %v", err)
	}
}

func TestWrongStepNameIgnored(t *testing.T) {
	k1 := newTestKey(t, "alice")
	k2 := newTestKey(t, "bob")
	layout := Layout{
		StepName:  "build",
		Keys:      []Key{k1.layoutKey(), k2.layoutKey()},
		Threshold: 2,
		Expires:   testExpires,
	}
	link := buildLink()
	link.Name = "package" // different step
	link.Signatures = []Signature{k1.sign(link), k2.sign(link)}

	if _, err := VerifyStep(layout, []Link{link}, testNow); !errors.Is(err, ErrThresholdNotMet) {
		t.Fatalf("err = %v, want ErrThresholdNotMet", err)
	}
}
