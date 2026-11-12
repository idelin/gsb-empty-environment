package netsec

import (
	"bufio"
	"bytes"
	"encoding/base64"
	"errors"
	"io"
	"strings"
	"testing"
)

// buildScryptFile returns the full encrypted file (header + STREAM payload)
// protected with a passphrase. scrypt work factor is kept low for tests.
func buildScryptFile(t *testing.T, passphrase string, plaintext []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	w, err := Encrypt(&buf, &ScryptRecipient{
		Passphrase: []byte(passphrase),
		LogN:       10,
	})
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	if _, err := w.Write(plaintext); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	return buf.Bytes()
}

// splitHeaderPayload separates the header bytes (through the MAC newline)
// from the encrypted payload, so tests can mutate only header bytes.
func splitHeaderPayload(t *testing.T, file []byte) (string, []byte) {
	t.Helper()
	idx := bytes.Index(file, []byte("\n--- hmac "))
	if idx < 0 {
		t.Fatal("MAC line not found")
	}
	// idx points at the newline opening the MAC line; locate the newline that
	// terminates it by searching strictly after the MAC prefix.
	macStart := idx + 1
	endRel := bytes.IndexByte(file[macStart:], '\n')
	if endRel < 0 {
		t.Fatal("unterminated MAC line")
	}
	cut := macStart + endRel + 1
	return string(file[:cut]), file[cut:]
}

// assertPayloadSealed parses a tampered file and proves the payload reader
// never returns a byte and always reports an error before authentication.
func assertPayloadSealed(t *testing.T, file []byte, identities ...Identity) {
	t.Helper()
	r := bufio.NewReader(bytes.NewReader(file))
	_, payload, parseErr := ParseHeader(r)
	if parseErr != nil {
		// Rejected at parse time: no payload reader at all, nothing leaked.
		return
	}

	// Direct attempt to read before any verification: must be sealed.
	probe := make([]byte, 64)
	n, err := payload.Read(probe)
	if n != 0 || !errors.Is(err, ErrHeaderNotAuthenticated) {
		t.Fatalf("pre-auth Read: n=%d err=%v, want n=0 ErrHeaderNotAuthenticated", n, err)
	}
	if !payload.Sealed() {
		t.Fatal("payload reader should report Sealed before verification")
	}

	// Drive the full decryption entry point: unwrap then MAC. It must fail and
	// the reader must still be sealed afterwards.
	decReader := bufio.NewReader(bytes.NewReader(file))
	_, pr, decErr := Decrypt(decReader, identities...)
	if decErr == nil {
		t.Fatal("Decrypt unexpectedly succeeded on tampered header")
	}
	if pr != nil {
		n, err = pr.Read(probe)
		if n != 0 || err == nil {
			t.Fatalf("post-failure Read: n=%d err=%v, want zero bytes and an error", n, err)
		}
	}
}

// TestDeleteRecipientLine removes the single scrypt stanza, so no identity can
// recover the file key and the payload stays inaccessible.
func TestDeleteRecipientLine(t *testing.T) {
	plaintext := bytes.Repeat([]byte("delete-line-"), 200)
	file := buildScryptFile(t, "correct horse battery staple", plaintext)
	header, payload := splitHeaderPayload(t, file)

	lines := strings.Split(header, "\n")
	var kept []string
	removed := false
	for _, line := range lines {
		if strings.HasPrefix(line, "scrypt ") {
			removed = true
			continue
		}
		kept = append(kept, line)
	}
	if !removed {
		t.Fatal("scrypt line was not present to remove")
	}
	tampered := []byte(strings.Join(kept, "\n") + string(payload))

	identity := &ScryptIdentity{Passphrase: []byte("correct horse battery staple")}
	assertPayloadSealed(t, tampered, identity)
}

// TestAddRecipientLine inserts an unknown-type stanza. The stanza is retained
// and covered by the MAC, so the added line invalidates authentication.
func TestAddRecipientLine(t *testing.T) {
	plaintext := []byte("a header with an extra line must not authenticate")
	file := buildScryptFile(t, "hunter2", plaintext)
	header, payload := splitHeaderPayload(t, file)

	extra := &Stanza{
		Type: "unknown-plugin",
		Args: [][]byte{[]byte("v1")},
		Body: []byte("attacker controlled extension data"),
	}
	insertAt := strings.Index(header, "scrypt ")
	tampered := []byte(header[:insertAt] + string(MarshalStanza(extra)) + header[insertAt:] + string(payload))

	identity := &ScryptIdentity{Passphrase: []byte("hunter2")}
	assertPayloadSealed(t, tampered, identity)
}

