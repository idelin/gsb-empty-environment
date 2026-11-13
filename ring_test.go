package netsec

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"math/big"
	"testing"
)

func q0047TestRing(t *testing.T, size int) ([]*ecdsa.PrivateKey, [][]byte) {
	t.Helper()
	keys := make([]*ecdsa.PrivateKey, size)
	ring := make([][]byte, size)
	for i := range keys {
		key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		if err != nil {
			t.Fatal(err)
		}
		keys[i] = key
		ring[i] = marshalCompressed(&point{key.X, key.Y})
	}
	return keys, ring
}

func TestQ0047SignVerifyEveryPosition(t *testing.T) {
	keys, ring := q0047TestRing(t, 5)
	message := []byte("anonymous report")
	scope := []byte("scope-a")
	for i, key := range keys {
		sig, err := GenerateRingSignature(ring, i, key, message, scope, nil)
		if err != nil {
			t.Fatalf("sign position %d: %v", i, err)
		}
		ok, err := VerifySignature(ring, message, scope, sig)
		if err != nil || !ok {
			t.Fatalf("verify position %d: ok=%v err=%v", i, ok, err)
		}
	}
}

func TestQ0047TamperAndRingOrder(t *testing.T) {
	keys, ring := q0047TestRing(t, 4)
	message := []byte("message")
	scope := []byte("scope")
	sig, err := GenerateRingSignature(ring, 2, keys[2], message, scope, nil)
	if err != nil {
		t.Fatal(err)
	}

	otherMessage := append([]byte(nil), message...)
	otherMessage[0] ^= 1
	if ok, err := VerifySignature(ring, otherMessage, scope, sig); err != nil || ok {
		t.Fatalf("changed message accepted: ok=%v err=%v", ok, err)
	}

	replaced := *sig
	replaced.I = append([]byte(nil), sig.I...)
	replaced.I[1] ^= 1
	replaced.KeyImage = replaced.I
	if ok, err := VerifySignature(ring, message, scope, &replaced); err == nil && ok {
		t.Fatal("replaced key image accepted")
	}

	reordered := [][]byte{ring[3], ring[1], ring[0], ring[2]}
	if ok, err := VerifySignature(reordered, message, scope, sig); err != nil || ok {
		t.Fatalf("reordered ring accepted: ok=%v err=%v", ok, err)
	}
}

func TestQ0047LinkScope(t *testing.T) {
	keys, ring := q0047TestRing(t, 3)
	message := []byte("message")
	sigA, err := GenerateRingSignature(ring, 1, keys[1], message, []byte("scope-a"), nil)
	if err != nil {
		t.Fatal(err)
	}
	sigA2, err := GenerateRingSignature(ring, 1, keys[1], message, []byte("scope-a"), nil)
	if err != nil {
		t.Fatal(err)
	}
	sigB, err := GenerateRingSignature(ring, 1, keys[1], message, []byte("scope-b"), nil)
	if err != nil {
		t.Fatal(err)
	}
	linked, err := LinkSignatures(sigA, sigA2)
	if err != nil || !linked {
		t.Fatalf("same scope should link: %v %v", linked, err)
	}
	linked, err = LinkSignatures(sigA, sigB)
	if err != nil {
		t.Fatal(err)
	}
	if linked {
		t.Fatal("different scopes linked")
	}

	otherSigner, err := GenerateRingSignature(ring, 2, keys[2], message, []byte("scope-a"), nil)
	if err != nil {
		t.Fatal(err)
	}
	linked, err = LinkSignatures(sigA, otherSigner)
	if err != nil {
		t.Fatal(err)
	}
	if linked {
		t.Fatal("different signers linked")
	}
}

func TestQ0047DeterministicZeroResponse(t *testing.T) {
	keys, ring := q0047TestRing(t, 2)
	// First scalar is the nonce (non-zero), the second is a response (zero).
	var randomness bytes.Buffer
	nonce := make([]byte, 32)
	nonce[31] = 7
	randomness.Write(nonce)
	randomness.Write(make([]byte, 32))
	sig, err := GenerateRingSignature(ring, 0, keys[0], []byte("m"), []byte("s"), &randomness)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(sig.S[1], make([]byte, 32)) {
		t.Fatal("expected zero response to be preserved")
	}
	ok, err := VerifySignature(ring, []byte("m"), []byte("s"), sig)
	if err != nil || !ok {
		t.Fatalf("verify: ok=%v err=%v", ok, err)
	}
}

