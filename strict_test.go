package netsec

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha512"
	"encoding/hex"
	"errors"
	"math/big"
	"testing"
)

func mustHex(t *testing.T, s string) []byte {
	t.Helper()
	b, err := hex.DecodeString(s)
	if err != nil {
		t.Fatalf("bad hex: %v", err)
	}
	return b
}

// Canonical encodings of small-order points on edwards25519.
var (
	encIdentity      = "0100000000000000000000000000000000000000000000000000000000000000" // order 1
	encOrder2        = "ecffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff7f" // order 2
	encOrder4a       = "0000000000000000000000000000000000000000000000000000000000000000" // order 4
	encOrder4b       = "0000000000000000000000000000000000000000000000000000000000000080" // order 4
	encOrder8a       = "26e8958fc2b227b045c3f489f2ef98f0d5dfac05d3c63339b13802886d53fc05" // order 8
	encOrder8b       = "c7176a703d4dd84fba3c0b760d10670f2a2053fa2c39ccc64ec7fd7792ac037a" // order 8
	encIdentityPlusP = "eeffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff7f" // y = 1 + p, non-canonical
)

func TestVerifyStrictAcceptsValidSignature(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	msg := []byte("license: product=pro, expires=2027-01-01")
	sig := ed25519.Sign(priv, msg)

	if err := VerifyStrict(pub, msg, sig); err != nil {
		t.Fatalf("valid signature rejected: %v", err)
	}
}

func TestVerifyStrictRejectsSPlusL(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	msg := []byte("license: product=pro")
	sig := ed25519.Sign(priv, msg)

	// Malleate: S' = S + L. The equation [S']B = R + [k]A still holds, so a
	// lax verifier accepts this as a second valid signature for the same
	// message.
	malleated := make([]byte, len(sig))
	copy(malleated, sig)
	s := leToBig(malleated[32:])
	s.Add(s, groupOrder)
	sBytes := s.FillBytes(make([]byte, 32))
	for i := 0; i < 32; i++ {
		malleated[32+i] = sBytes[31-i]
	}

	if !laxVerify(pub, msg, malleated) {
		t.Fatal("sanity check: S+L malleated signature must satisfy the verification equation")
	}
	if err := VerifyStrict(pub, msg, malleated); !errors.Is(err, ErrNonCanonicalS) {
		t.Fatalf("S+L signature: got %v, want ErrNonCanonicalS", err)
	}
}

func TestVerifyStrictRejectsSmallOrderPublicKey(t *testing.T) {
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	msg := []byte("license")
	sig := ed25519.Sign(priv, msg)

	for _, enc := range []string{encOrder2, encOrder4a, encOrder4b, encOrder8a, encOrder8b} {
		pub := mustHex(t, enc)
		if err := VerifyStrict(pub, msg, sig); !errors.Is(err, ErrSmallOrderPoint) {
			t.Fatalf("small-order pubkey %s: got %v, want ErrSmallOrderPoint", enc, err)
		}
	}
}

func TestVerifyStrictRejectsIdentityPublicKey(t *testing.T) {
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	msg := []byte("license")
	sig := ed25519.Sign(priv, msg)
	pub := mustHex(t, encIdentity)
	if err := VerifyStrict(pub, msg, sig); !errors.Is(err, ErrIdentityPublicKey) {
		t.Fatalf("identity pubkey: got %v, want ErrIdentityPublicKey", err)
	}
}

func TestVerifyStrictRejectsSmallOrderR(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	msg := []byte("license")
	sig := ed25519.Sign(priv, msg)

	for _, enc := range []string{encIdentity, encOrder2, encOrder8a} {
		forged := make([]byte, len(sig))
		copy(forged, sig)
		copy(forged[:32], mustHex(t, enc))
		if err := VerifyStrict(pub, msg, forged); !errors.Is(err, ErrSmallOrderPoint) {
			t.Fatalf("small-order R %s: got %v, want ErrSmallOrderPoint", enc, err)
		}
	}
}

