package luks2

import (
	"encoding/binary"
	"errors"
	"testing"
)

func TestChecksumVerification(t *testing.T) {
	raw := buildRawHeader(t, validJSON(), testHeaderSize)

	t.Run("valid checksum accepted", func(t *testing.T) {
		if err := VerifyChecksum(raw); err != nil {
			t.Fatalf("VerifyChecksum returned unexpected error: %v", err)
		}
	})

	t.Run("tampered json rejected", func(t *testing.T) {
		tampered := append([]byte(nil), raw...)
		tampered[jsonAreaOffset+10] ^= 0xFF
		if err := VerifyChecksum(tampered); !errors.Is(err, ErrBadHeader) {
			t.Fatalf("error = %v, want ErrBadHeader", err)
		}
	})

	t.Run("tampered binary field rejected", func(t *testing.T) {
		tampered := append([]byte(nil), raw...)
		tampered[offSeqID] ^= 0x01
		if err := VerifyChecksum(tampered); !errors.Is(err, ErrBadHeader) {
			t.Fatalf("error = %v, want ErrBadHeader", err)
		}
	})

	t.Run("forged checksum cannot hide tampering", func(t *testing.T) {
		tampered := append([]byte(nil), raw...)
		tampered[jsonAreaOffset+10] ^= 0xFF
		// Recompute over the tampered region but leave the checksum field in
		// place instead of zeroing it, i.e. the naive attacker mistake.
		sum := naiveChecksum(tampered)
		copy(tampered[offChecksum:offChecksum+ChecksumSize], sum[:])
		if err := VerifyChecksum(tampered); !errors.Is(err, ErrBadHeader) {
			t.Fatalf("forged checksum should not verify, got %v", err)
		}
	})

	t.Run("truncated header rejected", func(t *testing.T) {
		if err := VerifyChecksum(raw[:1024]); !errors.Is(err, ErrBadHeader) {
			t.Fatalf("error = %v, want ErrBadHeader", err)
		}
	})

	t.Run("bad magic rejected", func(t *testing.T) {
		bad := append([]byte(nil), raw...)
		bad[0] = 'X'
		if err := VerifyChecksum(bad); !errors.Is(err, ErrBadHeader) {
			t.Fatalf("error = %v, want ErrBadHeader", err)
		}
	})

	t.Run("unsupported version rejected", func(t *testing.T) {
		bad := append([]byte(nil), raw...)
		binary.LittleEndian.PutUint16(bad[offVersion:], 1)
		if err := VerifyChecksum(bad); !errors.Is(err, ErrBadHeader) {
			t.Fatalf("error = %v, want ErrBadHeader", err)
		}
	})
}

func TestParseHeaderRejectsTamperedAttacks(t *testing.T) {
	limits := KDFLimits{MaxTime: 10, MaxMemory: 4194304, MaxCPUs: 16}

	t.Run("overlap attack rejected end to end", func(t *testing.T) {
		jsonText := validJSON()
		overlap := replaceAreaOffset(jsonText, `"offset": "163840"`, `"offset": "100000"`)
		raw := buildRawHeader(t, overlap, testHeaderSize)
		if _, _, err := ParseHeader(raw, limits); !errors.Is(err, ErrAreaOverlap) {
			t.Fatalf("error = %v, want ErrAreaOverlap", err)
		}
	})

	t.Run("dangling digest binding rejected end to end", func(t *testing.T) {
		jsonText := replaceAreaOffset(validJSON(), `"keyslots": ["0", "1"]`, `"keyslots": ["0", "9"]`)
		raw := buildRawHeader(t, jsonText, testHeaderSize)
		if _, _, err := ParseHeader(raw, limits); !errors.Is(err, ErrDanglingReference) {
			t.Fatalf("error = %v, want ErrDanglingReference", err)
		}
	})

	t.Run("oversized KDF rejected end to end", func(t *testing.T) {
		raw := buildRawHeader(t, validJSON(), testHeaderSize)
		if _, _, err := ParseHeader(raw, KDFLimits{MaxTime: 2}); !errors.Is(err, ErrKDFLimit) {
			t.Fatalf("error = %v, want ErrKDFLimit", err)
		}
	})

	t.Run("tampered header fails checksum before JSON parsing", func(t *testing.T) {
		raw := buildRawHeader(t, validJSON(), testHeaderSize)
		raw[jsonAreaOffset+10] ^= 0xFF
		if _, _, err := ParseHeader(raw, limits); !errors.Is(err, ErrBadHeader) {
			t.Fatalf("error = %v, want ErrBadHeader", err)
		}
	})
}

func TestJSONAreaExtraction(t *testing.T) {
	raw := buildRawHeader(t, validJSON(), testHeaderSize)
	area, err := JSONArea(raw, testHeaderSize)
	if err != nil {
		t.Fatalf("JSONArea returned unexpected error: %v", err)
	}
	header, err := ParseMetadata(area)
	if err != nil {
		t.Fatalf("parsing extracted JSON area failed: %v", err)
	}
	if _, ok := header.Keyslots["0"]; !ok {
		t.Fatalf("keyslot 0 missing after JSON area extraction")
	}
}
