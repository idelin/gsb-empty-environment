package netsec_test

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	netsec "example.org/netsec/q0116"
)

var b64 = base64.RawURLEncoding

func mustKey(t *testing.T) ed25519.PrivateKey {
	t.Helper()
	_, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return key
}

func disclosure(salt, name string, value any) string {
	raw, _ := json.Marshal([]any{salt, name, value})
	return b64.EncodeToString(raw)
}

func issuerJWT(t *testing.T, issuerKey ed25519.PrivateKey, jkt string, disclosures []string) string {
	t.Helper()
	digests := make([]string, 0, len(disclosures))
	for _, d := range disclosures {
		digests = append(digests, netsec.SDHash("", []string{d})[:22])
	}
	header := map[string]any{"alg": "EdDSA", "typ": "vc+sd-jwt"}
	payload := map[string]any{
		"iss": "https://issuer.example",
		"cnf": map[string]any{"jkt": jkt},
		"_sd": digests,
	}
	h, _ := json.Marshal(header)
	p, _ := json.Marshal(payload)
	input := b64.EncodeToString(h) + "." + b64.EncodeToString(p)
	sig := ed25519.Sign(issuerKey, []byte(input))
	return input + "." + b64.EncodeToString(sig)
}

type fixture struct {
	issuerJWT   string
	disclosures []string
	holderKey   ed25519.PrivateKey
	kbJWT       string
}

func newFixture(t *testing.T) fixture {
	t.Helper()
	issuerKey := mustKey(t)
	holderKey := mustKey(t)
	holderPub := holderKey.Public().(ed25519.PublicKey)
	disclosures := []string{
		disclosure("salt-1", "given_name", "Alice"),
		disclosure("salt-2", "birthdate", "1990-01-01"),
		disclosure("salt-3", "country", "CN"),
	}
	iss := issuerJWT(t, issuerKey, netsec.Thumbprint(holderPub), disclosures)
	kb, err := netsec.SignKeyBinding(holderKey, iss, disclosures, "https://verifier.example", "nonce-1")
	if err != nil {
		t.Fatal(err)
	}
	return fixture{issuerJWT: iss, disclosures: disclosures, holderKey: holderKey, kbJWT: kb}
}

func (f fixture) presentation(disclosures []string, kb string) string {
	return netsec.AssemblePresentation(f.issuerJWT, disclosures, kb)
}

func TestAcceptsUnchangedDisclosuresWithMatchingJKT(t *testing.T) {
	f := newFixture(t)
	v, err := netsec.VerifyKeyBinding(f.presentation(f.disclosures, f.kbJWT))
	if err != nil {
		t.Fatalf("expected acceptance, got %v", err)
	}
	if len(v.Disclosures) != len(f.disclosures) {
		t.Fatalf("got %d disclosures, want %d", len(v.Disclosures), len(f.disclosures))
	}
	if v.KeyBindingClaims["sd_hash"] != netsec.SDHash(f.issuerJWT, f.disclosures) {
		t.Fatal("sd_hash claim does not match canonical disclosure bytes")
	}
}

func TestAcceptsNewProofForDifferentDisclosureSet(t *testing.T) {
	f := newFixture(t)
	subset := f.disclosures[:2]
	kb, err := netsec.SignKeyBinding(f.holderKey, f.issuerJWT, subset, "https://verifier.example", "nonce-2")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := netsec.VerifyKeyBinding(f.presentation(subset, kb)); err != nil {
		t.Fatalf("fresh proof over a different disclosure set must verify, got %v", err)
	}
}

func TestRejectsOriginalProofAfterAddingDisclosure(t *testing.T) {
	f := newFixture(t)
	extended := append(append([]string{}, f.disclosures...), disclosure("salt-4", "email", "alice@example.com"))
	_, err := netsec.VerifyKeyBinding(f.presentation(extended, f.kbJWT))
	if !errors.Is(err, netsec.ErrSDHashMismatch) {
		t.Fatalf("got %v, want ErrSDHashMismatch", err)
	}
}

func TestRejectsOriginalProofAfterRemovingDisclosure(t *testing.T) {
	f := newFixture(t)
	_, err := netsec.VerifyKeyBinding(f.presentation(f.disclosures[:2], f.kbJWT))
	if !errors.Is(err, netsec.ErrSDHashMismatch) {
		t.Fatalf("got %v, want ErrSDHashMismatch", err)
	}
}

