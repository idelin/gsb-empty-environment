package spake2plus

import (
	"bytes"
	"encoding/hex"
	"errors"
	"math/big"
	"strings"
	"testing"
)

var (
	testW0 = mustHex("8f3a2b1c9d4e5f60718293a4b5c6d7e8f90112233445566778899aabbccddeef")
	testW1 = mustHex("1234567890abcdef1234567890abcdef1234567890abcdef1234567890abcdef")

	testContext    = []byte("handshake-service/v1")
	testProverID   = []byte("client-7")
	testVerifierID = []byte("server-3")
)

func mustHex(s string) []byte {
	b, err := hex.DecodeString(s)
	if err != nil {
		panic(err)
	}
	return b
}

func testConfig() VerifierConfig {
	w1 := new(big.Int).SetBytes(testW1)
	return VerifierConfig{
		Context:    testContext,
		ProverID:   testProverID,
		VerifierID: testVerifierID,
		W0:         testW0,
		L:          encodePoint(scalarBaseMult(w1)),
	}
}

// testProver simulates the client side of the protocol for tests.
type testProver struct {
	context, idP, idV []byte
	w0, w1, x         *big.Int
	shareP            []byte
}

func newTestProver(t *testing.T, context, idP, idV, w0Bytes, w1Bytes []byte) *testProver {
	t.Helper()
	x, err := randomScalar()
	if err != nil {
		t.Fatal(err)
	}
	p := &testProver{
		context: context,
		idP:     idP,
		idV:     idV,
		w0:      new(big.Int).SetBytes(w0Bytes),
		w1:      new(big.Int).SetBytes(w1Bytes),
		x:       x,
	}
	share := addPoints(scalarBaseMult(x), scalarMult(pointM, p.w0))
	p.shareP = encodePoint(share)
	return p
}

// confirm processes the verifier's share and returns the prover's
// confirmation MAC, the confirmation MAC it expects back, and the session
// key it derives.
func (p *testProver) confirm(shareV []byte) (confirmP, expectConfirmV, sharedKey []byte) {
	sv, err := decodePoint(shareV)
	if err != nil {
		panic(err)
	}
	s := subPoints(sv, scalarMult(pointN, p.w0))
	zEl := scalarMult(s, p.x)
	vEl := scalarMult(s, p.w1)
	kConfirmP, kConfirmV, kShared := deriveKeys(p.context, p.idP, p.idV, p.shareP, shareV, zEl, vEl, paddedScalar(p.w0))
	return macTag(kConfirmP, shareV), macTag(kConfirmV, p.shareP), kShared
}

func TestValidHandshake(t *testing.T) {
	v, err := NewVerifier(testConfig())
	if err != nil {
		t.Fatal(err)
	}
	shareV, err := v.Start()
	if err != nil {
		t.Fatal(err)
	}
	if len(shareV) != ShareLength {
		t.Fatalf("shareV length = %d, want %d", len(shareV), ShareLength)
	}
	if _, err := decodePoint(shareV); err != nil {
		t.Fatalf("shareV is not a valid point: %v", err)
	}

	prover := newTestProver(t, testContext, testProverID, testVerifierID, testW0, testW1)
	confirmP, wantConfirmV, wantKey := prover.confirm(shareV)

	confirmV, sharedKey, err := v.Finish(prover.shareP, confirmP)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(confirmV, wantConfirmV) {
		t.Error("confirmV does not match the prover's expectation")
	}
	if !bytes.Equal(sharedKey, wantKey) {
		t.Error("session keys differ")
	}
	if len(sharedKey) != SharedKeyLength {
		t.Errorf("session key length = %d, want %d", len(sharedKey), SharedKeyLength)
	}
	if bytes.Equal(confirmP, confirmV) {
		t.Error("directional confirmation MACs must differ")
	}
}