// TestModifyScryptParameters flips the scrypt work factor and rewrites the
// body line. Either unwrap fails or the MAC does; both keep payload sealed.
func TestModifyScryptParameters(t *testing.T) {
	plaintext := bytes.Repeat([]byte("param-tamper-"), 300)
	file := buildScryptFile(t, "swordfish", plaintext)
	header, payload := splitHeaderPayload(t, file)

	t.Run("work factor", func(t *testing.T) {
		lines := strings.Split(header, "\n")
		mutated := false
		for i, line := range lines {
			if strings.HasPrefix(line, "scrypt ") {
				fields := strings.Split(line, " ")
				// fields[2] is the decimal logN; bump it without changing length.
				fields[2] = "11"
				lines[i] = strings.Join(fields, " ")
				mutated = true
			}
		}
		if !mutated {
			t.Fatal("scrypt parameter line not found")
		}
		tampered := []byte(strings.Join(lines, "\n") + string(payload))
		assertPayloadSealed(t, tampered, &ScryptIdentity{Passphrase: []byte("swordfish")})
	})

	t.Run("salt byte", func(t *testing.T) {
		lines := strings.Split(header, "\n")
		mutated := false
		for i, line := range lines {
			if strings.HasPrefix(line, "scrypt ") {
				fields := strings.Split(line, " ")
				salt, err := base64.StdEncoding.DecodeString(fields[1])
				if err != nil {
					t.Fatalf("salt decode: %v", err)
				}
				salt[0] ^= 0xff
				fields[1] = base64.StdEncoding.EncodeToString(salt)
				lines[i] = strings.Join(fields, " ")
				mutated = true
			}
		}
		if !mutated {
			t.Fatal("scrypt salt line not found")
		}
		tampered := []byte(strings.Join(lines, "\n") + string(payload))
		assertPayloadSealed(t, tampered, &ScryptIdentity{Passphrase: []byte("swordfish")})
	})
}

// TestMixedRecipients covers multi-stanza headers: an X25519 header carrying an
// extra unknown stanza must fail MAC, and a scrypt stanza mixed with another
// recipient is rejected outright.
func TestMixedRecipients(t *testing.T) {
	t.Run("x25519 plus unknown line", func(t *testing.T) {
		identity, err := GenerateX25519Identity()
		if err != nil {
			t.Fatal(err)
		}
		var buf bytes.Buffer
		w, err := Encrypt(&buf, identity.Recipient())
		if err != nil {
			t.Fatal(err)
		}
		plaintext := bytes.Repeat([]byte("mixed-"), 500)
		if _, err := w.Write(plaintext); err != nil {
			t.Fatal(err)
		}
		if err := w.Close(); err != nil {
			t.Fatal(err)
		}

		header, payload := splitHeaderPayload(t, buf.Bytes())
		extra := &Stanza{Type: "guest-recipient", Body: []byte("not on the guest list")}
		insertAt := strings.Index(header, "X25519 ")
		tampered := []byte(header[:insertAt] + string(MarshalStanza(extra)) + header[insertAt:] + string(payload))
		assertPayloadSealed(t, tampered, identity)
	})

	t.Run("scrypt mixed with x25519 rejected", func(t *testing.T) {
		stanzas := []*Stanza{
			{Type: scryptType, Args: [][]byte{make([]byte, 16), []byte("10")}, Body: make([]byte, 32)},
			{Type: x25519Type, Args: [][]byte{make([]byte, 32)}, Body: make([]byte, 32)},
		}
		scryptID := &ScryptIdentity{Passphrase: []byte("anything")}
		if _, err := scryptID.Unwrap(stanzas); err == nil {
			t.Fatal("expected rejection of scrypt stanza mixed with another recipient")
		}
	})
}

// TestValidRoundTrip shows the happy path: after proper unwrap + MAC the
// payload reader unlocks and yields the exact plaintext.
func TestValidRoundTrip(t *testing.T) {
	plaintext := bytes.Repeat([]byte("round-trip-payload-"), 5000) // spans many chunks
	file := buildScryptFile(t, "passphrase", plaintext)

	r := bufio.NewReader(bytes.NewReader(file))
	header, payload, err := Decrypt(r, &ScryptIdentity{Passphrase: []byte("passphrase")})
	if err != nil {
		t.Fatalf("Decrypt: %v", err)
	}
	if !header.Authenticated() {
		t.Fatal("header should be authenticated")
	}
	got, err := io.ReadAll(payload)
	if err != nil {
		t.Fatalf("payload Read: %v", err)
	}
	if !bytes.Equal(got, plaintext) {
		t.Fatalf("plaintext mismatch: got %d bytes, want %d", len(got), len(plaintext))
	}
}

// TestPayloadTamperAfterMAC proves the STREAM layer independently rejects
// ciphertext modification even when the header itself is authentic.
func TestPayloadTamperAfterMAC(t *testing.T) {
	plaintext := []byte("authenticate header, then protect payload")
	file := buildScryptFile(t, "passphrase", plaintext)
	tamperedFile := bytes.Clone(file)
	_, payloadBytes := splitHeaderPayload(t, tamperedFile)
	payloadBytes[0] ^= 1

	r := bufio.NewReader(bytes.NewReader(tamperedFile))
	_, payload, err := Decrypt(r, &ScryptIdentity{Passphrase: []byte("passphrase")})
	if err != nil {
		t.Fatalf("Decrypt: %v", err)
	}
	if _, err := io.ReadAll(payload); err == nil {
		t.Fatal("expected STREAM authentication failure on tampered payload")
	}
}
