package netsec

import (
	"bytes"
	"compress/zlib"
	"encoding/binary"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func compress(t *testing.T, data []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	w := zlib.NewWriter(&buf)
	if _, err := w.Write(data); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func makeFrame(declared uint64, compressed []byte) []byte {
	frame := make([]byte, LengthPrefixSize+len(compressed))
	binary.BigEndian.PutUint64(frame[:LengthPrefixSize], declared)
	copy(frame[LengthPrefixSize:], compressed)
	return frame
}

func TestValidSmallBitmapDecodesAndIsReadable(t *testing.T) {
	bitmap := []byte{0b00001011, 0b00000000}
	frame := makeFrame(uint64(len(bitmap)), compress(t, bitmap))

	got, err := DecodeStatusList(frame)
	if err != nil {
		t.Fatalf("expected successful decode, got %v", err)
	}
	if !bytes.Equal(got, bitmap) {
		t.Fatalf("bitmap mismatch: got %v want %v", got, bitmap)
	}

	// Bits 0,1,3 set in byte 0.
	for _, tc := range []struct {
		index int
		want  int
	}{
		{0, 1}, {1, 1}, {2, 0}, {3, 1}, {4, 0}, {15, 0},
	} {
		bit, err := BitAt(got, tc.index)
		if err != nil {
			t.Fatalf("BitAt(%d) error: %v", tc.index, err)
		}
		if bit != tc.want {
			t.Fatalf("BitAt(%d)=%d want %d", tc.index, bit, tc.want)
		}
	}
}

func TestOversizedDeclarationRejectedBeforeInflate(t *testing.T) {
	// A tiny payload that only inflates to one byte, but the declared length
	// claims a 1 TiB expansion.
	frame := makeFrame(1<<40, compress(t, []byte{0x00}))

	bitmap, err := DecodeStatusList(frame)
	if !errors.Is(err, ErrDeclaredTooLarge) {
		t.Fatalf("expected ErrDeclaredTooLarge, got %v", err)
	}
	if bitmap != nil {
		t.Fatalf("expected nil bitmap on rejection, got %d bytes", len(bitmap))
	}
}

func TestDeclarationJustOverLimitRejected(t *testing.T) {
	frame := makeFrame(MaxDeclaredSize+1, compress(t, []byte{0x00}))
	if _, err := DecodeStatusList(frame); !errors.Is(err, ErrDeclaredTooLarge) {
		t.Fatalf("expected ErrDeclaredTooLarge for size %d, got %v", MaxDeclaredSize+1, err)
	}
}

func TestDeclarationAtLimitIsNotRejectedForSize(t *testing.T) {
	// The size check alone must not reject an in-limit declaration; here the
	// length mismatch surfaces instead because the payload is only 1 byte.
	frame := makeFrame(MaxDeclaredSize, compress(t, []byte{0x00}))
	_, err := DecodeStatusList(frame)
	if errors.Is(err, ErrDeclaredTooLarge) {
		t.Fatalf("in-limit declaration must not be rejected as too large")
	}
	if !errors.Is(err, ErrLengthMismatch) {
		t.Fatalf("expected ErrLengthMismatch, got %v", err)
	}
}

func TestLengthMismatchRejected(t *testing.T) {
	// Declare 4 bytes but the stream only yields 1.
	frame := makeFrame(4, compress(t, []byte{0x00}))
	if _, err := DecodeStatusList(frame); !errors.Is(err, ErrLengthMismatch) {
		t.Fatalf("expected ErrLengthMismatch, got %v", err)
	}

	// Declare 1 byte but the stream yields 4.
	frame = makeFrame(1, compress(t, []byte{0x01, 0x02, 0x03, 0x04}))
	if _, err := DecodeStatusList(frame); !errors.Is(err, ErrLengthMismatch) {
		t.Fatalf("expected ErrLengthMismatch for over-long stream, got %v", err)
	}
}

func TestShortFrameRejected(t *testing.T) {
	for _, n := range []int{0, 1, 7} {
		if _, err := DecodeStatusList(make([]byte, n)); !errors.Is(err, ErrShortFrame) {
			t.Fatalf("len %d: expected ErrShortFrame, got %v", n, err)
		}
	}
}

func TestMalformedZlibRejected(t *testing.T) {
	frame := makeFrame(2, []byte{0x00, 0x00, 0x00, 0x00})
	if _, err := DecodeStatusList(frame); !errors.Is(err, ErrMalformed) {
		t.Fatalf("expected ErrMalformed, got %v", err)
	}
}

func TestEmptyBitmapRoundTrip(t *testing.T) {
	frame := makeFrame(0, compress(t, nil))
	got, err := DecodeStatusList(frame)
	if err != nil {
		t.Fatalf("empty bitmap decode failed: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("expected empty bitmap, got %d bytes", len(got))
	}
}

func TestVectorsOnDisk(t *testing.T) {
	valid, err := os.ReadFile(filepath.Join("testdata", "valid_small.bin"))
	if err != nil {
		t.Skipf("fixtures not generated: %v", err)
	}
	malicious, err := os.ReadFile(filepath.Join("testdata", "malicious_oversize_declaration.bin"))
	if err != nil {
		t.Skipf("fixtures not generated: %v", err)
	}

	if _, err := DecodeStatusList(valid); err != nil {
		t.Fatalf("valid fixture should decode: %v", err)
	}
	if _, err := DecodeStatusList(malicious); !errors.Is(err, ErrDeclaredTooLarge) {
		t.Fatalf("malicious fixture should be rejected for size, got %v", err)
	}
}
