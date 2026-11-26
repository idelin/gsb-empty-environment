package tls13sm

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"testing"
)

// TestHRRTranscriptFolding verifies the RFC 8446 4.4.1 synthetic
// message_hash transcript replacement. After an HRR the transcript must begin
// with a type-254 handshake message whose body is Hash(ClientHello1), and the
// HRR itself must follow it.
func TestHRRTranscriptFolding(t *testing.T) {
	tr := newTranscript(sha256.New)
	clientHello1 := marshalHandshake(HandshakeClientHello, []byte("client-hello-1-body"))
	hrr := marshalHandshake(HandshakeServerHello, []byte("hello-retry-request"))

	tr.add(clientHello1)
	originalCH1Hash := append([]byte(nil), tr.hashOf()...)

	if err := tr.foldHelloRetryRequest(clientHello1, hrr); err != nil {
		t.Fatal(err)
	}
	if !tr.HRRFolded() {
		t.Fatal("transcript should report HRR folding")
	}

	messages := tr.Messages()
	if len(messages) != 2 {
		t.Fatalf("folded transcript messages=%d, want 2", len(messages))
	}

	// First message must be the synthetic message_hash(254).
	hdr, body, err := parseHandshakeHeader(messages[0])
	if err != nil {
		t.Fatal(err)
	}
	if hdr.Type != HandshakeMessageHash {
		t.Fatalf("synthetic type=%d, want message_hash(254)", hdr.Type)
	}
	if !bytes.Equal(body, originalCH1Hash) {
		t.Fatal("synthetic body is not Hash(ClientHello1)")
	}

	// Second message must be the HRR.
	hdr2, _, err := parseHandshakeHeader(messages[1])
	if err != nil {
		t.Fatal(err)
	}
	if hdr2.Type != HandshakeServerHello {
		t.Fatalf("second message type=%d, want ServerHello", hdr2.Type)
	}

	// Running hash must equal Hash(synthetic || HRR).
	want := sha256.New()
	want.Write(messages[0])
	want.Write(messages[1])
	if !bytes.Equal(tr.hashOf(), want.Sum(nil)) {
		t.Fatal("running hash does not cover synthetic message_hash + HRR")
	}
}

// TestSecondHRRRejected proves folding twice is forbidden, the structural
// guard backing the state-machine "no second HRR" rule.
func TestSecondHRRRejected(t *testing.T) {
	tr := newTranscript(sha256.New)
	ch1 := marshalHandshake(HandshakeClientHello, []byte("ch1"))
	hrr := marshalHandshake(HandshakeServerHello, []byte("hrr"))
	if err := tr.foldHelloRetryRequest(ch1, hrr); err != nil {
		t.Fatal(err)
	}
	err := tr.foldHelloRetryRequest(ch1, hrr)
	if !errors.Is(err, ErrHRRRejected) {
		t.Fatalf("second fold err=%v, want ErrHRRRejected", err)
	}
}

// TestClientRejectsSecondHRR drives a real client through one HRR and proves
// a second HRR response is refused.
func TestClientRejectsSecondHRR(t *testing.T) {
	// First connection offers only P-256 so the server requests P-384.
	client, err := NewClientStateMachine(ClientConfig{
		Suite:  SuiteAES128GCM,
		Groups: []uint16{GroupP256, GroupP384},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.Start(); err != nil {
		t.Fatal(err)
	}

	firstHRR := buildServerHelloWithRandom(t, bytes.Repeat([]byte{0x11}, 32), true)
	// Force the HRR group to P-384, which the client did not provide.
	firstHRR = forceHRRGroup(t, firstHRR, GroupP384)
	event, err := client.HandleRecord(Record{
		Epoch:       EpochPlaintext,
		ContentType: ContentTypeHandshake,
		Version:     LegacyVersionTLS12,
		Payload:     firstHRR,
	})
	if err != nil {
		t.Fatalf("first HRR should be accepted: %v", err)
	}
	if len(event.Records) != 1 || !client.hrrSeen {
		t.Fatal("client did not emit retry ClientHello")
	}

	// A second HRR, even for another group, must be rejected.
	secondHRR := buildServerHelloWithRandom(t, bytes.Repeat([]byte{0x22}, 32), true)
	secondHRR = forceHRRGroup(t, secondHRR, GroupX25519)
	_, err = client.HandleRecord(Record{
		Epoch:       EpochPlaintext,
		ContentType: ContentTypeHandshake,
		Version:     LegacyVersionTLS12,
		Payload:     secondHRR,
	})
	if !errors.Is(err, ErrHRRRejected) {
		t.Fatalf("second HRR err=%v, want ErrHRRRejected", err)
	}
}

// forceHRRGroup rewrites the two-byte key_share group of an HRR message.
func forceHRRGroup(t *testing.T, hrr []byte, group uint16) []byte {
	t.Helper()
	hdr, body, err := parseHandshakeHeader(hrr)
	if err != nil {
		t.Fatal(err)
	}
	sh, err := parseServerHello(body)
	if err != nil {
		t.Fatal(err)
	}
	var exts []extension
	for _, ext := range sh.Extensions {
		if ext.Type == ExtKeyShare {
			ext.Data = []byte{byte(group >> 8), byte(group & 0xff)}
		}
		exts = append(exts, ext)
	}
	newBody := marshalServerHello(sh.Random, sh.SessionID, sh.CipherSuite, exts)
	return marshalHandshake(hdr.Type, newBody)
}

// TestHRRRejectsAlreadyOfferedGroup ensures an HRR cannot request a group the
// client already sent a key share for, which would make the retry
// nonsensical and indicates tampering.
func TestHRRRejectsAlreadyOfferedGroup(t *testing.T) {
	client, err := NewClientStateMachine(ClientConfig{
		Suite:  SuiteAES128GCM,
		Groups: []uint16{GroupP256},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.Start(); err != nil {
		t.Fatal(err)
	}
	hrr := buildServerHelloWithRandom(t, bytes.Repeat([]byte{0x33}, 32), true)
	hrr = forceHRRGroup(t, hrr, GroupP256) // already offered
	_, err = client.HandleRecord(Record{
		Epoch:       EpochPlaintext,
		ContentType: ContentTypeHandshake,
		Version:     LegacyVersionTLS12,
		Payload:     hrr,
	})
	if !errors.Is(err, ErrHRRRejected) {
		t.Fatalf("already-offered group err=%v, want ErrHRRRejected", err)
	}
}