func TestVerifyStrictRejectsNonCanonicalEncoding(t *testing.T) {
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	msg := []byte("license")
	sig := ed25519.Sign(priv, msg)

	// Public key with y = 1 + p (non-canonical identity encoding).
	pub := mustHex(t, encIdentityPlusP)
	if err := VerifyStrict(pub, msg, sig); !errors.Is(err, ErrNonCanonicalPoint) {
		t.Fatalf("non-canonical pubkey: got %v, want ErrNonCanonicalPoint", err)
	}

	// Same non-canonical encoding in the R position.
	forged := make([]byte, len(sig))
	copy(forged, sig)
	copy(forged[:32], mustHex(t, encIdentityPlusP))
	if err := VerifyStrict(ed25519.PrivateKey(priv).Public().(ed25519.PublicKey), msg, forged); !errors.Is(err, ErrNonCanonicalPoint) {
		t.Fatalf("non-canonical R: got %v, want ErrNonCanonicalPoint", err)
	}
}

func TestVerifyStrictRejectsForgery(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	sig := ed25519.Sign(priv, []byte("license A"))
	if err := VerifyStrict(pub, []byte("license B"), sig); !errors.Is(err, ErrInvalidSignature) {
		t.Fatalf("forged message: got %v, want ErrInvalidSignature", err)
	}
}

func TestRevocationKeyIgnoresSignatureBytes(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	msg := []byte("license: product=pro")

	// Two distinct valid signatures for the same message (different nonces
	// via different private keys are not possible; instead confirm the key
	// derivation never touches signature bytes at all).
	sig1 := ed25519.Sign(priv, msg)
	sig2 := ed25519.Sign(priv, msg)
	if string(sig1) != string(sig2) {
		t.Fatal("ed25519 should be deterministic")
	}

	k1 := RevocationKey(pub, msg)
	k2 := RevocationKey(pub, msg)
	if k1 != k2 {
		t.Fatal("revocation key not stable for same message and key")
	}

	// A malleated S+L signature for the same message maps to the same
	// revocation entry, because the key does not depend on the signature.
	malleated := make([]byte, len(sig1))
	copy(malleated, sig1)
	s := leToBig(malleated[32:])
	s.Add(s, groupOrder)
	sBytes := s.FillBytes(make([]byte, 32))
	for i := 0; i < 32; i++ {
		malleated[32+i] = sBytes[31-i]
	}
	if k3 := RevocationKey(pub, msg); k3 != k1 {
		t.Fatal("revocation key changed for malleated signature")
	}

	// Different message or different key must produce a different entry.
	if k := RevocationKey(pub, []byte("license: product=basic")); k == k1 {
		t.Fatal("revocation key did not change with message")
	}
	otherPub, _, _ := ed25519.GenerateKey(rand.Reader)
	if k := RevocationKey(otherPub, msg); k == k1 {
		t.Fatal("revocation key did not change with public key")
	}
}

func TestDecodePointStrictRoundTrip(t *testing.T) {
	pub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	p, err := decodePointStrict(pub)
	if err != nil {
		t.Fatalf("valid public key rejected: %v", err)
	}
	if isSmallOrder(p) {
		t.Fatal("valid public key misclassified as small order")
	}
	if isIdentity(p) {
		t.Fatal("valid public key misclassified as identity")
	}
	// Curve equation check: -x^2 + y^2 = 1 + d*x^2*y^2 (mod p).
	x2 := new(big.Int).Mul(p.x, p.x)
	x2.Mod(x2, fieldPrime)
	y2 := new(big.Int).Mul(p.y, p.y)
	y2.Mod(y2, fieldPrime)
	lhs := new(big.Int).Sub(y2, x2)
	lhs.Mod(lhs, fieldPrime)
	rhs := new(big.Int).Mul(x2, y2)
	rhs.Mul(rhs, curveParamD)
	rhs.Add(rhs, bigOne)
	rhs.Mod(rhs, fieldPrime)
	if lhs.Cmp(rhs) != 0 {
		t.Fatal("decoded point not on curve")
	}
}

