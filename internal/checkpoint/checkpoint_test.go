package checkpoint

import (
	"crypto/ed25519"
	"encoding/base64"
	"strings"
	"testing"

	"example.org/offlinecli/internal/tlog"
)

func newTestKey(t *testing.T) (ed25519.PublicKey, ed25519.PrivateKey) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	return pub, priv
}

func TestSignAndVerifyRoundTrip(t *testing.T) {
	pub, priv := newTestKey(t)
	signer, err := NewSigner("log.example", "example-log", priv)
	if err != nil {
		t.Fatal(err)
	}
	verifier, err := NewVerifier("log.example", "example-log", pub)
	if err != nil {
		t.Fatal(err)
	}

	root := tlog.RecordHash([]byte("root"))
	env, err := signer.Sign(7, root)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := verifier.Parse(env.Text)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if parsed.Origin != "example-log" || parsed.Size != 7 || parsed.Hash != root {
		t.Fatalf("round trip mismatch: %+v", parsed.Checkpoint)
	}
	if string(parsed.Text) != string(env.Text) {
		t.Fatal("envelope text not preserved byte-for-byte")
	}
}

func TestRejectTamperedHeader(t *testing.T) {
	pub, priv := newTestKey(t)
	signer, _ := NewSigner("log.example", "example-log", priv)
	verifier, _ := NewVerifier("log.example", "example-log", pub)

	env, err := signer.Sign(3, tlog.RecordHash([]byte("r")))
	if err != nil {
		t.Fatal(err)
	}

	// Flip each header byte in turn; the signature must stop validating.
	headerEnd := strings.Index(string(env.Text), "\n\n")
	for i := 0; i < headerEnd; i++ {
		bad := append([]byte{}, env.Text...)
		bad[i] ^= 0x01
		if _, err := verifier.Parse(bad); err == nil {
			t.Fatalf("tampered header accepted at byte %d", i)
		}
	}
}

func TestRejectForeignOriginAndKey(t *testing.T) {
	pub, priv := newTestKey(t)
	signer, _ := NewSigner("log.example", "origin-a", priv)

	env, err := signer.Sign(2, tlog.RecordHash([]byte("r")))
	if err != nil {
		t.Fatal(err)
	}

	otherOrigin, err := NewVerifier("log.example", "origin-b", pub)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := otherOrigin.Parse(env.Text); err == nil {
		t.Fatal("foreign origin accepted")
	}

	otherPub, _ := newTestKey(t)
	otherKey, err := NewVerifier("log.example", "origin-a", otherPub)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := otherKey.Parse(env.Text); err == nil {
		t.Fatal("envelope accepted under an untrusted key")
	}

	otherName, err := NewVerifier("different.example", "origin-a", pub)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := otherName.Parse(env.Text); err == nil {
		t.Fatal("envelope accepted under a different key name")
	}
}

func TestRejectMalformedEnvelopes(t *testing.T) {
	pub, priv := newTestKey(t)
	signer, _ := NewSigner("log.example", "origin-a", priv)
	verifier, _ := NewVerifier("log.example", "origin-a", pub)

	env, _ := signer.Sign(1, tlog.RecordHash([]byte("r")))

	noSig := []byte("origin-a\n1\n" + b64(tlog.RecordHash([]byte("r"))) + "\n")
	cases := map[string][]byte{
		"no signature block":   noSig,
		"truncated":            env.Text[:len(env.Text)-3],
		"extra signature line": append(append([]byte{}, env.Text...), []byte("— other abcd\n")...),
		"non-numeric size":     rebuildHeader(env, "origin-a", "NaN", b64(tlog.RecordHash([]byte("r")))),
		"zero size":            rebuildHeader(env, "origin-a", "0", b64(tlog.RecordHash([]byte("r")))),
		"short hash":           rebuildHeader(env, "origin-a", "1", "AAAA"),
	}
	for name, text := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := verifier.Parse(text); err == nil {
				t.Fatalf("%s accepted", name)
			}
		})
	}
}

func b64(h tlog.Hash) string {
	return base64.StdEncoding.EncodeToString(h[:])
}

// rebuildHeader replaces the three header lines of a valid envelope while
// keeping its signature block, producing a structurally invalid message.
func rebuildHeader(env *Envelope, origin, size, hash string) []byte {
	idx := strings.Index(string(env.Text), "\n\n")
	sigBlock := env.Text[idx:]
	return append([]byte(origin+"\n"+size+"\n"+hash+"\n"), sigBlock...)
}

func TestSignerRejectsBadInputs(t *testing.T) {
	_, priv := newTestKey(t)
	signer, err := NewSigner("log.example", "o", priv)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := signer.Sign(0, tlog.Hash{}); err == nil {
		t.Fatal("accepted size zero")
	}
	if _, err := NewSigner("", "o", priv); err == nil {
		t.Fatal("accepted empty key name")
	}
	var shortKey ed25519.PrivateKey = make([]byte, 10)
	if _, err := NewSigner("n", "o", shortKey); err == nil {
		t.Fatal("accepted malformed private key")
	}
}
