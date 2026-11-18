package netsec

import (
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"testing"
	"time"
)

const testKey = "/ipns/k51qzi5uqu5dtest"

var fixedNow = time.Date(2026, 11, 19, 12, 0, 0, 0, time.UTC)

type testRecord struct {
	value        []byte
	validity     []byte
	validityType int64
	sequence     uint64
	ttl          uint64
	signV1       bool
	signV2       bool
	cborFields   map[string]any
	cborData     []byte
	badV2Sig     bool
	omitV2       bool
}

func baseRecord() testRecord {
	return testRecord{
		value:        []byte("/ipfs/bafybeigdyrzt5sfp7udm7hu76uh7y26nf3efuylqabf3oclgtqy55fbzdi"),
		validity:     []byte(fixedNow.Add(2 * time.Hour).Format(time.RFC3339Nano)),
		validityType: int64(ValidityTypeEOL),
		sequence:     10,
		ttl:          uint64(time.Minute),
		signV2:       true,
	}
}

type captureVerifier struct {
	pub     ed25519.PublicKey
	gotData []byte
	gotSig  []byte
	fail    bool
}

func (c *captureVerifier) verify(data, sig []byte) error {
	c.gotData = append([]byte(nil), data...)
	c.gotSig = append([]byte(nil), sig...)
	if c.fail {
		return errors.New("verifier forced failure")
	}
	if !ed25519.Verify(c.pub, data, sig) {
		return errors.New("ed25519 signature verification failed")
	}
	return nil
}

func newKeyPair(t *testing.T) (ed25519.PublicKey, ed25519.PrivateKey) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	return pub, priv
}

func cborMap(fields map[string]any) []byte {
	keys := make([]string, 0, len(fields))
	for k := range fields {
		keys = append(keys, k)
	}
	sortStrings(keys)

	out := cborHead(5, uint64(len(keys)))
	for _, k := range keys {
		out = append(out, cborText(k)...)
		out = append(out, cborAny(fields[k])...)
	}
	return out
}

func sortStrings(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j-1] > s[j]; j-- {
			s[j-1], s[j] = s[j], s[j-1]
		}
	}
}

func cborAny(v any) []byte {
	switch x := v.(type) {
	case uint64:
		return cborHead(0, x)
	case int64:
		return cborHead(0, uint64(x))
	case []byte:
		return append(cborHead(2, uint64(len(x))), x...)
	case string:
		return cborText(x)
	default:
		panic("unsupported cbor test value")
	}
}

func cborText(s string) []byte {
	return append(cborHead(3, uint64(len(s))), s...)
}

func cborHead(major byte, n uint64) []byte {
	base := major << 5
	switch {
	case n < 24:
		return []byte{base | byte(n)}
	case n <= 0xff:
		return []byte{base | 24, byte(n)}
	case n <= 0xffff:
		return []byte{base | 25, byte(n >> 8), byte(n)}
	case n <= 0xffffffff:
		return []byte{base | 26, byte(n >> 24), byte(n >> 16), byte(n >> 8), byte(n)}
	default:
		return []byte{base | 27,
			byte(n >> 56), byte(n >> 48), byte(n >> 40), byte(n >> 32),
			byte(n >> 24), byte(n >> 16), byte(n >> 8), byte(n)}
	}
}

func defaultCBOR(r testRecord) map[string]any {
	return map[string]any{
		"Value":        append([]byte(nil), r.value...),
		"Validity":     append([]byte(nil), r.validity...),
		"ValidityType": uint64(r.validityType),
		"Sequence":     r.sequence,
		"TTL":          r.ttl,
	}
}

func pbVarint(field int, v uint64) []byte {
	out := []byte{byte(field << 3)}
	for v >= 0x80 {
		out = append(out, byte(v)|0x80)
		v >>= 7
	}
	out = append(out, byte(v))
	return out
}

func pbBytes(field int, raw []byte) []byte {
	out := []byte{byte(field<<3 | 2)}
	v := uint64(len(raw))
	for v >= 0x80 {
		out = append(out, byte(v)|0x80)
		v >>= 7
	}
	out = append(out, byte(v))
	return append(out, raw...)
}

func buildRecord(t *testing.T, r testRecord, priv ed25519.PrivateKey) []byte {
	t.Helper()

	fields := r.cborFields
	if fields == nil {
		fields = defaultCBOR(r)
	}
	cborData := r.cborData
	if cborData == nil {
		cborData = cborMap(fields)
	}

	var sigV1, sigV2 []byte
	if r.signV1 {
		sigV1 = ed25519.Sign(priv, r.value)
	}
	if r.signV2 && !r.omitV2 {
		signed := append([]byte(V2SignaturePrefix), cborData...)
		sigV2 = ed25519.Sign(priv, signed)
		if r.badV2Sig {
			sigV2 = append([]byte(nil), sigV2...)
			sigV2[0] ^= 0xff
		}
	}

	out := pbBytes(pbFieldValue, r.value)
	out = append(out, pbBytes(pbFieldSignatureV1, sigV1)...)
	out = append(out, pbBytes(pbFieldValidity, r.validity)...)
	out = append(out, pbVarint(pbFieldValidityType, uint64(r.validityType))...)
	out = append(out, pbVarint(pbFieldSequence, r.sequence)...)
	out = append(out, pbVarint(pbFieldTTL, r.ttl)...)
	if !r.omitV2 {
		out = append(out, pbBytes(pbFieldSignatureV2, sigV2)...)
	}
	out = append(out, pbBytes(pbFieldCBORData, cborData)...)
	return out
}

func fixedValidator(verifier Verifier) *Validator {
	return &Validator{Verify: verifier, Now: func() time.Time { return fixedNow }}
}

func wantErrIs(t *testing.T, err error, target error) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected error wrapping %v, got nil", target)
	}
	if !errors.Is(err, target) {
		t.Fatalf("expected error wrapping %v, got %v", target, err)
	}
}