func TestQ0047RejectInvalidInputs(t *testing.T) {
	keys, ring := q0047TestRing(t, 2)
	message, scope := []byte("m"), []byte("s")

	duplicate := [][]byte{ring[0], ring[0]}
	if _, err := GenerateRingSignature(duplicate, 0, keys[0], message, scope, nil); err == nil {
		t.Fatal("duplicate ring accepted")
	}

	badPoint := append([][]byte(nil), ring...)
	badPoint[0] = []byte{0x04}
	if _, err := GenerateRingSignature(badPoint, 0, keys[0], message, scope, nil); err == nil {
		t.Fatal("invalid point accepted")
	}

	sig, err := GenerateRingSignature(ring, 0, keys[0], message, scope, nil)
	if err != nil {
		t.Fatal(err)
	}
	sig.C0 = append([]byte(nil), sig.C0...)
	copy(sig.C0, GroupOrder().Bytes())
	if _, err := VerifySignature(ring, message, scope, sig); err == nil {
		t.Fatal("non-canonical challenge accepted")
	}

	sig, err = GenerateRingSignature(ring, 0, keys[0], message, scope, nil)
	if err != nil {
		t.Fatal(err)
	}
	sig.I = make([]byte, 33)
	sig.KeyImage = sig.I
	if _, err := VerifySignature(ring, message, scope, sig); err == nil {
		t.Fatal("identity-like key image accepted")
	}
}

func TestQ0047RingSizeBounds(t *testing.T) {
	if _, err := GenerateRingSignature(nil, 0, nil, nil, nil, nil); err == nil {
		t.Fatal("empty ring accepted")
	}
	keys, ring := q0047TestRing(t, MaxRingSize+1)
	if _, err := GenerateRingSignature(ring, 0, keys[0], nil, nil, nil); err == nil {
		t.Fatal("oversized ring accepted")
	}
}

func TestQ0047FlexibleAPI(t *testing.T) {
	keys, ring := q0047TestRing(t, 3)
	message, scope := []byte("m"), []byte("s")
	publicKeys := []*ecdsa.PublicKey{&keys[0].PublicKey, &keys[1].PublicKey, &keys[2].PublicKey}

	sig, err := Sign(publicKeys, 1, keys[1], message, scope)
	if err != nil {
		t.Fatal(err)
	}
	ok, err := Verify(ring, message, scope, sig)
	if err != nil || !ok {
		t.Fatalf("verify flexible: ok=%v err=%v", ok, err)
	}
	ok, err = VerifySignature(sig, ring, message, scope)
	if err != nil || !ok {
		t.Fatalf("verify reordered: ok=%v err=%v", ok, err)
	}
	if !VerifyOK(ring, message, scope, sig) {
		t.Fatal("VerifyOK failed")
	}

	scalarSig, err := Generate(ring, 2, keys[2].D, message, scope)
	if err != nil {
		t.Fatal(err)
	}
	if ok, err := VerifySignature(ring, message, scope, scalarSig); err != nil || !ok {
		t.Fatalf("scalar sign: ok=%v err=%v", ok, err)
	}
	if !Link(sig, scalarSig) {
		// Different signers must not link.
	} else {
		t.Fatal("different signers linked through flexible API")
	}
	sameSigner, err := Sign(ring, 1, keys[1], []byte("other"), scope)
	if err != nil {
		t.Fatal(err)
	}
	if !Link(sig, sameSigner) {
		t.Fatal("same signer did not link")
	}
	if !AreLinked(sig, sameSigner) {
		t.Fatal("AreLinked failed")
	}
	if !Link(sig.I, sameSigner.I) {
		t.Fatal("raw key-image link failed")
	}

	image, err := KeyImage(keys[1], ring[1], scope)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(image, sig.I) {
		t.Fatal("key image mismatch")
	}
	image, err = KeyImage(keys[1].D, ring[1], scope)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(image, sig.I) {
		t.Fatal("scalar key image mismatch")
	}
}

