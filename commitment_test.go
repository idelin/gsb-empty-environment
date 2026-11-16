package netsec

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"testing"
)

func testNonce(fill byte) []byte {
	nonce := make([]byte, NonceSize)
	for i := range nonce {
		nonce[i] = fill
	}
	return nonce
}

func mustCommitment(t *testing.T, roundID, participant string, nonce []byte) [CommitmentSize]byte {
	t.Helper()
	commitment, err := Commitment(roundID, participant, nonce)
	if err != nil {
		t.Fatalf("Commitment(%q, %q): %v", roundID, participant, err)
	}
	return commitment
}

func manualCommitment(roundID, participant string, nonce []byte) [CommitmentSize]byte {
	var prefix [4]byte
	var buf bytes.Buffer
	binary.BigEndian.PutUint32(prefix[:], uint32(len(roundID)))
	buf.Write(prefix[:])
	buf.WriteString(roundID)
	binary.BigEndian.PutUint32(prefix[:], uint32(len(participant)))
	buf.Write(prefix[:])
	buf.WriteString(participant)
	buf.Write(nonce)
	return sha256.Sum256(buf.Bytes())
}

func TestCommitmentEncoding(t *testing.T) {
	nonce := testNonce(0x5a)
	got := mustCommitment(t, "round-7", "alice", nonce)
	want := manualCommitment("round-7", "alice", nonce)
	if got != want {
		t.Fatalf("commitment mismatch:\n got %x\nwant %x", got, want)
	}

	// Length prefixes must keep ("ab", "c") distinct from ("a", "bc").
	a := mustCommitment(t, "ab", "c", nonce)
	b := mustCommitment(t, "a", "bc", nonce)
	if a == b {
		t.Fatal("length-prefixed encoding is ambiguous")
	}

	otherRound := mustCommitment(t, "round-8", "alice", nonce)
	if got == otherRound {
		t.Fatal("commitment is not bound to the round ID")
	}
}

func TestCommitmentValidation(t *testing.T) {
	if _, err := Commitment("", "alice", testNonce(1)); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("empty round error = %v, want ErrInvalidConfig", err)
	}
	if _, err := Commitment("r", "", testNonce(1)); !errors.Is(err, ErrInvalidParticipant) {
		t.Fatalf("empty participant error = %v, want ErrInvalidParticipant", err)
	}
	if _, err := Commitment("r", "alice", make([]byte, NonceSize-1)); !errors.Is(err, ErrInvalidNonce) {
		t.Fatalf("short nonce error = %v, want ErrInvalidNonce", err)
	}
	if _, err := Commitment("r", "alice", make([]byte, NonceSize+1)); !errors.Is(err, ErrInvalidNonce) {
		t.Fatalf("long nonce error = %v, want ErrInvalidNonce", err)
	}
}

func TestNewNonce(t *testing.T) {
	nonce, err := NewNonce()
	if err != nil {
		t.Fatalf("NewNonce: %v", err)
	}
	if len(nonce) != NonceSize {
		t.Fatalf("nonce length = %d, want %d", len(nonce), NonceSize)
	}
}
