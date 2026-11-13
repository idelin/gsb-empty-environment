package strictp256

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"errors"
	"math/big"
	"testing"
)

// TestStrictRejectsMalformedDER covers the structural DER requirements.
func TestStrictRejectsMalformedDER(t *testing.T) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	pub := &key.PublicKey
	msg := []byte("malformed-der-probe")
	r, s, err := ecdsa.Sign(rand.Reader, key, DigestMessage(msg))
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	// Force low-S so a well-formed control signature verifies.
	n := p256Order()
	if s.Cmp(new(big.Int).Rsh(n, 1)) > 0 {
		s.Sub(n, s)
	}
	good := seq(derInt(r), derInt(s))
	if err := VerifyStrict(pub, msg, good); err != nil {
		t.Fatalf("control signature must verify: %v", err)
	}

	rRaw := derInt(r)
	sRaw := derInt(s)

	// A 32-byte positive INTEGER makes each TLV 34 bytes and the SEQUENCE
	// content 68 bytes (0x44), which uses the single-byte short length form.
	longContent := make([]byte, 32)
	longContent[0] = 0x01
	longInt := tlv(tagInteger, longContent)

	cases := map[string][]byte{
		// Negative INTEGER: sign bit set with no leading zero octet.
		"negative r": seq(derIntRaw([]byte{0x80}), sRaw),
		"negative s": seq(rRaw, derIntRaw([]byte{0xff, 0x01})),
		// Unnecessary leading zero octet.
		"redundant zero r": seq(derIntRaw(append([]byte{0x00, 0x01}, r.Bytes()...)), sRaw),
		"redundant zero s": seq(rRaw, derIntRaw(append([]byte{0x00, 0x01}, s.Bytes()...))),
		// Trailing data after the SEQUENCE.
		"trailing byte":    append(append([]byte{}, good...), 0x00),
		"trailing integer": append(append([]byte{}, good...), derInt(big.NewInt(1))...),
		// A third INTEGER inside the SEQUENCE.
		"third integer": seq(derInt(r), derInt(s), derInt(big.NewInt(1))),
		// Non-minimal long-form length where short form would do.
		"non-minimal seq length": tlvRaw(tagSequence, []byte{0x81, byte(len(longInt) * 2)},
			append(append([]byte{}, longInt...), longInt...)),
		// Long-form length starting with a zero octet.
		"zero-padded long length": tlvRaw(tagSequence, []byte{0x82, 0x00, byte(len(longInt) * 2)},
			append(append([]byte{}, longInt...), longInt...)),
		// Indefinite length is forbidden.
		"indefinite length": append([]byte{tagSequence, 0x80}, good[2:]...),
		// Wrong outer / inner tags.
		"wrong outer tag": tlv(0x31, append(append([]byte{}, rRaw...), sRaw...)),
		"wrong inner tag": seq(tlv(0x03, r.Bytes()), sRaw),
		// Truncated and empty inputs.
		"truncated": good[:len(good)-3],
		"empty":     {},
		"garbage":   []byte{0x00, 0x01, 0x02},
		// Empty INTEGER content.
		"empty integer": seq(tlv(tagInteger, nil), sRaw),
	}

	for name, malformed := range cases {
		t.Run(name, func(t *testing.T) {
			if err := VerifyStrict(pub, msg, malformed); !errors.Is(err, ErrMalformedDER) {
				t.Fatalf("err = %v, want ErrMalformedDER", err)
			}
		})
	}
}

// TestScalarBounds exercises 1 <= r, s < n and the s <= n/2 boundary using
// well-formed DER so that range and low-S policy are reached distinctly.
func TestScalarBounds(t *testing.T) {
	n := p256Order()
	one := big.NewInt(1)
	nm1 := new(big.Int).Sub(n, one)
	halfN := new(big.Int).Rsh(n, 1)
	halfPlusOne := new(big.Int).Add(halfN, one)

	structureOK := func(err error) bool {
		return err == nil || errors.Is(err, ErrIntegerRange) || errors.Is(err, ErrHighS)
	}

	cases := []struct {
		name    string
		r, s    *big.Int
		wantErr error
	}{
		{"r zero", big.NewInt(0), one, ErrIntegerRange},
		{"s zero", one, big.NewInt(0), ErrIntegerRange},
		{"r equals n", new(big.Int).Set(n), one, ErrIntegerRange},
		{"s equals n", one, new(big.Int).Set(n), ErrIntegerRange},
		{"r equals n+1", new(big.Int).Add(n, one), one, ErrIntegerRange},
		{"s equals n+1", one, new(big.Int).Add(n, one), ErrIntegerRange},
		{"max r and s", nm1, nm1, ErrHighS},
		{"s at half plus one", one, halfPlusOne, ErrHighS},
		{"s exactly half boundary", one, halfN, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			sig := seq(derInt(tc.r), derInt(tc.s))
			parsedR, parsedS, err := parseSignature(sig)
			if err != nil {
				t.Fatalf("structure should be valid, got %v", err)
			}
			err = enforceScalarBounds(parsedR, parsedS, n)
			if !structureOK(err) {
				t.Fatalf("unexpected structural error %v", err)
			}
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("err = %v, want %v", err, tc.wantErr)
			}
		})
	}
}

// TestInvalidPublicKey rejects nil and off-curve keys before any parsing.
func TestInvalidPublicKey(t *testing.T) {
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	msg := []byte("m")
	sig := seq(derInt(big.NewInt(1)), derInt(big.NewInt(1)))

	if err := VerifyStrict(nil, msg, sig); !errors.Is(err, ErrInvalidPublicKey) {
		t.Fatalf("nil key err = %v", err)
	}

	bad := &ecdsa.PublicKey{
		Curve: elliptic.P256(),
		X:     big.NewInt(1),
		Y:     big.NewInt(1),
	}
	if err := VerifyStrict(bad, msg, sig); !errors.Is(err, ErrInvalidPublicKey) {
		t.Fatalf("off-curve key err = %v", err)
	}

	if _, err := CompressedPublicKey(nil); !errors.Is(err, ErrInvalidPublicKey) {
		t.Fatalf("compress nil err = %v", err)
	}
	_ = key
}