func TestRejectsInvalidShares(t *testing.T) {
	w0 := new(big.Int).SetBytes(testW0)

	// Uncompressed base point with the y coordinate incremented: off curve.
	g := basePoint()
	offCurve := make([]byte, 65)
	offCurve[0] = 4
	g.x.FillBytes(offCurve[1:33])
	new(big.Int).Add(g.y, big.NewInt(1)).FillBytes(offCurve[33:65])

	// Compressed point with x == p.
	xTooBig := make([]byte, 33)
	xTooBig[0] = 2
	p256Prime.FillBytes(xTooBig[1:])

	// shareP = w0*M: unmasks to the identity, making Z independent of y.
	degenerate := encodePoint(scalarMult(pointM, w0))

	cases := map[string]struct {
		share   []byte
		wantErr error
	}{
		"empty":               {nil, ErrInvalidShare},
		"identity encoding":   {[]byte{0x00}, ErrInvalidShare},
		"truncated":           {[]byte{0x02}, ErrInvalidShare},
		"bad prefix":          {append([]byte{0x07}, make([]byte, 64)...), ErrInvalidShare},
		"x too large":         {xTooBig, ErrInvalidShare},
		"off curve":           {offCurve, ErrInvalidShare},
		"unmasks to identity": {degenerate, ErrDegenerateShare},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			v, err := NewVerifier(testConfig())
			if err != nil {
				t.Fatal(err)
			}
			if _, err := v.Start(); err != nil {
				t.Fatal(err)
			}
			confirmV, sharedKey, err := v.Finish(tc.share, make([]byte, ConfirmMACLength))
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("Finish error = %v, want %v", err, tc.wantErr)
			}
			if confirmV != nil || sharedKey != nil {
				t.Error("no key material may be released on failure")
			}
		})
	}
}

func TestRejectsWrongIdentity(t *testing.T) {
	cases := map[string]struct {
		context []byte
		idP     []byte
		idV     []byte
	}{
		"wrong context":     {[]byte("other-protocol/v9"), testProverID, testVerifierID},
		"wrong prover id":   {testContext, []byte("client-8"), testVerifierID},
		"wrong verifier id": {testContext, testProverID, []byte("server-4")},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			v, err := NewVerifier(testConfig())
			if err != nil {
				t.Fatal(err)
			}
			shareV, err := v.Start()
			if err != nil {
				t.Fatal(err)
			}
			// The prover derives its MAC over a transcript that binds a
			// different context or identity than the verifier's.
			prover := newTestProver(t, tc.context, tc.idP, tc.idV, testW0, testW1)
			confirmP, _, _ := prover.confirm(shareV)

			confirmV, sharedKey, err := v.Finish(prover.shareP, confirmP)
			if !errors.Is(err, ErrConfirmation) {
				t.Fatalf("Finish error = %v, want %v", err, ErrConfirmation)
			}
			if confirmV != nil || sharedKey != nil {
				t.Error("no key material may be released on failure")
			}
		})
	}
}

func TestRejectsReflectedMAC(t *testing.T) {
	v, err := NewVerifier(testConfig())
	if err != nil {
		t.Fatal(err)
	}
	shareV, err := v.Start()
	if err != nil {
		t.Fatal(err)
	}

	// An attacker who observed a verifier's confirmV cannot replay it as
	// confirmP: the directions use independent keys and MAC different
	// messages.
	prover := newTestProver(t, testContext, testProverID, testVerifierID, testW0, testW1)
	_, reflectedConfirmV, _ := prover.confirm(shareV)

	confirmV, sharedKey, err := v.Finish(prover.shareP, reflectedConfirmV)
	if !errors.Is(err, ErrConfirmation) {
		t.Fatalf("Finish error = %v, want %v", err, ErrConfirmation)
	}
	if confirmV != nil || sharedKey != nil {
		t.Error("no key material may be released on failure")
	}
}

func TestRejectsWrongPassword(t *testing.T) {
	v, err := NewVerifier(testConfig())
	if err != nil {
		t.Fatal(err)
	}
	shareV, err := v.Start()
	if err != nil {
		t.Fatal(err)
	}

	wrongW0 := new(big.Int).Add(new(big.Int).SetBytes(testW0), big.NewInt(1))
	prover := newTestProver(t, testContext, testProverID, testVerifierID, wrongW0.Bytes(), testW1)
	confirmP, _, _ := prover.confirm(shareV)

	if _, _, err := v.Finish(prover.shareP, confirmP); !errors.Is(err, ErrConfirmation) {
		t.Fatalf("Finish error = %v, want %v", err, ErrConfirmation)
	}
}

func TestVerifierStateMachine(t *testing.T) {
	v, err := NewVerifier(testConfig())
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := v.Finish(make([]byte, ShareLength), nil); !errors.Is(err, ErrOutOfOrder) {
		t.Fatalf("Finish before Start: error = %v, want %v", err, ErrOutOfOrder)
	}
	if _, err := v.Start(); err != nil {
		t.Fatal(err)
	}
	if _, err := v.Start(); !errors.Is(err, ErrOutOfOrder) {
		t.Fatalf("second Start: error = %v, want %v", err, ErrOutOfOrder)
	}
	if _, _, err := v.Finish(nil, nil); !errors.Is(err, ErrInvalidShare) {
		t.Fatalf("Finish with bad share: error = %v, want %v", err, ErrInvalidShare)
	}
	if _, _, err := v.Finish(nil, nil); !errors.Is(err, ErrOutOfOrder) {
		t.Fatalf("second Finish: error = %v, want %v", err, ErrOutOfOrder)
	}
}

