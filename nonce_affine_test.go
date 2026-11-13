package netsec_test

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/sha256"
	"errors"
	"fmt"
	"math/big"
	"testing"

	netsec "example.org/netsec/q0018"
)

var (
	testOrder = elliptic.P256().Params().N
	testCurve = elliptic.P256()
)

func TestSameNonceRecoversWithEveryValidSignInterpretation(t *testing.T) {
	d := scalarFromLabel("same-nonce-d")
	k := scalarFromLabel("same-nonce-k")
	z1 := digestScalarForTest(digest("same-nonce-message-1"))
	z2 := digestScalarForTest(digest("same-nonce-message-2"))

	in := netsec.Input{
		PublicKey:  publicKeyFor(d),
		Digest1:    digest("same-nonce-message-1"),
		Digest2:    digest("same-nonce-message-2"),
		Signature1: signatureFor(d, k, z1),
		Signature2: signatureFor(d, k, z2),
		A:          big.NewInt(1),
		B:          big.NewInt(0),
	}

	report, err := netsec.Analyze(in)
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	if report.Status != netsec.StatusRecovered {
		t.Fatalf("status = %s, want %s", report.Status, netsec.StatusRecovered)
	}
	if !report.Signatures[0].Verified || !report.Signatures[1].Verified {
		t.Fatal("expected both raw signatures to be verified before analysis")
	}
	if got, want := len(report.Candidates), 2; got != want {
		t.Fatalf("candidates = %d, want %d sign interpretations", got, want)
	}
	if !report.Ambiguous {
		t.Fatal("multiple valid sign interpretations must be marked ambiguous")
	}
	if report.DistinctPrivateKeys != 1 {
		t.Fatalf("distinct private keys = %d, want 1", report.DistinctPrivateKeys)
	}

	for _, candidate := range report.Candidates {
		if candidate.D.Cmp(d) != 0 {
			t.Fatalf("candidate d = %s, want %s", candidate.D, d)
		}
		if candidate.Nonce2.Cmp(candidate.Nonce1) != 0 {
			t.Fatalf("same-nonce candidate has k2 != k1: %s vs %s", candidate.Nonce1, candidate.Nonce2)
		}
		if candidate.Derivation.Outcome != netsec.AttemptCandidate {
			t.Fatalf("candidate derivation outcome = %s", candidate.Derivation.Outcome)
		}
		if candidate.Derivation.Signature1Sign != candidate.Signature1Sign ||
			candidate.Derivation.Signature2Sign != candidate.Signature2Sign {
			t.Fatal("candidate and derivation sign choices disagree")
		}
		if candidate.Derivation.EquationPrivateKey == "" ||
			candidate.Derivation.NonceEquation1 == "" ||
			candidate.Derivation.NonceEquation2 == "" ||
			candidate.Derivation.AffineEquation == "" {
			t.Fatal("candidate derivation is missing an equation")
		}
		if err := netsec.RecheckCandidate(in, candidate); err != nil {
			t.Fatalf("independent candidate recheck: %v", err)
		}
	}

	callbacks := 0
	if err := report.Recheck(in, func(candidate netsec.Candidate) error {
		callbacks++
		if candidate.D.Cmp(d) != 0 {
			t.Fatalf("callback saw d = %s, want %s", candidate.D, d)
		}
		return nil
	}); err != nil {
		t.Fatalf("report.Recheck: %v", err)
	}
	if callbacks != len(report.Candidates) {
		t.Fatalf("callback count = %d, want %d", callbacks, len(report.Candidates))
	}
}

