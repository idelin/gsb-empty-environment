package tls13zerortt

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"testing"
)

func testPSK() []byte { return bytes.Repeat([]byte{0x7E}, 32) }

// TestBinderRoundTrip verifies a self-built ClientHello passes binder
// verification for both resumption and external PSKs.
func TestBinderRoundTrip(t *testing.T) {
	psk := testPSK()
	identity := []byte("ticket-ref-0001")
	for _, mode := range []PSKMode{ResumptionPSK, ExternalPSK} {
		msg := buildClientHello(psk, mode, identity, 12345, 0x1301,
			[]byte("h2"), true, nil)
		parsed, err := ParseClientHello(msg)
		if err != nil {
			t.Fatalf("mode %v parse: %v", mode, err)
		}
		if err := parsed.VerifyBinderFromPSK(psk, mode, 0); err != nil {
			t.Fatalf("mode %v verify: %v", mode, err)
		}
		if !parsed.HasEarlyData() {
			t.Fatal("expected early_data extension to be detected")
		}
	}
}

// TestTruncationEquivalence proves RFC 8446 4.2.11.2 method 1 (truncated with
// rewritten lengths) and method 2 (full message with zeroed binders) hash to
// the same transcript, so the binder computed over either is identical.
func TestTruncationEquivalence(t *testing.T) {
	psk := testPSK()
	msg := buildClientHello(psk, ResumptionPSK, []byte("id"), 1, 0x1301,
		[]byte("h2"), true, nil)
	parsed, err := ParseClientHello(msg)
	if err != nil {
		t.Fatal(err)
	}

	truncated, err := parsed.TruncatedClientHello()
	if err != nil {
		t.Fatal(err)
	}
	zeroed := parsed.ZeroedClientHello()

	h1 := sha256.Sum256(truncated)
	h2 := sha256.Sum256(zeroed)
	if h1 != h2 {
		t.Fatalf("transcript hash differs between truncation methods:\n trunc=%x\n zero =%x", h1, h2)
	}

	// The truncated message must itself re-parse and remain length-consistent.
	if 4+int(uint32(truncated[1])<<16|uint32(truncated[2])<<8|uint32(truncated[3])) != len(truncated) {
		t.Fatal("truncated handshake length field inconsistent")
	}
}

// TestBinderIndependentOracle checks the package binder against a fully
// independent HMAC/HKDF recomputation written directly from the RFC formulas.
func TestBinderIndependentOracle(t *testing.T) {
	psk := testPSK()
	msg := buildClientHello(psk, ResumptionPSK, []byte("id"), 7, 0x1301,
		nil, false, nil)
	parsed, err := ParseClientHello(msg)
	if err != nil {
		t.Fatal(err)
	}
	truncated, err := parsed.TruncatedClientHello()
	if err != nil {
		t.Fatal(err)
	}
	want := independentBinder(psk, ResumptionPSK, truncated)
	got, err := parsed.RecomputeBinder(mustSchedule(t, psk, ResumptionPSK), 0)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("binder differs from independent oracle:\n got %x\nwant %x", got, want)
	}
}

func mustSchedule(t *testing.T, psk []byte, mode PSKMode) *KeySchedule {
	t.Helper()
	ks, err := NewKeySchedule(psk, mode)
	if err != nil {
		t.Fatal(err)
	}
	return ks
}