func TestQ0047HpProperties(t *testing.T) {
	_, ring := q0047TestRing(t, 1)
	for _, scope := range [][]byte{nil, []byte("a"), []byte("b")} {
		p, err := Hp(ring[0], scope)
		if err != nil {
			t.Fatal(err)
		}
		if p.isInfinity() {
			t.Fatal("Hp returned infinity")
		}
		if p.y.Bit(0) != 0 {
			t.Fatal("Hp returned odd y")
		}
		if !curveP256.IsOnCurve(p.x, p.y) {
			t.Fatal("Hp returned point off curve")
		}
		encoded, err := HpBytes(ring[0], scope)
		if err != nil {
			t.Fatal(err)
		}
		again, err := parseCompressedPoint(encoded)
		if err != nil || !pointEqual(p, again) {
			t.Fatalf("HpBytes mismatch: %v", err)
		}
	}
	a, _ := Hp(ring[0], []byte("a"))
	b, _ := Hp(ring[0], []byte("b"))
	if pointEqual(a, b) {
		t.Fatal("scope did not affect Hp")
	}
}

func TestQ0047MaxRing(t *testing.T) {
	keys, ring := q0047TestRing(t, MaxRingSize)
	message, scope := []byte("max"), []byte("scope")
	sig, err := GenerateRingSignature(ring, MaxRingSize-1, keys[MaxRingSize-1], message, scope, nil)
	if err != nil {
		t.Fatal(err)
	}
	if ok, err := VerifySignature(ring, message, scope, sig); err != nil || !ok {
		t.Fatalf("max ring: ok=%v err=%v", ok, err)
	}
}

func TestQ0047SingleMemberRing(t *testing.T) {
	keys, ring := q0047TestRing(t, 1)
	sig, err := GenerateRingSignature(ring, 0, keys[0], []byte("m"), []byte("s"), nil)
	if err != nil {
		t.Fatal(err)
	}
	if ok, err := VerifySignature(ring, []byte("m"), []byte("s"), sig); err != nil || !ok {
		t.Fatalf("single member: ok=%v err=%v", ok, err)
	}
	second, err := GenerateRingSignature(ring, 0, keys[0], []byte("m2"), []byte("s"), nil)
	if err != nil {
		t.Fatal(err)
	}
	if !Link(sig, second) {
		t.Fatal("single-member signatures did not link")
	}
}

func TestQ0047CrossScopeDoesNotVerify(t *testing.T) {
	keys, ring := q0047TestRing(t, 3)
	message := []byte("m")
	sig, err := GenerateRingSignature(ring, 1, keys[1], message, []byte("a"), nil)
	if err != nil {
		t.Fatal(err)
	}
	if ok, err := VerifySignature(ring, message, []byte("b"), sig); err != nil || ok {
		t.Fatalf("cross-scope verification: ok=%v err=%v", ok, err)
	}
}

func TestQ0047AliasTamperRejected(t *testing.T) {
	keys, ring := q0047TestRing(t, 2)
	sig, err := GenerateRingSignature(ring, 0, keys[0], []byte("m"), []byte("s"), nil)
	if err != nil {
		t.Fatal(err)
	}
	sig.KeyImage[0] ^= 1
	if ok, err := VerifySignature(ring, []byte("m"), []byte("s"), sig); err == nil && ok {
		t.Fatal("tampered key image accepted")
	}
}

func TestQ0047SentinelErrors(t *testing.T) {
	keys, ring := q0047TestRing(t, 2)
	duplicate := [][]byte{ring[0], ring[0]}
	if _, err := GenerateRingSignature(duplicate, 0, keys[0], nil, nil, nil); !errors.Is(err, ErrDuplicateRing) {
		t.Fatalf("duplicate sentinel: %v", err)
	}

	nonCanonicalX := make([]byte, CompressedPointSize)
	nonCanonicalX[0] = 0x02
	copy(nonCanonicalX[1:], fieldPrime.Bytes())
	badRing := [][]byte{nonCanonicalX}
	if _, err := GenerateRingSignature(badRing, 0, keys[0], nil, nil, nil); !errors.Is(err, ErrInvalidPoint) {
		t.Fatalf("invalid point sentinel: %v", err)
	}

	sig, err := GenerateRingSignature(ring, 0, keys[0], []byte("m"), []byte("s"), nil)
	if err != nil {
		t.Fatal(err)
	}
	sig.C0 = append([]byte(nil), sig.C0...)
	copy(sig.C0, groupOrder.Bytes())
	sig.C = sig.C0
	sig.Challenge = sig.C0
	if _, err := VerifySignature(ring, []byte("m"), []byte("s"), sig); !errors.Is(err, ErrInvalidScalar) {
		t.Fatalf("scalar sentinel: %v", err)
	}

	sig, err = GenerateRingSignature(ring, 0, keys[0], []byte("m"), []byte("s"), nil)
	if err != nil {
		t.Fatal(err)
	}
	sig.I = []byte{0x00}
	sig.KeyImage = sig.I
	if _, err := VerifySignature(ring, []byte("m"), []byte("s"), sig); !errors.Is(err, ErrIdentityPoint) {
		t.Fatalf("identity sentinel: %v", err)
	}
}