func TestNonZeroOffsetRecoversCandidateAndDoesNotDependOnEqualR(t *testing.T) {
	d := scalarFromLabel("offset-d")
	k1 := scalarFromLabel("offset-k1")
	a := big.NewInt(7)
	b := big.NewInt(1234)
	k2 := new(big.Int).Mul(a, k1)
	k2.Add(k2, b)
	k2.Mod(k2, testOrder)
	if k2.Sign() == 0 {
		t.Fatal("fixture produced a zero second nonce")
	}

	z1 := digestScalarForTest(digest("offset-message-1"))
	z2 := digestScalarForTest(digest("offset-message-2"))
	in := netsec.Input{
		PublicKey:  publicKeyFor(d),
		Digest1:    digest("offset-message-1"),
		Digest2:    digest("offset-message-2"),
		Signature1: signatureFor(d, k1, z1),
		Signature2: signatureFor(d, k2, z2),
		A:          new(big.Int).Set(a),
		B:          new(big.Int).Set(b),
	}

	report, err := netsec.Analyze(in)
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	if report.Status != netsec.StatusRecovered || len(report.Candidates) == 0 {
		t.Fatalf("status = %s, candidates = %d", report.Status, len(report.Candidates))
	}
	if in.Signature1.R.Cmp(in.Signature2.R) == 0 {
		t.Fatal("fixture unexpectedly has equal r values")
	}

	foundExpected := false
	for _, candidate := range report.Candidates {
		if err := netsec.RecheckCandidate(in, candidate); err != nil {
			t.Fatalf("independent candidate recheck: %v", err)
		}
		if candidate.Signature1Sign == netsec.SignPositive &&
			candidate.Signature2Sign == netsec.SignPositive &&
			candidate.D.Cmp(d) == 0 &&
			candidate.Nonce1.Cmp(k1) == 0 &&
			candidate.Nonce2.Cmp(k2) == 0 {
			foundExpected = true
		}
	}
	if !foundExpected {
		t.Fatalf("did not recover expected (d, k1, k2) under (+, +): %#v", report.Candidates)
	}
}

func TestOneLowSNormalizationIsUndoneBySignChoice(t *testing.T) {
	d := scalarFromLabel("low-s-d")
	a := big.NewInt(5)
	b := big.NewInt(777)
	half := new(big.Int).Rsh(new(big.Int).Set(testOrder), 1)

	var (
		k1       *big.Int
		k2       *big.Int
		rawSig2  netsec.Signature
		message1 = digest("low-s-message-1")
		message2 = digest("low-s-message-2")
	)
	for i := 0; i < 100000; i++ {
		candidateK1 := scalarFromLabel(fmt.Sprintf("low-s-k1-%d", i))
		candidateK2 := new(big.Int).Mul(a, candidateK1)
		candidateK2.Add(candidateK2, b)
		candidateK2.Mod(candidateK2, testOrder)
		if candidateK2.Sign() == 0 {
			continue
		}
		rawSig1 := signatureFor(d, candidateK1, digestScalarForTest(message1))
		candidateSig2 := signatureFor(d, candidateK2, digestScalarForTest(message2))
		if rawSig1.S.Cmp(half) > 0 && candidateSig2.S.Cmp(half) > 0 {
			k1, k2, rawSig2 = candidateK1, candidateK2, candidateSig2
			break
		}
	}
	if k1 == nil {
		t.Fatal("could not construct a fixture with both raw s values above n/2")
	}

	sig1 := signatureFor(d, k1, digestScalarForTest(message1))
	sig2 := netsec.Signature{
		R: new(big.Int).Set(rawSig2.R),
		S: new(big.Int).Sub(testOrder, rawSig2.S),
	}
	in := netsec.Input{
		PublicKey:  publicKeyFor(d),
		Digest1:    message1,
		Digest2:    message2,
		Signature1: sig1,
		Signature2: sig2,
		A:          new(big.Int).Set(a),
		B:          new(big.Int).Set(b),
	}

	report, err := netsec.Analyze(in)
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	if report.Status != netsec.StatusRecovered || len(report.Candidates) == 0 {
		t.Fatalf("status = %s, candidates = %d", report.Status, len(report.Candidates))
	}
	if report.Signatures[0].LowS {
		t.Fatal("fixture intended signature 1 to remain high-S")
	}
	if !report.Signatures[1].LowS {
		t.Fatal("fixture intended signature 2 to be low-S")
	}

	foundExpected := false
	for _, candidate := range report.Candidates {
		if err := netsec.RecheckCandidate(in, candidate); err != nil {
			t.Fatalf("independent candidate recheck: %v", err)
		}
		if candidate.Signature1Sign == netsec.SignPositive &&
			candidate.Signature2Sign == netsec.SignNegative &&
			candidate.D.Cmp(d) == 0 &&
			candidate.Nonce1.Cmp(k1) == 0 &&
			candidate.Nonce2.Cmp(k2) == 0 {
			foundExpected = true
		}
	}
	if !foundExpected {
		t.Fatalf("low-S sign choice did not recover expected candidate: %#v", report.Candidates)
	}
}

