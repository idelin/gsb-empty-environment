package hostkeys

import (
	"errors"
	"testing"
)

func TestParseNotification(t *testing.T) {
	blobA := []byte("ssh-ed25519-blob-a")
	blobB := []byte("ssh-ed25519-blob-b")
	payload := marshalStringList([][]byte{blobA, blobB})

	got, err := parseNotification(payload)
	if err != nil {
		t.Fatalf("parseNotification: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("want 2 blobs, got %d", len(got))
	}
	if string(got[0]) != string(blobA) || string(got[1]) != string(blobB) {
		t.Fatalf("round-tripped blobs mismatch: %q %q", got[0], got[1])
	}
}

func TestParseNotificationRejectsEmptyAndTruncated(t *testing.T) {
	if _, err := parseNotification(nil); !errors.Is(err, ErrMalformedNotification) {
		t.Fatalf("empty payload: want ErrMalformedNotification, got %v", err)
	}

	// Declared length exceeds the remaining bytes.
	truncated := []byte{0, 0, 0, 10, 'a', 'b'}
	if _, err := parseNotification(truncated); !errors.Is(err, ErrMalformedNotification) {
		t.Fatalf("truncated payload: want ErrMalformedNotification, got %v", err)
	}

	// A zero-length string is never a valid key blob.
	zeroLength := []byte{0, 0, 0, 0}
	if _, err := parseNotification(zeroLength); !errors.Is(err, ErrMalformedNotification) {
		t.Fatalf("zero-length blob: want ErrMalformedNotification, got %v", err)
	}
}
