package netsec

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"strings"
	"testing"
)

func testSigner(t *testing.T, name string, seed []byte) *Ed25519Signer {
	t.Helper()
	if len(seed) != ed25519.SeedSize {
		t.Fatalf("bad seed length %d", len(seed))
	}
	priv := ed25519.NewKeyFromSeed(seed)
	signer, err := NewSigner(name, priv)
	if err != nil {
		t.Fatalf("NewSigner(%q): %v", name, err)
	}
	return signer
}

func checkpointBody(origin string, size uint64, root []byte) []byte {
	return (&Checkpoint{Origin: origin, TreeSize: size, Root: root}).Marshal()
}

func signLine(t *testing.T, signer Signer, body []byte) string {
	t.Helper()
	raw, err := signer.Sign(body)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	hash := make([]byte, 4)
	h := signer.KeyHash()
	hash[0] = byte(h >> 24)
	hash[1] = byte(h >> 16)
	hash[2] = byte(h >> 8)
	hash[3] = byte(h)
	return emDash + signer.Name() + " " +
		base64.StdEncoding.EncodeToString(append(hash, raw...))
}

func trustedSet(keys ...*Ed25519Key) *Ed25519Verifiers {
	return &Ed25519Verifiers{Keys: keys}
}

func TestOpenMultiSignatureThreshold(t *testing.T) {
	alice := testSigner(t, "alice", bytes.Repeat([]byte{0x01}, ed25519.SeedSize))
	bob := testSigner(t, "bob", bytes.Repeat([]byte{0x02}, ed25519.SeedSize))
	carol := testSigner(t, "carol", bytes.Repeat([]byte{0x03}, ed25519.SeedSize))

	body := checkpointBody("example.log", 42, []byte{0xde, 0xad, 0xbe, 0xef})
	note, err := Sign([]Signer{alice, bob, carol}, body)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}

	trusted := trustedSet(&alice.Ed25519Key, &bob.Ed25519Key, &carol.Ed25519Key)
	trusted.Threshold = 2

	opened, err := Open(trusted, note)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if !bytes.Equal(opened.Text, body) {
		t.Fatalf("body mismatch:\n got %q\nwant %q", opened.Text, body)
	}
	if len(opened.Signatures) != 3 {
		t.Fatalf("got %d accepted signatures, want 3", len(opened.Signatures))
	}

	trusted.Threshold = 3
	if _, err := Open(trusted, note); err != nil {
		t.Fatalf("Open threshold 3: %v", err)
	}

	// Removing carol's line drops below the threshold of three.
	carolLine := signLine(t, carol, body)
	trimmed := strings.TrimSuffix(string(note), carolLine+"\n")
	if _, err := Open(trusted, []byte(trimmed)); err == nil {
		t.Fatal("Open accepted a note missing a required signature")
	}
}

func TestDuplicateSignerCountedOnce(t *testing.T) {
	alice := testSigner(t, "alice", bytes.Repeat([]byte{0x11}, ed25519.SeedSize))
	body := checkpointBody("example.log", 7, []byte("root"))

	line := signLine(t, alice, body)
	note := append(append([]byte(nil), body...), '\n')
	note = append(note, []byte(line+"\n"+line+"\n")...)

	trusted := trustedSet(&alice.Ed25519Key)
	trusted.Threshold = 2
	if _, err := Open(trusted, note); err == nil {
		t.Fatal("Open counted the same signer twice toward the threshold")
	}

	trusted.Threshold = 1
	opened, err := Open(trusted, note)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if len(opened.Signatures) != 1 {
		t.Fatalf("got %d signatures, want duplicate collapsed to 1", len(opened.Signatures))
	}
}

func TestUnknownSignerIgnored(t *testing.T) {
	alice := testSigner(t, "alice", bytes.Repeat([]byte{0x21}, ed25519.SeedSize))
	mallory := testSigner(t, "mallory", bytes.Repeat([]byte{0x22}, ed25519.SeedSize))
	body := checkpointBody("example.log", 9, []byte("root"))

	note, err := Sign([]Signer{alice, mallory}, body)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}

	trusted := trustedSet(&alice.Ed25519Key)
	opened, err := Open(trusted, note)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if len(opened.Signatures) != 1 || opened.Signatures[0].Name != "alice" {
		t.Fatalf("unexpected accepted signatures: %+v", opened.Signatures)
	}
}

func TestForgedAndTamperedNotesRejected(t *testing.T) {
	alice := testSigner(t, "alice", bytes.Repeat([]byte{0x31}, ed25519.SeedSize))
	body := checkpointBody("example.log", 5, []byte("root"))
	note, err := Sign([]Signer{alice}, body)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	trusted := trustedSet(&alice.Ed25519Key)

	tampered := bytes.Replace(note, []byte("example.log"), []byte("evil.example"), 1)
	if _, err := Open(trusted, tampered); err == nil {
		t.Fatal("Open accepted a tampered body")
	}

	// A valid alice signature spliced onto a different checkpoint body fails.
	other := checkpointBody("example.log", 6, []byte("other-root"))
	forged := append(append([]byte(nil), other...), '\n')
	forged = append(forged, []byte(signLine(t, alice, body)+"\n")...)
	if _, err := Open(trusted, forged); err == nil {
		t.Fatal("Open accepted a signature spliced from another checkpoint")
	}

	// A different key reusing alice's name has a non-matching hash.
	impostor := testSigner(t, "alice", bytes.Repeat([]byte{0x32}, ed25519.SeedSize))
	fakeNote, err := Sign([]Signer{impostor}, body)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	if _, err := Open(trusted, fakeNote); err == nil {
		t.Fatal("Open accepted a different key using the same verifier name")
	}
}