func TestRejectsBadRegistrationRecord(t *testing.T) {
	cfg := testConfig()
	cfg.W0 = nil
	if _, err := NewVerifier(cfg); !errors.Is(err, ErrInvalidW0) {
		t.Fatalf("zero w0: error = %v, want %v", err, ErrInvalidW0)
	}

	cfg = testConfig()
	cfg.W0 = paddedScalar(p256Order)
	if _, err := NewVerifier(cfg); !errors.Is(err, ErrInvalidW0) {
		t.Fatalf("w0 == n: error = %v, want %v", err, ErrInvalidW0)
	}

	cfg = testConfig()
	cfg.L = []byte{0x00}
	if _, err := NewVerifier(cfg); !errors.Is(err, ErrInvalidL) {
		t.Fatalf("identity L: error = %v, want %v", err, ErrInvalidL)
	}
}

func TestSuiteConstants(t *testing.T) {
	if isInfinity(pointM) || isInfinity(pointN) {
		t.Fatal("M and N must be non-identity")
	}
	if !p256.IsOnCurve(pointM.x, pointM.y) || !p256.IsOnCurve(pointN.x, pointN.y) {
		t.Fatal("M and N must be on P-256")
	}
	if pointM.x.Cmp(pointN.x) == 0 {
		t.Fatal("M and N must be distinct")
	}
	// The points bound into the transcript must be exactly the RFC 9383
	// Section 4 constants (given there as SEC 1 compressed points).
	for name, tc := range map[string]struct {
		p          point
		compressed string
	}{
		"M": {pointM, "02886e2f97ace46e55ba9dd7242579f2993b64e16ef3dcab95afd497333d8fa12f"},
		"N": {pointN, "03d8bbd6c639c62937b04d997f38c3770719c629d7014d49a24b4f98baa1292b49"},
	} {
		want, err := decodePoint(mustHex(tc.compressed))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if tc.p.x.Cmp(want.x) != 0 || tc.p.y.Cmp(want.y) != 0 {
			t.Errorf("%s does not match the RFC 9383 constant", name)
		}
	}
}

// TestHKDFRFC5869 checks the key derivation against RFC 5869 Test Case 1.
func TestHKDFRFC5869(t *testing.T) {
	ikm := bytes.Repeat([]byte{0x0b}, 22)
	salt := mustHex("000102030405060708090a0b0c")
	info := mustHex("f0f1f2f3f4f5f6f7f8f9")
	wantPRK := "077709362c2e32df0ddc3f0dc47bba6390b6c73bb50f9c3122ec844ad7c2b3e5"
	wantOKM := "3cb25f25faacd57a90434f64d0362f2a2d2d0a90cf1a5a4c5db02d56ecc4c5bf34007208d5b887185865"

	prk := hkdfExtract(salt, ikm)
	if got := hex.EncodeToString(prk); got != wantPRK {
		t.Errorf("PRK = %s, want %s", got, wantPRK)
	}
	okm := hkdfExpand(prk, info, 42)
	if got := hex.EncodeToString(okm); got != wantOKM {
		t.Errorf("OKM = %s, want %s", got, wantOKM)
	}
}

// TestRegistrationVectorRFC9383 checks L = w1*B against the registration
// record of the RFC 9383 Appendix C.1 test vector.
func TestRegistrationVectorRFC9383(t *testing.T) {
	w1 := new(big.Int).SetBytes(mustHex("7e945f34d78785b8a3ef44d0df5a1a97d6b3b460409a345ca7830387a74b1dba"))
	l := scalarBaseMult(w1)
	xHex := hex.EncodeToString(paddedScalar(l.x))
	yHex := hex.EncodeToString(paddedScalar(l.y))
	// Fragments of the L = 0x04... test vector in RFC 9383 Appendix C.1:
	// the x coordinate ends with "...f13ae3e2...854c7fc00f" and the y
	// coordinate is "00bfedcb...5966efd" in full.
	if !strings.HasSuffix(xHex, "f13ae3e225efbe91ea487425854c7fc00f") {
		t.Errorf("L.x = %s", xHex)
	}
	if yHex != "00bfedcbd09b2400142d40a14f2064ef31dfaa903b91d1faea7093d835966efd" {
		t.Errorf("L.y = %s", yHex)
	}
}