func TestVerifiedSignaturesWithoutRelationReturnNoCandidate(t *testing.T) {
	d := scalarFromLabel("no-relation-d")
	k1 := scalarFromLabel("no-relation-k1")
	k2 := scalarFromLabel("no-relation-k2")
	z1 := digestScalarForTest(digest("no-relation-message-1"))
	z2 := digestScalarForTest(digest("no-relation-message-2"))

	in := netsec.Input{
		PublicKey:  publicKeyFor(d),
		Digest1:    digest("no-relation-message-1"),
		Digest2:    digest("no-relation-message-2"),
		Signature1: signatureFor(d, k1, z1),
		Signature2: signatureFor(d, k2, z2),
		A:          big.NewInt(13),
		B:          big.NewInt(999),
	}

	if err := netsec.VerifyInput(in); err != nil {
		t.Fatalf("both signatures should verify as supplied: %v", err)
	}
	report, err := netsec.Analyze(in)
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	if report.Status != netsec.StatusNoCandidate {
		t.Fatalf("status = %s, want %s", report.Status, netsec.StatusNoCandidate)
	}
	if len(report.Candidates) != 0 {
		t.Fatalf("candidates = %d, want 0", len(report.Candidates))
	}
	if !contains(report.Notes, "does not establish nonce safety") {
		t.Fatalf("report must explicitly avoid calling no-candidate safe: %#v", report.Notes)
	}
}

func TestSameRAloneDoesNotFabricateTheRequestedRelation(t *testing.T) {
	d := scalarFromLabel("same-r-d")
	k1 := scalarFromLabel("same-r-k1")
	k2 := new(big.Int).Sub(testOrder, k1)
	z1 := digestScalarForTest(digest("same-r-message-1"))
	z2 := digestScalarForTest(digest("same-r-message-2"))

	in := netsec.Input{
		PublicKey:  publicKeyFor(d),
		Digest1:    digest("same-r-message-1"),
		Digest2:    digest("same-r-message-2"),
		Signature1: signatureFor(d, k1, z1),
		Signature2: signatureFor(d, k2, z2),
		A:          big.NewInt(2),
		B:          big.NewInt(0),
	}
	if in.Signature1.R.Cmp(in.Signature2.R) != 0 {
		t.Fatal("fixture did not produce equal r values")
	}

	report, err := netsec.Analyze(in)
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	if report.Status != netsec.StatusNoCandidate || len(report.Candidates) != 0 {
		t.Fatalf("equal r must not be treated as the configured relation: status=%s candidates=%d",
			report.Status, len(report.Candidates))
	}
}

