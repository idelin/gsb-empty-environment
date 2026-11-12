package netsec

import (
	"bufio"
	"bytes"
	"io"
	"testing"
)

func roundTrip(t *testing.T, plaintext []byte) {
	t.Helper()
	file := buildScryptFile(t, "edge-pass", plaintext)
	_, payload, err := Decrypt(bufio.NewReader(bytes.NewReader(file)), &ScryptIdentity{Passphrase: []byte("edge-pass")})
	if err != nil {
		t.Fatalf("Decrypt: %v", err)
	}
	got, err := io.ReadAll(payload)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if !bytes.Equal(got, plaintext) {
		t.Fatalf("mismatch: got %d bytes want %d", len(got), len(plaintext))
	}
}

func TestStreamEdges(t *testing.T) {
	t.Run("empty", func(t *testing.T) { roundTrip(t, nil) })
	t.Run("one byte", func(t *testing.T) { roundTrip(t, []byte{7}) })
	t.Run("exact chunk", func(t *testing.T) { roundTrip(t, make([]byte, chunkSize)) })
	t.Run("chunk plus one", func(t *testing.T) { roundTrip(t, make([]byte, chunkSize+1)) })
	t.Run("many chunks", func(t *testing.T) { roundTrip(t, make([]byte, chunkSize*3+123)) })
}

// TestTruncatedPayloadRejected confirms a missing final chunk fails closed.
func TestTruncatedPayloadRejected(t *testing.T) {
	file := buildScryptFile(t, "edge-pass", make([]byte, chunkSize+500))
	header, payload := splitHeaderPayload(t, file)
	truncated := append([]byte(header), payload[:40]...)

	_, pr, err := Decrypt(bufio.NewReader(bytes.NewReader(truncated)), &ScryptIdentity{Passphrase: []byte("edge-pass")})
	if err != nil {
		t.Fatalf("Decrypt header: %v", err)
	}
	if _, err := io.ReadAll(pr); err == nil {
		t.Fatal("expected error reading truncated payload")
	}
}