// TestTruncatedByteTamper flips each class of protected byte and asserts the
// binder is rejected: a binder-content flip, and tampering with the truncated
// region (random and the handshake length field).
func TestTruncatedByteTamper(t *testing.T) {
	psk := testPSK()
	base := buildClientHello(psk, ResumptionPSK, []byte("id"), 9, 0x1301,
		[]byte("h2"), true, nil)

	t.Run("binder_byte_flip", func(t *testing.T) {
		msg := bytes.Clone(base)
		parsed, err := ParseClientHello(msg)
		if err != nil {
			t.Fatal(err)
		}
		r := parsed.binderRanges[0]
		msg[r.start] ^= 0x01
		parsed, err = ParseClientHello(msg)
		if err != nil {
			t.Fatal(err)
		}
		err = parsed.VerifyBinderFromPSK(psk, ResumptionPSK, 0)
		if !errors.Is(err, ErrBinderMismatch) {
			t.Fatalf("want ErrBinderMismatch, got %v", err)
		}
	})

	t.Run("truncated_random_flip", func(t *testing.T) {
		msg := bytes.Clone(base)
		// Flip a byte inside the random (offset 4+2 = 6), well before binders.
		msg[6] ^= 0xFF
		parsed, err := ParseClientHello(msg)
		if err != nil {
			t.Fatal(err)
		}
		if err := parsed.VerifyBinderFromPSK(psk, ResumptionPSK, 0); !errors.Is(err, ErrBinderMismatch) {
			t.Fatalf("want ErrBinderMismatch, got %v", err)
		}
	})

	t.Run("truncated_length_rewrite_tamper", func(t *testing.T) {
		// Mutate the extensions length on the wire; binder verification must
		// fail because the truncated transcript changes (or parsing rejects).
		msg := bytes.Clone(base)
		parsed, err := ParseClientHello(msg)
		if err != nil {
			t.Fatal(err)
		}
		off := parsed.extensionsLengthOffset
		v := binary.BigEndian.Uint16(msg[off:])
		binary.BigEndian.PutUint16(msg[off:], v+1)
		if _, err := ParseClientHello(msg); err == nil {
			// If it still parses, the binder must fail.
			p2, _ := ParseClientHello(msg)
			if err := p2.VerifyBinderFromPSK(psk, ResumptionPSK, 0); !errors.Is(err, ErrBinderMismatch) {
				t.Fatalf("want mismatch, got %v", err)
			}
		}
	})

	t.Run("wrong_psk", func(t *testing.T) {
		parsed, err := ParseClientHello(bytes.Clone(base))
		if err != nil {
			t.Fatal(err)
		}
		wrong := bytes.Repeat([]byte{0x11}, 32)
		if err := parsed.VerifyBinderFromPSK(wrong, ResumptionPSK, 0); !errors.Is(err, ErrBinderMismatch) {
			t.Fatalf("want mismatch, got %v", err)
		}
	})

	t.Run("wrong_mode_label", func(t *testing.T) {
		parsed, err := ParseClientHello(bytes.Clone(base))
		if err != nil {
			t.Fatal(err)
		}
		// Same PSK, wrong binder label -> different finished key -> mismatch.
		if err := parsed.VerifyBinderFromPSK(psk, ExternalPSK, 0); !errors.Is(err, ErrBinderMismatch) {
			t.Fatalf("want mismatch, got %v", err)
		}
	})
}

// TestRecordLayerWrapper verifies a single record header is stripped.
func TestRecordLayerWrapper(t *testing.T) {
	psk := testPSK()
	msg := buildClientHello(psk, ResumptionPSK, []byte("id"), 3, 0x1301,
		nil, true, nil)
	record := []byte{contentTypeHandshake, 0x03, 0x03}
	lenBuf := make([]byte, 2)
	binary.BigEndian.PutUint16(lenBuf, uint16(len(msg)))
	record = append(record, lenBuf...)
	record = append(record, msg...)

	parsed, err := ParseClientHello(record)
	if err != nil {
		t.Fatalf("parse record: %v", err)
	}
	if err := parsed.VerifyBinderFromPSK(psk, ResumptionPSK, 0); err != nil {
		t.Fatalf("verify over record: %v", err)
	}

	// Fragment length mismatch must be rejected.
	bad := bytes.Clone(record)
	binary.BigEndian.PutUint16(bad[3:], uint16(len(msg))+1)
	if _, err := ParseClientHello(bad); !errors.Is(err, ErrMalformedClientHello) {
		t.Fatalf("want malformed, got %v", err)
	}
}
