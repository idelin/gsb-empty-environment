package netsec

import (
	"bufio"
	"bytes"
	"errors"
	"io"
	"testing"
)

// TestUnknownStanzaRetainedAndMACed parses a valid header carrying an unknown
// stanza type and confirms the stanza is retained verbatim, participates in
// the HMAC, and that any byte-level modification invalidates authentication.
func TestUnknownStanzaRetainedAndMACed(t *testing.T) {
	identity, err := GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer
	w, err := Encrypt(&buf, identity.Recipient())
	if err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}

	// Insert an unknown stanza directly into the raw header and recompute a
	// valid file: the parser must keep it and expose it in Stanzas.
	header, payload := splitHeaderPayload(t, buf.Bytes())
	unknown := &Stanza{Type: "future-extension", Args: [][]byte{[]byte("42")}, Body: []byte{1, 2, 3, 4}}
	insertAt := bytes.Index([]byte(header), []byte("X25519 "))
	withUnknown := append([]byte{}, header[:insertAt]...)
	withUnknown = append(withUnknown, MarshalStanza(unknown)...)
	withUnknown = append(withUnknown, header[insertAt:]...)

	r := bufio.NewReader(bytes.NewReader(append(withUnknown, payload...)))
	h, pr, err := ParseHeader(r)
	if err != nil {
		t.Fatalf("ParseHeader: %v", err)
	}
	var found bool
	for _, s := range h.Stanzas {
		if s.Type == "future-extension" {
			found = true
			if !bytes.Equal(s.Body, []byte{1, 2, 3, 4}) || len(s.Args) != 1 || string(s.Args[0]) != "42" {
				t.Fatalf("unknown stanza not preserved: %+v", s)
			}
		}
	}
	if !found {
		t.Fatal("unknown stanza was dropped during parsing")
	}

	// The added line changes the authenticated prefix but not the MAC, so the
	// reader must remain sealed even though X25519 unwrap would succeed.
	fileKey, err := identity.Unwrap(h.Stanzas)
	if err != nil {
		t.Fatalf("Unwrap: %v", err)
	}
	if err := h.VerifyMAC(fileKey); err == nil {
		t.Fatal("MAC must fail when an unknown stanza is inserted")
	}
	probe := make([]byte, 32)
	if n, rerr := pr.Read(probe); n != 0 || !errors.Is(rerr, ErrHeaderNotAuthenticated) {
		t.Fatalf("sealed Read: n=%d err=%v", n, rerr)
	}
}

// TestParseThenVerifyLifecycle exercises the documented entry sequence
// directly: parse -> unwrap -> verify -> read, including a failed verify that
// keeps the reader permanently sealed.
func TestParseThenVerifyLifecycle(t *testing.T) {
	plaintext := bytes.Repeat([]byte("lifecycle-"), 1000)
	file := buildScryptFile(t, "lifecycle-pass", plaintext)

	r := bufio.NewReader(bytes.NewReader(file))
	header, payload, err := ParseHeader(r)
	if err != nil {
		t.Fatalf("ParseHeader: %v", err)
	}
	if header.Authenticated() {
		t.Fatal("freshly parsed header must not be authenticated")
	}

	// Before unwrap/verify, reads are refused.
	probe := make([]byte, 16)
	if n, err := payload.Read(probe); n != 0 || !errors.Is(err, ErrHeaderNotAuthenticated) {
		t.Fatalf("early Read: n=%d err=%v", n, err)
	}

	fileKey, err := (&ScryptIdentity{Passphrase: []byte("lifecycle-pass")}).Unwrap(header.Stanzas)
	if err != nil {
		t.Fatalf("Unwrap: %v", err)
	}
	if err := header.VerifyMAC(fileKey); err != nil {
		t.Fatalf("VerifyMAC: %v", err)
	}
	if !header.Authenticated() || payload.Sealed() {
		t.Fatal("reader should be unlocked after successful verification")
	}
	got, err := io.ReadAll(payload)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if !bytes.Equal(got, plaintext) {
		t.Fatal("decrypted payload does not match plaintext")
	}
}

// TestWrongPassphraseSealsPayload confirms a wrong passphrase cannot reach the
// MAC step and the payload reader never emits bytes.
func TestWrongPassphraseSealsPayload(t *testing.T) {
	file := buildScryptFile(t, "right", []byte("secret bytes"))
	_, payload, err := Decrypt(bufio.NewReader(bytes.NewReader(file)), &ScryptIdentity{Passphrase: []byte("wrong")})
	if err == nil {
		t.Fatal("Decrypt with wrong passphrase must fail")
	}
	if payload != nil {
		probe := make([]byte, 8)
		if n, rerr := payload.Read(probe); n != 0 || rerr == nil {
			t.Fatalf("wrong-passphrase Read: n=%d err=%v", n, rerr)
		}
	}
}

// TestMultipleX25519Recipients round-trips a header with two distinct key
// recipients, proving the normal multi-stanza path is unaffected by the
// scrypt-only uniqueness rule.
func TestMultipleX25519Recipients(t *testing.T) {
	id1, _ := GenerateX25519Identity()
	id2, _ := GenerateX25519Identity()
	plaintext := []byte("two keys, one file")

	var buf bytes.Buffer
	w, err := Encrypt(&buf, id1.Recipient(), id2.Recipient())
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	if _, err := w.Write(plaintext); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}

	for _, id := range []*X25519Identity{id1, id2} {
		_, payload, err := Decrypt(bufio.NewReader(bytes.NewReader(buf.Bytes())), id)
		if err != nil {
			t.Fatalf("Decrypt with identity: %v", err)
		}
		got, err := io.ReadAll(payload)
		if err != nil || !bytes.Equal(got, plaintext) {
			t.Fatalf("identity read: %v %q", err, got)
		}
	}
}
