package netsec

import (
	"bytes"
	"errors"
	"testing"
)

func TestShortPartialIVIsRejected(t *testing.T) {
	spec := AESGCM96

	// A partial IV submitted one byte shorter than the algorithm requires.
	short := make([]byte, spec.PartialIVLen-1) // 3 bytes instead of 4

	if err := spec.ValidatePartialIV(short); !errors.Is(err, ErrBadPartialIVLen) {
		t.Fatalf("ValidatePartialIV(short) error = %v, want ErrBadPartialIVLen", err)
	}

	prefix := make([]byte, spec.PrefixLen)
	if _, err := spec.BuildIV(prefix, short); !errors.Is(err, ErrBadPartialIVLen) {
		t.Fatalf("BuildIV(prefix, short) error = %v, want ErrBadPartialIVLen", err)
	}
}

func TestTooLongPartialIVIsRejected(t *testing.T) {
	spec := AESGCM96
	long := make([]byte, spec.PartialIVLen+1)
	if err := spec.ValidatePartialIV(long); !errors.Is(err, ErrBadPartialIVLen) {
		t.Fatalf("ValidatePartialIV(long) error = %v, want ErrBadPartialIVLen", err)
	}
}

func TestLeftPaddingWouldCollide(t *testing.T) {
	spec := AESGCM96
	prefix := bytes.Repeat([]byte{0xAB}, spec.PrefixLen)

	// Two genuinely different partial values that a naive left-pad would merge.
	// The 3-byte submission is one byte short; after left zero-padding it becomes
	// identical to a legitimate 4-byte value whose leading byte is zero.
	short := []byte{0x01, 0x02, 0x03}
	legit := []byte{0x00, 0x01, 0x02, 0x03}

	padded := append([]byte{0x00}, short...)
	if !bytes.Equal(padded, legit) {
		t.Fatalf("test setup invalid: padded short %x != legit %x", padded, legit)
	}

	// The vulnerable behavior is exactly what we refuse: the short submission
	// must fail rather than be padded into the same IV as the legitimate message.
	if _, err := spec.BuildIV(prefix, short); !errors.Is(err, ErrBadPartialIVLen) {
		t.Fatalf("BuildIV accepted a short partial IV: %v", err)
	}

	goodIV, err := spec.BuildIV(prefix, legit)
	if err != nil {
		t.Fatalf("BuildIV with a valid partial IV failed: %v", err)
	}
	if len(goodIV) != spec.IVLen() {
		t.Fatalf("IV length = %d, want %d", len(goodIV), spec.IVLen())
	}
}

func TestValidPartialIVParticipatesInConstruction(t *testing.T) {
	spec := AESGCM96
	prefix := bytes.Repeat([]byte{0xCD}, spec.PrefixLen)
	partial := []byte{0x10, 0x20, 0x30, 0x40}

	if err := spec.ValidatePartialIV(partial); err != nil {
		t.Fatalf("valid partial IV rejected: %v", err)
	}

	iv, err := spec.BuildIV(prefix, partial)
	if err != nil {
		t.Fatalf("BuildIV failed: %v", err)
	}

	want := append(append([]byte{}, prefix...), partial...)
	if !bytes.Equal(iv, want) {
		t.Fatalf("IV = %x, want %x (no padding allowed)", iv, want)
	}
}

type stubReader struct {
	data []byte
}

func (r stubReader) Read(p []byte) (int, error) {
	n := copy(p, r.data)
	return n, nil
}

func TestGeneratePartialIVHasRequiredLength(t *testing.T) {
	spec := AESGCM96
	rng := stubReader{data: bytes.Repeat([]byte{0x7A}, spec.PartialIVLen)}

	partial, err := spec.GeneratePartialIV(rng)
	if err != nil {
		t.Fatalf("GeneratePartialIV failed: %v", err)
	}
	if err := spec.ValidatePartialIV(partial); err != nil {
		t.Fatalf("generated partial IV fails validation: %v", err)
	}

	prefix := make([]byte, spec.PrefixLen)
	iv, err := spec.GenerateIV(rng, prefix)
	if err != nil {
		t.Fatalf("GenerateIV failed: %v", err)
	}
	if len(iv) != spec.IVLen() {
		t.Fatalf("generated IV length = %d, want %d", len(iv), spec.IVLen())
	}
}

func TestBadPrefixIsRejected(t *testing.T) {
	spec := AESGCM96
	partial := make([]byte, spec.PartialIVLen)

	if _, err := spec.BuildIV([]byte{0x00}, partial); !errors.Is(err, ErrBadPrefixLen) {
		t.Fatalf("BuildIV with short prefix error = %v, want ErrBadPrefixLen", err)
	}
	if _, err := spec.GenerateIV(nil, []byte{0x00}); !errors.Is(err, ErrBadPrefixLen) {
		t.Fatalf("GenerateIV with short prefix error = %v, want ErrBadPrefixLen", err)
	}
}
