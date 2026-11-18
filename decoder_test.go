package netsec

import (
	"errors"
	"testing"
)

func TestParseCBORPayloadRejectsBadInput(t *testing.T) {
	valid := cborMap(defaultCBOR(baseRecord()))

	cases := []struct {
		name string
		data []byte
	}{
		{"empty", nil},
		{"trailing bytes", append(append([]byte(nil), valid...), 0x00)},
		{"indefinite map", []byte{0xbf, 0xff}},
		{"integer instead of map", []byte{0x01}},
		{"float value not supported", []byte{0xfa, 0x00, 0x00, 0x00, 0x00}},
		{"tag not supported", []byte{0xc0, 0x01}},
		{"truncated head", []byte{0x58}},
		{"truncated string payload", []byte{0x42, 0x01}},
		{"truncated uint16", []byte{0x19, 0x01}},
		{"reserved additional info", []byte{0x1e}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := parseCBORPayload(tc.data); !errors.Is(err, ErrMalformedRecord) {
				t.Fatalf("expected ErrMalformedRecord, got %v", err)
			}
		})
	}
}

func TestParseCBORPayloadRejectsWrongKeys(t *testing.T) {
	t.Run("extra key", func(t *testing.T) {
		fields := defaultCBOR(baseRecord())
		fields["Extra"] = uint64(1)
		if _, err := parseCBORPayload(cborMap(fields)); !errors.Is(err, ErrMalformedRecord) {
			t.Fatalf("expected ErrMalformedRecord, got %v", err)
		}
	})

	t.Run("missing key", func(t *testing.T) {
		fields := map[string]any{
			"Value":        []byte("v"),
			"Validity":     []byte("d"),
			"ValidityType": uint64(0),
			"Sequence":     uint64(1),
		}
		if _, err := parseCBORPayload(cborMap(fields)); !errors.Is(err, ErrMalformedRecord) {
			t.Fatalf("expected ErrMalformedRecord, got %v", err)
		}
	})

	t.Run("wrong value type rejected by full validation", func(t *testing.T) {
		pub, priv := newKeyPair(t)
		v := fixedValidator((&captureVerifier{pub: pub}).verify)
		fields := defaultCBOR(baseRecord())
		fields["Sequence"] = "10"

		r := baseRecord()
		r.cborFields = fields
		if _, err := v.Validate(buildRecord(t, r, priv)); !errors.Is(err, ErrFieldMismatch) {
			t.Fatalf("expected ErrFieldMismatch, got %v", err)
		}
	})
}

func TestParsePBEntryRejectsBadInput(t *testing.T) {
	cases := []struct {
		name string
		data []byte
	}{
		{"empty", nil},
		{"truncated varint", []byte{0x08, 0xff}},
		{"varint overflow", []byte{0x08, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0x7f}},
		{"field number zero", []byte{0x00}},
		{"truncated length", []byte{0x0a, 0x05, 0x01}},
		{"truncated fixed64", []byte{0x09, 0x00}},
		{"unsupported wire group", []byte{0x0b}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := parsePBEntry(tc.data); !errors.Is(err, ErrMalformedRecord) {
				t.Fatalf("expected ErrMalformedRecord, got %v", err)
			}
		})
	}
}

func TestParsePBEntryRejectsDuplicateFields(t *testing.T) {
	data := append(pbVarint(pbFieldSequence, 1), pbVarint(pbFieldSequence, 2)...)
	if _, err := parsePBEntry(data); !errors.Is(err, ErrMalformedRecord) {
		t.Fatalf("expected ErrMalformedRecord, got %v", err)
	}
}
