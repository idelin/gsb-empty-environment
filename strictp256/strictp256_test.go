package strictp256

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"errors"
	"math/big"
	"testing"
)

// ---- DER construction helpers (used to emit deliberately malformed bytes) ----

// derInt encodes a positive integer in canonical shortest-form DER.
func derInt(v *big.Int) []byte {
	raw := v.Bytes()
	if len(raw) == 0 {
		raw = []byte{0x00}
	}
	if raw[0]&0x80 != 0 {
		raw = append([]byte{0x00}, raw...)
	}
	return tlv(tagInteger, raw)
}

// derIntRaw wraps an arbitrary content payload as an INTEGER, allowing
// negative and non-canonical encodings.
func derIntRaw(payload []byte) []byte {
	return tlv(tagInteger, payload)
}

// tlv builds a DER tag-length-value with a minimal length field.
func tlv(tag byte, content []byte) []byte {
	out := []byte{tag}
	n := len(content)
	switch {
	case n < 0x80:
		out = append(out, byte(n))
	case n < 1<<8:
		out = append(out, 0x81, byte(n))
	default:
		out = append(out, 0x82, byte(n>>8), byte(n))
	}
	return append(out, content...)
}

// tlvRaw builds a TLV with a caller-controlled length field, enabling
// non-minimal and indefinite-length cases.
func tlvRaw(tag byte, length []byte, content []byte) []byte {
	out := append([]byte{tag}, length...)
	return append(out, content...)
}

func seq(parts ...[]byte) []byte {
	var content []byte
	for _, p := range parts {
		content = append(content, p...)
	}
	return tlv(tagSequence, content)
}

func p256Order() *big.Int {
	return new(big.Int).Set(elliptic.P256().Params().N)
}

// TestRealSignatureHighAndLowS is the core attack reproduction: one valid
// (r, s), its mirror s2 = n-s, both accepted by the permissive standard
// library but only the low-S form accepted by the strict entry point, while
// both map to the identical content-derived request ID.
func TestRealSignatureHighAndLowS(t *testing.T) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	pub := &key.PublicKey
	msg := []byte("transfer 42 coins from alice to bob")

	r, s, err := ecdsa.Sign(rand.Reader, key, DigestMessage(msg))
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	n := p256Order()

	lowS := new(big.Int).Set(s)
	highS := new(big.Int).Sub(n, s)
	halfN := new(big.Int).Rsh(n, 1)
	if lowS.Cmp(halfN) > 0 {
		lowS, highS = highS, lowS
	}
	if lowS.Cmp(halfN) > 0 {
		t.Fatalf("lowS not <= n/2")
	}
	if highS.Cmp(halfN) <= 0 {
		t.Fatalf("highS not > n/2")
	}
	if got := new(big.Int).Sub(n, lowS); got.Cmp(highS) != 0 {
		t.Fatalf("n - lowS = %v, want highS %v", got, highS)
	}

	lowSig := seq(derInt(r), derInt(lowS))
	highSig := seq(derInt(r), derInt(highS))

	// The permissive standard library verifies both forms.
	if !ecdsa.VerifyASN1(pub, DigestMessage(msg), lowSig) {
		t.Fatal("stdlib rejected valid low-S signature")
	}
	if !ecdsa.VerifyASN1(pub, DigestMessage(msg), highSig) {
		t.Fatal("stdlib rejected valid high-S signature (mirror)")
	}

	// The strict entry point accepts only low-S.
	if err := VerifyStrict(pub, msg, lowSig); err != nil {
		t.Fatalf("strict verify low-S: %v", err)
	}
	if err := VerifyStrict(pub, msg, highSig); !errors.Is(err, ErrHighS) {
		t.Fatalf("strict verify high-S err = %v, want ErrHighS", err)
	}

	// Both variants carry the same request ID because the ID excludes the
	// signature.
	idLow, err := RequestID(pub, msg)
	if err != nil {
		t.Fatalf("request id: %v", err)
	}
	idHigh, err := RequestID(pub, msg)
	if err != nil {
		t.Fatalf("request id: %v", err)
	}
	if idLow != idHigh {
		t.Fatal("request IDs differ for the same content")
	}
}
