package netsec

import (
	"crypto/ed25519"
	"errors"
	"testing"
)

// encodeTestCBORTextValidity mirrors encodeTestCBOR but writes Validity as a
// CBOR text string (major 3) instead of a byte string, matching the
// on-the-wire form described by the IPNS specification.
func encodeTestCBORTextValidity(spec recordSpec) []byte {
	var b []byte
	b = append(b, 0xa5)

	b = appendCBORText(b, cborKeyValue)
	b = appendCBORBytes(b, spec.value)

	b = appendCBORText(b, cborKeyValidity)
	b = appendCBORText(b, spec.validity)

	b = appendCBORText(b, cborKeyValidityType)
	b = appendCBORUint(b, spec.validityType)

	b = appendCBORText(b, cborKeySequence)
	b = appendCBORUint(b, spec.sequence)

	b = appendCBORText(b, cborKeyTTL)
	b = appendCBORUint(b, spec.ttl)
	return b
}

func TestValidateAcceptsTextStringValidity(t *testing.T) {
	pub, priv := newTestSigner(t)
	v := newTestValidator(pub)

	spec := defaultSpec()
	data := encodeTestCBORTextValidity(spec)
	sig := ed25519.Sign(priv, append(append([]byte(nil), V2SignaturePrefix...), data...))

	var raw []byte
	raw = appendProtoBytes(raw, fieldValue, spec.value)
	raw = appendProtoVarint(raw, fieldValidityType, spec.validityType)
	raw = appendProtoBytes(raw, fieldValidity, []byte(spec.validity))
	raw = appendProtoVarint(raw, fieldSequence, spec.sequence)
	raw = appendProtoVarint(raw, fieldTTL, spec.ttl)
	raw = appendProtoBytes(raw, fieldV2Signature, sig)
	raw = appendProtoBytes(raw, fieldData, data)

	if _, err := v.Validate(testKey, raw); err != nil {
		t.Fatalf("text-string Validity must be accepted: %v", err)
	}
}

func TestCBORStrictness(t *testing.T) {
	t.Run("duplicate key rejected", func(t *testing.T) {
		// map(2): "Sequence" -> 1, "Sequence" -> 2
		data := []byte{
			0xa2,
			0x68, 'S', 'e', 'q', 'u', 'e', 'n', 'c', 'e', 0x01,
			0x68, 'S', 'e', 'q', 'u', 'e', 'n', 'c', 'e', 0x02,
		}
		if _, err := parseCBOR(data); !errors.Is(err, ErrMalformedRecord) {
			t.Fatalf("err = %v, want ErrMalformedRecord", err)
		}
	})

	t.Run("indefinite length map rejected", func(t *testing.T) {
		// indefinite map containing one tstr/uint pair, break stop code.
		data := []byte{
			0xbf,
			0x63, 'T', 'T', 'L', 0x01,
			0xff,
		}
		if _, err := parseCBOR(data); !errors.Is(err, ErrMalformedRecord) {
			t.Fatalf("err = %v, want ErrMalformedRecord", err)
		}
	})

	t.Run("missing required key rejected", func(t *testing.T) {
		// map(1): "TTL" -> 1
		data := []byte{0xa1, 0x63, 'T', 'T', 'L', 0x01}
		if _, err := parseCBOR(data); !errors.Is(err, ErrMalformedRecord) {
			t.Fatalf("err = %v, want ErrMalformedRecord", err)
		}
	})

	t.Run("tag rejected", func(t *testing.T) {
		// map(1): "TTL" -> tag(0) 1
		data := []byte{0xa1, 0x63, 'T', 'T', 'L', 0xc0, 0x01}
		if _, err := parseCBOR(data); !errors.Is(err, ErrMalformedRecord) {
			t.Fatalf("err = %v, want ErrMalformedRecord", err)
		}
	})

	t.Run("unknown key skipped while required keys present", func(t *testing.T) {
		spec := defaultSpec()
		good := encodeTestCBOR(spec)

		// Rebuild as map(6) with an extra "Future" -> 7 entry.
		var data []byte
		data = append(data, 0xa6)
		data = appendCBORText(data, "Future")
		data = appendCBORUint(data, 7)
		data = append(data, good[1:]...) // drop the old map(5) header

		parsed, err := parseCBOR(data)
		if err != nil {
			t.Fatalf("unknown key should be skipped, got %v", err)
		}
		if parsed.Sequence != spec.sequence || parsed.TTL != spec.ttl {
			t.Fatal("known fields not decoded alongside unknown key")
		}
		if !parsed.present["Future"] {
			t.Fatal("unknown key presence not tracked")
		}
	})
}

func TestValidateRejectsMissingData(t *testing.T) {
	pub, priv := newTestSigner(t)
	v := newTestValidator(pub)

	spec := defaultSpec()
	spec.omitData = true
	if _, err := v.Validate(testKey, buildRecord(t, spec, priv)); !errors.Is(err, ErrMalformedRecord) {
		t.Fatalf("err = %v, want ErrMalformedRecord", err)
	}
}

func TestValidateRejectsEmptyData(t *testing.T) {
	pub, priv := newTestSigner(t)
	v := newTestValidator(pub)

	spec := defaultSpec()
	data := encodeTestCBOR(spec)
	sig := ed25519.Sign(priv, append(append([]byte(nil), V2SignaturePrefix...), data...))

	var raw []byte
	raw = appendProtoBytes(raw, fieldValue, spec.value)
	raw = appendProtoVarint(raw, fieldValidityType, spec.validityType)
	raw = appendProtoBytes(raw, fieldValidity, []byte(spec.validity))
	raw = appendProtoVarint(raw, fieldSequence, spec.sequence)
	raw = appendProtoVarint(raw, fieldTTL, spec.ttl)
	raw = appendProtoBytes(raw, fieldV2Signature, sig)
	raw = appendProtoBytes(raw, fieldData, []byte{})

	if _, err := v.Validate(testKey, raw); !errors.Is(err, ErrMissingData) {
		t.Fatalf("err = %v, want ErrMissingData", err)
	}
}