func TestRejectsWrongTyp(t *testing.T) {
	f := newFixture(t)
	parts := strings.Split(f.kbJWT, ".")
	header, _ := b64.DecodeString(parts[0])
	var h map[string]any
	if err := json.Unmarshal(header, &h); err != nil {
		t.Fatal(err)
	}
	h["typ"] = "jwt"
	raw, _ := json.Marshal(h)
	parts[0] = b64.EncodeToString(raw)
	// Re-sign so only the typ check can fail.
	sig := ed25519.Sign(f.holderKey, []byte(parts[0]+"."+parts[1]))
	parts[2] = b64.EncodeToString(sig)
	_, err := netsec.VerifyKeyBinding(f.presentation(f.disclosures, strings.Join(parts, ".")))
	if !errors.Is(err, netsec.ErrUnexpectedTyp) {
		t.Fatalf("got %v, want ErrUnexpectedTyp", err)
	}
}

func TestRejectsJKTMismatch(t *testing.T) {
	f := newFixture(t)
	otherKey := mustKey(t)
	otherPub := otherKey.Public().(ed25519.PublicKey)
	issuerKey := mustKey(t)
	iss := issuerJWT(t, issuerKey, netsec.Thumbprint(otherPub), f.disclosures)
	kb, err := netsec.SignKeyBinding(f.holderKey, iss, f.disclosures, "", "")
	if err != nil {
		t.Fatal(err)
	}
	_, err = netsec.VerifyKeyBinding(netsec.AssemblePresentation(iss, f.disclosures, kb))
	if !errors.Is(err, netsec.ErrJKTMismatch) {
		t.Fatalf("got %v, want ErrJKTMismatch", err)
	}
}

func TestRejectsForgedSignature(t *testing.T) {
	f := newFixture(t)
	parts := strings.Split(f.kbJWT, ".")
	forged := mustKey(t)
	parts[2] = b64.EncodeToString(ed25519.Sign(forged, []byte(parts[0]+"."+parts[1])))
	_, err := netsec.VerifyKeyBinding(f.presentation(f.disclosures, strings.Join(parts, ".")))
	if !errors.Is(err, netsec.ErrSignatureInvalid) {
		t.Fatalf("got %v, want ErrSignatureInvalid", err)
	}
}

func TestVerifyWithOutOfBandKey(t *testing.T) {
	f := newFixture(t)
	holderPub := f.holderKey.Public().(ed25519.PublicKey)
	v, err := netsec.VerifyKeyBindingWithKey(f.presentation(f.disclosures, f.kbJWT), holderPub)
	if err != nil {
		t.Fatalf("expected acceptance, got %v", err)
	}
	if netsec.Thumbprint(v.HolderPublicKey) != netsec.Thumbprint(holderPub) {
		t.Fatal("resolved holder key mismatch")
	}
}

func TestRejectsPresentationWithoutKeyBinding(t *testing.T) {
	f := newFixture(t)
	bare := f.issuerJWT + "~" + strings.Join(f.disclosures, "~") + "~"
	if _, err := netsec.VerifyKeyBinding(bare); !errors.Is(err, netsec.ErrMalformedPresentation) {
		t.Fatalf("got %v, want ErrMalformedPresentation", err)
	}
}

func ExampleVerifyKeyBinding() {
	_, holderKey, _ := ed25519.GenerateKey(rand.Reader)
	holderPub := holderKey.Public().(ed25519.PublicKey)
	header, _ := json.Marshal(map[string]any{"alg": "EdDSA", "typ": "vc+sd-jwt"})
	payload, _ := json.Marshal(map[string]any{
		"iss": "https://issuer.example",
		"cnf": map[string]any{"jkt": netsec.Thumbprint(holderPub)},
	})
	input := b64.EncodeToString(header) + "." + b64.EncodeToString(payload)
	iss := input + "." + b64.EncodeToString(ed25519.Sign(holderKey, []byte(input)))
	discs := []string{disclosure("s", "given_name", "Alice")}
	kb, _ := netsec.SignKeyBinding(holderKey, iss, discs, "https://verifier.example", "n-1")
	presentation := netsec.AssemblePresentation(iss, discs, kb)
	_, err := netsec.VerifyKeyBinding(presentation)
	fmt.Println(err == nil)
	// Output: true
}