func TestDegenerateEquationReturnsInsufficientInformation(t *testing.T) {
	d := scalarFromLabel("degenerate-d")
	k := scalarFromLabel("degenerate-k")
	message := digest("degenerate-message")
	z := digestScalarForTest(message)
	sig := signatureFor(d, k, z)

	in := netsec.Input{
		PublicKey:  publicKeyFor(d),
		Digest1:    message,
		Digest2:    message,
		Signature1: sig,
		Signature2: netsec.Signature{R: new(big.Int).Set(sig.R), S: new(big.Int).Set(sig.S)},
		A:          big.NewInt(1),
		B:          big.NewInt(0),
	}
	if err := netsec.VerifyInput(in); err != nil {
		t.Fatalf("fixture signatures should verify: %v", err)
	}

	report, err := netsec.Analyze(in)
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	if report.Status != netsec.StatusInsufficientInformation {
		t.Fatalf("status = %s, want %s", report.Status, netsec.StatusInsufficientInformation)
	}
	if !report.Degenerate {
		t.Fatal("degenerate equations were not flagged")
	}
	if len(report.Candidates) != 0 {
		t.Fatalf("degenerate fixture returned %d candidates", len(report.Candidates))
	}
	degenerateAttempts := 0
	for _, attempt := range report.Attempts {
		if attempt.Outcome == netsec.AttemptDegenerate {
			degenerateAttempts++
			if attempt.Determinant.Sign() != 0 || attempt.RHS.Sign() != 0 {
				t.Fatalf("degenerate attempt has determinant=%s rhs=%s", attempt.Determinant, attempt.RHS)
			}
		}
	}
	if degenerateAttempts < 2 {
		t.Fatalf("degenerate attempts = %d, want at least 2 for identical signatures", degenerateAttempts)
	}
}

func TestDigestUsesECDSALeftTruncation(t *testing.T) {
	d := scalarFromLabel("long-digest-d")
	k1 := scalarFromLabel("long-digest-k1")
	a := big.NewInt(4)
	b := big.NewInt(321)
	k2 := new(big.Int).Mul(a, k1)
	k2.Add(k2, b)
	k2.Mod(k2, testOrder)

	message1 := bytes.Repeat([]byte{0xff}, 48)
	message2 := append(bytes.Repeat([]byte{0x7f}, 40), bytes.Repeat([]byte{0x11}, 8)...)
	z1 := new(big.Int).SetBytes(message1[:32])
	z2 := new(big.Int).SetBytes(message2[:32])

	in := netsec.Input{
		PublicKey:  publicKeyFor(d),
		Digest1:    message1,
		Digest2:    message2,
		Signature1: signatureFor(d, k1, z1),
		Signature2: signatureFor(d, k2, z2),
		A:          new(big.Int).Set(a),
		B:          new(big.Int).Set(b),
	}
	if err := netsec.VerifyInput(in); err != nil {
		t.Fatalf("ECDSA verification with long digest: %v", err)
	}
	report, err := netsec.Analyze(in)
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	if report.Status != netsec.StatusRecovered || len(report.Candidates) == 0 {
		t.Fatalf("status = %s, candidates = %d", report.Status, len(report.Candidates))
	}
	if report.Digests[0].Z.Cmp(z1) != 0 || report.Digests[1].Z.Cmp(z2) != 0 {
		t.Fatalf("digest scalars = [%s %s], want [%s %s]",
			report.Digests[0].Z, report.Digests[1].Z, z1, z2)
	}
	if !report.Digests[0].Truncated || !report.Digests[1].Truncated {
		t.Fatal("long digests were not marked truncated")
	}
}