func TestQ0047RingEncodings(t *testing.T) {
	keys, ring := q0047TestRing(t, 2)
	message, scope := []byte("m"), []byte("s")

	values := []ecdsa.PublicKey{q0047KeyValue(keys[0]), q0047KeyValue(keys[1])}
	arrays := [][33]byte{q0047PointArray(ring[0]), q0047PointArray(ring[1])}
	hexes := []string{hex.EncodeToString(ring[0]), hex.EncodeToString(ring[1])}

	for name, encoded := range map[string]any{
		"values": values,
		"arrays": arrays,
		"hex":    hexes,
	} {
		sig, err := Sign(encoded, 1, keys[0], message, scope)
		if err != nil {
			// The private key belongs to index 0, not index 1.
			sig, err = Sign(encoded, 0, keys[0], message, scope)
		}
		if err != nil {
			t.Fatalf("%s sign: %v", name, err)
		}
		if ok, err := VerifySignature(encoded, message, scope, sig); err != nil || !ok {
			t.Fatalf("%s verify: ok=%v err=%v", name, ok, err)
		}
	}
}

func q0047KeyValue(key *ecdsa.PrivateKey) ecdsa.PublicKey {
	return key.PublicKey
}

func q0047PointArray(encoded []byte) [33]byte {
	var out [33]byte
	copy(out[:], encoded)
	return out
}

func TestQ0047NilMessageKeepsScope(t *testing.T) {
	keys, ring := q0047TestRing(t, 2)
	sigA, err := Sign(ring, 0, keys[0], nil, []byte("scope-a"), nil)
	if err != nil {
		t.Fatal(err)
	}
	if ok, err := Verify(ring, nil, []byte("scope-a"), sigA); err != nil || !ok {
		t.Fatalf("nil message verify: ok=%v err=%v", ok, err)
	}
	if ok, err := Verify(ring, nil, []byte("scope-b"), sigA); err != nil || ok {
		t.Fatalf("nil message cross-scope verify: ok=%v err=%v", ok, err)
	}
	sigB, err := Sign(ring, 0, keys[0], nil, []byte("scope-b"), nil)
	if err != nil {
		t.Fatal(err)
	}
	if Link(sigA, sigB) {
		t.Fatal("nil message cross-scope linked")
	}
}

func TestQ0047SignerIndexInferred(t *testing.T) {
	keys, ring := q0047TestRing(t, 4)
	sig, err := Sign(ring, keys[2], []byte("m"), []byte("s"))
	if err != nil {
		t.Fatal(err)
	}
	if ok, err := VerifySignature(ring, []byte("m"), []byte("s"), sig); err != nil || !ok {
		t.Fatalf("inferred signer: ok=%v err=%v", ok, err)
	}
}

func TestQ0047InjectedScalars(t *testing.T) {
	keys, ring := q0047TestRing(t, 2)
	sig, err := Sign(ring, 0, keys[0], []byte("m"), []byte("s"), []*big.Int{
		big.NewInt(9),
		big.NewInt(0),
	})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(sig.S[1], scalarBytes(big.NewInt(0))) {
		t.Fatal("zero injected response not preserved")
	}
	if ok, err := VerifySignature(ring, []byte("m"), []byte("s"), sig); err != nil || !ok {
		t.Fatalf("verify injected: ok=%v err=%v", ok, err)
	}

	if _, err := Sign(ring, 0, keys[0], []byte("m"), []byte("s"), []*big.Int{
		big.NewInt(0),
		big.NewInt(9),
	}); !errors.Is(err, ErrInvalidScalar) {
		t.Fatalf("zero nonce accepted: %v", err)
	}
	if _, err := Sign(ring, 0, keys[0], []byte("m"), []byte("s"), []*big.Int{
		new(big.Int).Set(GroupOrder()),
		big.NewInt(9),
	}); !errors.Is(err, ErrInvalidScalar) {
		t.Fatalf("non-canonical nonce accepted: %v", err)
	}
}