func TestInlineNewlineBody(t *testing.T) {
	alice := testSigner(t, "alice", bytes.Repeat([]byte{0x41}, ed25519.SeedSize))
	// An interior newline is part of the signed body; only the blank
	// separator line introduces signatures.
	body := []byte("origin\n1\ncm9vdA==\nextension-line\n")

	note, err := Sign([]Signer{alice}, body)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	trusted := trustedSet(&alice.Ed25519Key)
	opened, err := Open(trusted, note)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if !bytes.Equal(opened.Text, body) {
		t.Fatalf("body mismatch:\n got %q\nwant %q", opened.Text, body)
	}

	// A missing trailing newline is canonicalized before signing.
	noNewline := bytes.TrimSuffix(body, []byte("\n"))
	note2, err := Sign([]Signer{alice}, noNewline)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	opened2, err := Open(trusted, note2)
	if err != nil {
		t.Fatalf("Open canonicalized body: %v", err)
	}
	if !bytes.HasSuffix(opened2.Text, []byte("\n")) {
		t.Fatal("opened body was not canonicalized to end in newline")
	}

	if _, err := ParseCheckpoint(body); err == nil {
		t.Fatal("ParseCheckpoint accepted an extra extension line")
	}
}

func TestKeyHashVector(t *testing.T) {
	name := "test-key"
	pub := bytes.Repeat([]byte{0x7a}, ed25519.PublicKeySize)
	key, err := NewVerifier(name, ed25519.PublicKey(pub))
	if err != nil {
		t.Fatalf("NewVerifier: %v", err)
	}

	h := sha256.New()
	h.Write([]byte(name))
	h.Write([]byte("\n\x01"))
	h.Write(pub)
	sum := h.Sum(nil)
	want := uint32(sum[0])<<24 | uint32(sum[1])<<16 | uint32(sum[2])<<8 | uint32(sum[3])
	if key.KeyHash() != want {
		t.Fatalf("KeyHash = %08x, want %08x", key.KeyHash(), want)
	}
}

func TestLeafHash(t *testing.T) {
	alice := testSigner(t, "alice", bytes.Repeat([]byte{0x51}, ed25519.SeedSize))
	body := checkpointBody("example.log", 11, []byte{1, 2, 3, 4})
	note, err := Sign([]Signer{alice}, body)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	trusted := trustedSet(&alice.Ed25519Key)
	opened, err := Open(trusted, note)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	h := sha256.Sum256(append([]byte{0x00}, body...))
	if opened.LeafHash() != h || LeafHash(body) != h {
		t.Fatalf("LeafHash = %x, want %x", opened.LeafHash(), h)
	}
}

func TestMalformedNotes(t *testing.T) {
	alice := testSigner(t, "alice", bytes.Repeat([]byte{0x61}, ed25519.SeedSize))
	body := checkpointBody("example.log", 3, []byte("r"))
	good, err := Sign([]Signer{alice}, body)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	trusted := trustedSet(&alice.Ed25519Key)

	cases := map[string][]byte{
		"no signatures":            body,
		"no blank separator":       bytes.Replace(good, []byte("\n"+emDash), []byte("\nX "), 1),
		"signature missing name":   append(append([]byte(nil), body...), []byte(emDash+" "+base64.StdEncoding.EncodeToString([]byte("abcd"))+"\n")...),
		"signature bad base64":     append(append([]byte(nil), body...), []byte(emDash+"alice !!!not-base64!!!\n")...),
		"signature too short":      append(append([]byte(nil), body...), []byte(emDash+"alice "+base64.StdEncoding.EncodeToString([]byte("ab"))+"\n")...),
		"ascii hyphen not em dash": bytes.Replace(good, []byte(emDash), []byte("- "), 1),
		"invalid name with space":  append(append([]byte(nil), body...), []byte(emDash+"bad name x\n")...),
	}
	for label, input := range cases {
		if _, err := Open(trusted, input); err == nil {
			t.Errorf("case %q: expected error, got nil", label)
		}
	}

	big := bytes.Repeat([]byte("x"), MaxNoteSize+1)
	if _, err := Open(trusted, big); err == nil {
		t.Error("expected oversized note to be rejected")
	}
}

func TestParseCheckpoint(t *testing.T) {
	root := []byte{0, 1, 2, 3, 255}
	body := checkpointBody("example.log", 1234, root)
	cp, err := ParseCheckpoint(body)
	if err != nil {
		t.Fatalf("ParseCheckpoint: %v", err)
	}
	if cp.Origin != "example.log" || cp.TreeSize != 1234 || !bytes.Equal(cp.Root, root) {
		t.Fatalf("unexpected checkpoint: %+v", cp)
	}
	if !bytes.Equal(cp.Marshal(), body) {
		t.Fatalf("round trip mismatch:\n got %q\nwant %q", cp.Marshal(), body)
	}

	bad := []string{
		"example.log\n12",
		"example.log\n00\ncm9vdA==\n",
		"example.log\n-1\ncm9vdA==\n",
		"example.log\nabc\ncm9vdA==\n",
		"example.log\n1\n!!!\n",
		"\n1\ncm9vdA==\n",
		"example.log\n1\ncm9vdA==\nextra\n",
	}
	for _, input := range bad {
		if _, err := ParseCheckpoint([]byte(input)); err == nil {
			t.Errorf("ParseCheckpoint(%q) expected error, got nil", input)
		}
	}
}

func TestSignRejectsDuplicateSigner(t *testing.T) {
	alice := testSigner(t, "alice", bytes.Repeat([]byte{0x71}, ed25519.SeedSize))
	body := checkpointBody("example.log", 1, []byte("r"))
	if _, err := Sign([]Signer{alice, alice}, body); err == nil {
		t.Fatal("Sign accepted two signers with the same key hash")
	}
}