func TestValidationRejectsEveryOutOfRangeOrInvalidInput(t *testing.T) {
	d := scalarFromLabel("validation-d")
	k := scalarFromLabel("validation-k")
	message1 := digest("validation-message-1")
	message2 := digest("validation-message-2")
	valid := netsec.Input{
		PublicKey:  publicKeyFor(d),
		Digest1:    message1,
		Digest2:    message2,
		Signature1: signatureFor(d, k, digestScalarForTest(message1)),
		Signature2: signatureFor(d, k, digestScalarForTest(message2)),
		A:          big.NewInt(3),
		B:          big.NewInt(7),
	}

	tests := []struct {
		name string
		edit func(*netsec.Input)
		want error
	}{
		{"r1 zero", func(in *netsec.Input) { in.Signature1.R = big.NewInt(0) }, netsec.ErrScalarOutOfRange},
		{"r1 equals n", func(in *netsec.Input) { in.Signature1.R = new(big.Int).Set(testOrder) }, netsec.ErrScalarOutOfRange},
		{"s1 zero", func(in *netsec.Input) { in.Signature1.S = big.NewInt(0) }, netsec.ErrScalarOutOfRange},
		{"s1 equals n", func(in *netsec.Input) { in.Signature1.S = new(big.Int).Set(testOrder) }, netsec.ErrScalarOutOfRange},
		{"r2 zero", func(in *netsec.Input) { in.Signature2.R = big.NewInt(0) }, netsec.ErrScalarOutOfRange},
		{"s2 equals n", func(in *netsec.Input) { in.Signature2.S = new(big.Int).Set(testOrder) }, netsec.ErrScalarOutOfRange},
		{"a negative", func(in *netsec.Input) { in.A = big.NewInt(-1) }, netsec.ErrScalarOutOfRange},
		{"a equals n", func(in *netsec.Input) { in.A = new(big.Int).Set(testOrder) }, netsec.ErrScalarOutOfRange},
		{"b negative", func(in *netsec.Input) { in.B = big.NewInt(-1) }, netsec.ErrScalarOutOfRange},
		{"b equals n", func(in *netsec.Input) { in.B = new(big.Int).Set(testOrder) }, netsec.ErrScalarOutOfRange},
		{"nil a", func(in *netsec.Input) { in.A = nil }, netsec.ErrScalarOutOfRange},
		{"nil b", func(in *netsec.Input) { in.B = nil }, netsec.ErrScalarOutOfRange},
		{"point at infinity", func(in *netsec.Input) {
			in.PublicKey = &ecdsa.PublicKey{Curve: testCurve, X: big.NewInt(0), Y: big.NewInt(0)}
		}, netsec.ErrPointAtInfinity},
		{"point not on curve", func(in *netsec.Input) {
			in.PublicKey = &ecdsa.PublicKey{Curve: testCurve, X: big.NewInt(1), Y: big.NewInt(1)}
		}, netsec.ErrPointNotOnCurve},
		{"wrong curve", func(in *netsec.Input) {
			p384 := elliptic.P384()
			x, y := p384.ScalarBaseMult(big.NewInt(42).Bytes())
			in.PublicKey = &ecdsa.PublicKey{Curve: p384, X: x, Y: y}
		}, netsec.ErrP256Required},
		{"nil coordinate", func(in *netsec.Input) {
			in.PublicKey = &ecdsa.PublicKey{Curve: testCurve, X: big.NewInt(1), Y: nil}
		}, netsec.ErrInvalidPublicKey},
		{"signature verification", func(in *netsec.Input) {
			in.Signature1.R = new(big.Int).Add(in.Signature1.R, big.NewInt(1))
			in.Signature1.R.Mod(in.Signature1.R, testOrder)
			if in.Signature1.R.Sign() == 0 {
				in.Signature1.R.SetInt64(1)
			}
		}, netsec.ErrSignatureVerification},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			in := cloneInput(valid)
			test.edit(&in)
			err := netsec.VerifyInput(in)
			if err == nil {
				t.Fatal("expected validation error")
			}
			if !errors.Is(err, test.want) {
				t.Fatalf("error = %v, want errors.Is(_, %v)", err, test.want)
			}
		})
	}

	zeroAllowed := cloneInput(valid)
	zeroAllowed.A = big.NewInt(0)
	zeroAllowed.B = big.NewInt(0)
	if err := netsec.VerifyInput(zeroAllowed); err != nil {
		t.Fatalf("a=b=0 are in range and must pass range validation: %v", err)
	}
}

