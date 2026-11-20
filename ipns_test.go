package netsec

import (
	"bytes"
	"crypto/ed25519"
	"errors"
	"testing"
	"time"
)

const testKey = "/ipns/k51qzi5uqu5dtestkeytestkeytestkeytestkeytestkey0000"

var testClock = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

func newTestSigner(t *testing.T) (ed25519.PublicKey, ed25519.PrivateKey) {
	t.Helper()
	seed := make([]byte, ed25519.SeedSize)
	for i := range seed {
		seed[i] = byte(i + 1)
	}
	return signerFromSeed(seed)
}

func newTestSignerWithOffset(t *testing.T, offset byte) (ed25519.PublicKey, ed25519.PrivateKey) {
	t.Helper()
	seed := make([]byte, ed25519.SeedSize)
	for i := range seed {
		seed[i] = byte(i+1) ^ offset
	}
	return signerFromSeed(seed)
}

func signerFromSeed(seed []byte) (ed25519.PublicKey, ed25519.PrivateKey) {
	priv := ed25519.NewKeyFromSeed(seed)
	return priv.Public().(ed25519.PublicKey), priv
}

func testVerifier(pub ed25519.PublicKey) V2Verifier {
	return func(_ string, signed, signature []byte) error {
		if !bytes.Equal(signed[:len(V2SignaturePrefix)], V2SignaturePrefix) {
			return errors.New("missing domain-separation prefix")
		}
		if ed25519.Verify(pub, signed, signature) {
			return nil
		}
		return errors.New("bad ed25519 signature")
	}
}

func newTestValidator(pub ed25519.PublicKey) *Validator {
	clock := testClock
	return NewValidator(Config{
		Verify: testVerifier(pub),
		Now:    func() time.Time { return clock },
	})
}

// recordSpec describes a synthetic IpnsEntry for the tests.
type recordSpec struct {
	value        []byte
	validity     string
	validityType uint64
	sequence     uint64
	ttl          uint64
	includeV1    bool
	omitV2       bool
	omitData     bool
}

func defaultSpec() recordSpec {
	return recordSpec{
		value:        []byte("/ipfs/bafybeihtest"),
		validity:     "2030-01-01T00:00:00Z",
		validityType: ValidityEOL,
		sequence:     10,
		ttl:          uint64(time.Hour / time.Nanosecond),
	}
}

func buildRecord(t *testing.T, spec recordSpec, signer ed25519.PrivateKey) []byte {
	t.Helper()

	data := encodeTestCBOR(spec)

	var msg []byte
	msg = appendProtoBytes(msg, fieldValue, spec.value)
	if spec.includeV1 {
		msg = appendProtoBytes(msg, fieldSignature, []byte("legacy-v1-junk"))
	}
	msg = appendProtoVarint(msg, fieldValidityType, spec.validityType)
	msg = appendProtoBytes(msg, fieldValidity, []byte(spec.validity))
	msg = appendProtoVarint(msg, fieldSequence, spec.sequence)
	msg = appendProtoVarint(msg, fieldTTL, spec.ttl)
	if !spec.omitV2 && !spec.omitData {
		payload := append(append([]byte(nil), V2SignaturePrefix...), data...)
		sig := ed25519.Sign(signer, payload)
		msg = appendProtoBytes(msg, fieldV2Signature, sig)
	}
	if !spec.omitData {
		msg = appendProtoBytes(msg, fieldData, data)
	}
	return msg
}

func appendProtoVarint(buf []byte, field, value uint64) []byte {
	buf = appendUvarint(buf, field<<3)
	buf = appendUvarint(buf, value)
	return buf
}

func appendProtoBytes(buf []byte, field uint64, value []byte) []byte {
	buf = appendUvarint(buf, field<<3|2)
	buf = appendUvarint(buf, uint64(len(value)))
	return append(buf, value...)
}

func appendUvarint(buf []byte, x uint64) []byte {
	for x >= 0x80 {
		buf = append(buf, byte(x)|0x80)
		x >>= 7
	}
	return append(buf, byte(x))
}

// encodeTestCBOR emits the definite-length CBOR map used as IpnsEntry.data.
func encodeTestCBOR(spec recordSpec) []byte {
	var b []byte
	b = append(b, 0xa5) // map(5)

	b = appendCBORText(b, cborKeyValue)
	b = appendCBORBytes(b, spec.value)

	b = appendCBORText(b, cborKeyValidity)
	b = appendCBORBytes(b, []byte(spec.validity))

	b = appendCBORText(b, cborKeyValidityType)
	b = appendCBORUint(b, spec.validityType)

	b = appendCBORText(b, cborKeySequence)
	b = appendCBORUint(b, spec.sequence)

	b = appendCBORText(b, cborKeyTTL)
	b = appendCBORUint(b, spec.ttl)
	return b
}

func appendCBORUint(b []byte, x uint64) []byte {
	switch {
	case x < 24:
		return append(b, byte(x))
	case x <= 0xff:
		return append(b, 24, byte(x))
	case x <= 0xffff:
		return append(b, 25, byte(x>>8), byte(x))
	case x <= 0xffffffff:
		return append(b, 26, byte(x>>24), byte(x>>16), byte(x>>8), byte(x))
	default:
		return append(b, 27,
			byte(x>>56), byte(x>>48), byte(x>>40), byte(x>>32),
			byte(x>>24), byte(x>>16), byte(x>>8), byte(x))
	}
}

func appendCBORBytes(b []byte, value []byte) []byte {
	b = append(b, 0x40|cborLenInfo(len(value)))
	b = appendCBORLen(b, len(value))
	return append(b, value...)
}

func appendCBORText(b []byte, value string) []byte {
	b = append(b, 0x60|cborLenInfo(len(value)))
	b = appendCBORLen(b, len(value))
	return append(b, value...)
}

func cborLenInfo(length int) byte {
	switch {
	case length < 24:
		return byte(length)
	case length <= 0xff:
		return 24
	case length <= 0xffff:
		return 25
	case length <= 0xffffffff:
		return 26
	default:
		return 27
	}
}

func appendCBORLen(b []byte, length int) []byte {
	switch {
	case length < 24:
		return b
	case length <= 0xff:
		return append(b, byte(length))
	case length <= 0xffff:
		return append(b, byte(length>>8), byte(length))
	case length <= 0xffffffff:
		return append(b, byte(length>>24), byte(length>>16), byte(length>>8), byte(length))
	default:
		return append(b,
			byte(length>>56), byte(length>>48), byte(length>>40), byte(length>>32),
			byte(length>>24), byte(length>>16), byte(length>>8), byte(length))
	}
}