// basepoint is the edwards25519 base point B.
var basepoint = &point{
	x: mustBig("15112221349535400772501151409588531511454012693041857206046113283949847762202"),
	y: mustBig("46316835694926478169428394003475163141307993866256225615783033603165251855960"),
}

func mustBig(decimal string) *big.Int {
	n, ok := new(big.Int).SetString(decimal, 10)
	if !ok {
		panic("bad decimal constant")
	}
	return n
}

// scalarMult computes [k]P by simple double-and-add. Test-only helper.
func scalarMult(p *point, k *big.Int) *point {
	result := &point{x: new(big.Int), y: new(big.Int).Set(bigOne)}
	addend := p
	for i := 0; i < k.BitLen(); i++ {
		if k.Bit(i) == 1 {
			result = addPoints(result, addend)
		}
		addend = addPoints(addend, addend)
	}
	return result
}

// laxVerify checks only the Ed25519 verification equation
// [S]B == R + [k]A with k = SHA512(R || A || M) mod L, without any
// canonicality or small-order checks. It demonstrates which forgeries a
// non-strict verifier would accept.
func laxVerify(publicKey, message, signature []byte) bool {
	a, err := decodePointStrict(publicKey)
	if err != nil {
		return false
	}
	r, err := decodePointStrict(signature[:32])
	if err != nil {
		return false
	}
	s := leToBig(signature[32:])

	h := sha512.New()
	h.Write(signature[:32])
	h.Write(publicKey)
	h.Write(message)
	k := new(big.Int).SetBytes(reverse(h.Sum(nil)))
	k.Mod(k, groupOrder)

	lhs := scalarMult(basepoint, s)
	rhs := addPoints(r, scalarMult(a, k))
	lhs.x.Mod(lhs.x, fieldPrime)
	lhs.y.Mod(lhs.y, fieldPrime)
	rhs.x.Mod(rhs.x, fieldPrime)
	rhs.y.Mod(rhs.y, fieldPrime)
	return lhs.x.Cmp(rhs.x) == 0 && lhs.y.Cmp(rhs.y) == 0
}

func reverse(b []byte) []byte {
	out := make([]byte, len(b))
	for i, c := range b {
		out[len(b)-1-i] = c
	}
	return out
}

func TestLaxVerifySanity(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	msg := []byte("license")
	sig := ed25519.Sign(priv, msg)
	if !laxVerify(pub, msg, sig) {
		t.Fatal("laxVerify rejected a genuine signature")
	}
}

func encodePoint(p *point) []byte {
	enc := make([]byte, 32)
	yBytes := p.y.FillBytes(make([]byte, 32))
	for i := 0; i < 32; i++ {
		enc[i] = yBytes[31-i]
	}
	enc[31] |= byte(p.x.Bit(0)) << 7
	return enc
}

// TestRFCKnownAnswer pins both the lax equation check and the strict
// verifier against RFC 8032 test vector 1.
func TestRFCKnownAnswer(t *testing.T) {
	pub := mustHex(t, "d75a980182b10ab7d54bfed3c964073a0ee172f3daa62325af021a68f707511a")
	sig := mustHex(t, "e5564300c360ac729086e2cc806e828a84877f1eb8e5d974d873e065224901555fb8821590a33bacc61e39701cf9b46bd25bf5f0595bbe24655141438e7a100b")
	if !laxVerify(pub, nil, sig) {
		t.Fatal("RFC 8032 test vector 1 failed laxVerify")
	}
	if err := VerifyStrict(pub, nil, sig); err != nil {
		t.Fatalf("RFC 8032 test vector 1 failed VerifyStrict: %v", err)
	}
}

func TestScalarMultMatchesPublicKey(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	h := sha512.Sum512(priv.Seed())
	a := reverse(h[:32])
	a[0] &= 127
	a[0] |= 64
	a[31] &= 248
	scalar := new(big.Int).SetBytes(a)
	got := scalarMult(basepoint, scalar)
	enc := encodePoint(got)
	if string(enc) != string(pub) {
		t.Fatalf("scalarMult(basepoint, a) mismatch:\ngot  %x\nwant %x", enc, pub)
	}
}