func TestRecheckCandidateRejectsTamperingAndPropagatesCallbackError(t *testing.T) {
	d := scalarFromLabel("recheck-d")
	k := scalarFromLabel("recheck-k")
	message1 := digest("recheck-message-1")
	message2 := digest("recheck-message-2")
	in := netsec.Input{
		PublicKey:  publicKeyFor(d),
		Digest1:    message1,
		Digest2:    message2,
		Signature1: signatureFor(d, k, digestScalarForTest(message1)),
		Signature2: signatureFor(d, k, digestScalarForTest(message2)),
		A:          big.NewInt(1),
		B:          big.NewInt(0),
	}

	report, err := netsec.Analyze(in)
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	if len(report.Candidates) == 0 {
		t.Fatal("fixture expected candidates")
	}

	tampered := report.Candidates[0]
	tampered.D = new(big.Int).Add(tampered.D, big.NewInt(1))
	tampered.D.Mod(tampered.D, testOrder)
	if err := netsec.RecheckCandidate(in, tampered); err == nil || !errors.Is(err, netsec.ErrCandidateInvalid) {
		t.Fatalf("tampered candidate error = %v, want ErrCandidateInvalid", err)
	}

	tampered = report.Candidates[0]
	tampered.Derivation.Determinant = new(big.Int).Add(tampered.Derivation.Determinant, big.NewInt(1))
	if err := netsec.RecheckCandidate(in, tampered); err == nil || !errors.Is(err, netsec.ErrCandidateInvalid) {
		t.Fatalf("tampered derivation error = %v, want ErrCandidateInvalid", err)
	}

	callbackErr := errors.New("host rejected candidate")
	err = report.Recheck(in, func(netsec.Candidate) error { return callbackErr })
	if err == nil || !errors.Is(err, callbackErr) {
		t.Fatalf("callback error = %v, want wrapped %v", err, callbackErr)
	}
}

func signatureFor(d, k, z *big.Int) netsec.Signature {
	// This is ECDSA signing with an explicitly supplied local-fixture nonce:
	// r = x(kG) mod n and s = (z+r*d)*k^-1 mod n.
	rx, _ := testCurve.ScalarBaseMult(k.Bytes())
	r := new(big.Int).Mod(rx, testOrder)
	if r.Sign() == 0 {
		panic("fixture nonce produced r = 0")
	}
	s := new(big.Int).Mul(r, d)
	s.Add(s, z)
	s.Mul(s, new(big.Int).ModInverse(k, testOrder))
	s.Mod(s, testOrder)
	if s.Sign() == 0 {
		panic("fixture nonce produced s = 0")
	}
	return netsec.Signature{R: r, S: s}
}

func publicKeyFor(d *big.Int) *ecdsa.PublicKey {
	x, y := testCurve.ScalarBaseMult(d.Bytes())
	return &ecdsa.PublicKey{Curve: testCurve, X: x, Y: y}
}

func scalarFromLabel(label string) *big.Int {
	h := sha256.Sum256([]byte("q0018-fixture:" + label))
	value := new(big.Int).SetBytes(h[:])
	value.Mod(value, new(big.Int).Sub(testOrder, big.NewInt(1)))
	value.Add(value, big.NewInt(1))
	return value
}

func digest(label string) []byte {
	sum := sha256.Sum256([]byte(label))
	return sum[:]
}

func digestScalarForTest(message []byte) *big.Int {
	if len(message) > 32 {
		message = message[:32]
	}
	return new(big.Int).SetBytes(message)
}

func contains(values []string, want string) bool {
	for _, value := range values {
		if bytes.Contains([]byte(value), []byte(want)) {
			return true
		}
	}
	return false
}

func cloneInput(in netsec.Input) netsec.Input {
	return netsec.Input{
		PublicKey: &ecdsa.PublicKey{
			Curve: in.PublicKey.Curve,
			X:     new(big.Int).Set(in.PublicKey.X),
			Y:     new(big.Int).Set(in.PublicKey.Y),
		},
		Digest1:    append([]byte(nil), in.Digest1...),
		Digest2:    append([]byte(nil), in.Digest2...),
		Signature1: netsec.Signature{R: new(big.Int).Set(in.Signature1.R), S: new(big.Int).Set(in.Signature1.S)},
		Signature2: netsec.Signature{R: new(big.Int).Set(in.Signature2.R), S: new(big.Int).Set(in.Signature2.S)},
		A:          new(big.Int).Set(in.A),
		B:          new(big.Int).Set(in.B),
	}
}
