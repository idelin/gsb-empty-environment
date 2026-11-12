package lsadb

import "testing"

func TestChecksumRoundTrip(t *testing.T) {
	t.Parallel()

	l := buildLSA(t, neighborID, 1, testLSID, InitSequenceNumber, 10, []byte{0xde, 0xad, 0xbe, 0xef})
	if l.Checksum == 0 {
		t.Fatal("expected non-zero Fletcher checksum")
	}
	if !verifyChecksum(l.raw) {
		t.Fatal("freshly marshaled LSA fails its own checksum")
	}

	// Changing any checksum-covered byte must invalidate the checksum.
	corrupt := make([]byte, len(l.raw))
	copy(corrupt, l.raw)
	corrupt[HeaderLength] ^= 0xff
	if verifyChecksum(corrupt) {
		t.Fatal("corrupted body still passed checksum verification")
	}

	// The LS age field is excluded, so changing it must not break the
	// checksum (RFC 2328 section 12.1.7).
	aged := make([]byte, len(l.raw))
	copy(aged, l.raw)
	aged[0] ^= 0x12
	aged[1] ^= 0x34
	if !verifyChecksum(aged) {
		t.Fatal("LS age change unexpectedly invalidated the checksum")
	}
}

func TestParseRejectsBadChecksum(t *testing.T) {
	t.Parallel()

	l := buildLSA(t, neighborID, 1, testLSID, InitSequenceNumber, 10, nil)
	corrupt := make([]byte, len(l.raw))
	copy(corrupt, l.raw)
	corrupt[HeaderLength+1] ^= 0x01

	if _, err := ParseLSA(corrupt); err == nil {
		t.Fatal("ParseLSA accepted a corrupted LSA")
	} else if !errorIs(err, ErrChecksum) {
		t.Fatalf("ParseLSA error = %v, want ErrChecksum", err)
	}
}

func TestParseRejectsReservedSequence(t *testing.T) {
	t.Parallel()

	l := buildLSA(t, neighborID, 1, testLSID, reservedSequenceNumber, 10, nil)
	if _, err := ParseLSA(l.raw); err == nil {
		t.Fatal("ParseLSA accepted reserved sequence number")
	} else if !errorIs(err, ErrReservedSequence) {
		t.Fatalf("ParseLSA error = %v, want ErrReservedSequence", err)
	}
}

func TestParseLSA(t *testing.T) {
	t.Parallel()

	original := buildLSA(t, neighborID, 2, testLSID, 42, 30, []byte{1, 2, 3})
	parsed, err := ParseLSA(original.raw)
	if err != nil {
		t.Fatalf("ParseLSA: %v", err)
	}
	if parsed.LSType != 2 || parsed.SeqNum != 42 || parsed.LSAGE != 30 ||
		parsed.AdvertisingRouter != neighborID || parsed.LinkStateID != testLSID {
		t.Fatalf("parsed header mismatch: %+v", parsed)
	}
	if len(parsed.Body) != 3 || parsed.Body[0] != 1 {
		t.Fatalf("parsed body mismatch: %v", parsed.Body)
	}
	if int(parsed.Length) != HeaderLength+3 {
		t.Fatalf("parsed length = %d", parsed.Length)
	}
}
