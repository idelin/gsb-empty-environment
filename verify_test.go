package netsec

import (
	"errors"
	"testing"
)

// 1. Happy path: valid v1 envelope passes; hint helper preselects only.
func TestVerifyV1Envelope_HappyPath(t *testing.T) {
	priv, pub := mustKey(t, 1)
	_, dest := mustKey(t, 2)

	raw, _ := buildV1EnvelopeBytes(t, pub, dest, []DecoratedSignature{
		signDecorated(PassphraseTestnet, EnvelopeTypeTx, mustTx(t, pub, dest), priv),
	})
	env, err := ParseEnvelope(raw)
	if err != nil {
		t.Fatalf("parse envelope: %v", err)
	}

	v := &Verifier{
		NetworkPassphrase: PassphraseTestnet,
		Accounts:          lookupFor(staticAccount(pub, 1)),
		Threshold:         lowThreshold,
	}
	res, err := v.VerifyEnvelope(env)
	if err != nil || !res.Passed {
		t.Fatalf("expected pass, got err=%v result=%+v", err, res)
	}
	if res.Outer.Weight != 1 || res.Outer.TotalSignatures != 1 || len(res.Outer.UniqueSigners) != 1 {
		t.Fatalf("unexpected outcome: %+v", res.Outer)
	}

	account, _ := v.Accounts(pub)
	sigHint := env.Signatures[0].Hint
	if got := SelectByHint(account, sigHint); len(got) != 1 {
		t.Fatalf("expected one hint candidate, got %d", len(got))
	}
}

// 2. Cross-network replay: public-network signature rejected on testnet.
func TestCrossNetworkReplay_Rejected(t *testing.T) {
	priv, pub := mustKey(t, 3)
	_, dest := mustKey(t, 4)
	tx := mustTx(t, pub, dest)

	publicSig := signDecorated(PassphrasePublic, EnvelopeTypeTx, tx, priv)
	raw, _ := buildV1EnvelopeBytes(t, pub, dest, []DecoratedSignature{publicSig})
	env, err := ParseEnvelope(raw)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	v := &Verifier{
		NetworkPassphrase: PassphraseTestnet,
		Accounts:          lookupFor(staticAccount(pub, 1)),
		Threshold:         lowThreshold,
	}
	if _, err := v.VerifyEnvelope(env); !errors.Is(err, ErrInvalidSignature) {
		t.Fatalf("expected ErrInvalidSignature for cross-network replay, got %v", err)
	}

	dTest := PayloadDigest(PassphraseTestnet, EnvelopeTypeTx, tx)
	dPub := PayloadDigest(PassphrasePublic, EnvelopeTypeTx, tx)
	if dTest == dPub {
		t.Fatal("digests for different networks must differ")
	}
	// And the public-network verifier accepts the same signature.
	v.NetworkPassphrase = PassphrasePublic
	if _, err := v.VerifyEnvelope(env); err != nil {
		t.Fatalf("same signature should verify on its own network: %v", err)
	}
}

//  3. Fee bump: inner signature cannot be replayed as the outer signature;
//     a properly signed wrapper passes at both layers.
func TestFeeBump_DomainSeparationAndTwoLayerVerify(t *testing.T) {
	innerPriv, innerPub := mustKey(t, 5)
	bumpPriv, bumpPub := mustKey(t, 6)
	_, dest := mustKey(t, 7)

	tx := mustTx(t, innerPub, dest)
	innerSig := signDecorated(PassphraseTestnet, EnvelopeTypeTx, tx, innerPriv)
	innerEnv := &xdrWriter{}
	innerEnv.v1Envelope(tx, innerSig)

	// Attack: reuse the inner (type 2) signature on the fee bump outer layer.
	fraud := &xdrWriter{}
	fraud.feeBumpEnvelope(bumpPub, 200, innerEnv.bytes(), []DecoratedSignature{innerSig})
	fraudEnv, err := ParseEnvelope(fraud.bytes())
	if err != nil {
		t.Fatalf("parse fee bump: %v", err)
	}
	if fraudEnv.Type != EnvelopeTypeTxFeeBump || fraudEnv.Inner == nil {
		t.Fatal("expected parsed fee bump with inner envelope")
	}

	v := &Verifier{
		NetworkPassphrase: PassphraseTestnet,
		Accounts: lookupFor(
			staticAccount(innerPub, 1),
			staticAccount(bumpPub, 1),
		),
		Threshold: lowThreshold,
	}
	if _, err := v.VerifyEnvelope(fraudEnv); !errors.Is(err, ErrInvalidSignature) {
		t.Fatalf("expected invalid outer fee bump signature, got %v", err)
	}

	// Legit wrapper: outer signs digest over the FeeBumpTransaction bytes.
	outerSig := signDecorated(PassphraseTestnet, EnvelopeTypeTxFeeBump, fraudEnv.TransactionXDR, bumpPriv)
	good := &xdrWriter{}
	good.feeBumpEnvelope(bumpPub, 200, innerEnv.bytes(), []DecoratedSignature{outerSig})
	goodEnv, err := ParseEnvelope(good.bytes())
	if err != nil {
		t.Fatalf("parse good fee bump: %v", err)
	}
	res, err := v.VerifyEnvelope(goodEnv)
	if err != nil || !res.Passed {
		t.Fatalf("expected valid fee bump to pass, err=%v res=%+v", err, res)
	}
	if res.Inner == nil || !res.Inner.Passed || !res.Outer.Passed {
		t.Fatalf("both layers must pass: %+v", res)
	}

	// Outer layer alone insufficient: remove inner signature -> inner fails.
	noInner := &xdrWriter{}
	noInner.feeBumpEnvelope(bumpPub, 200, mustUnsignedInner(t, innerPub, dest), []DecoratedSignature{outerSig})
	noInnerEnv, err := ParseEnvelope(noInner.bytes())
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if _, err := v.VerifyEnvelope(noInnerEnv); !errors.Is(err, ErrInvalidSignature) {
		t.Fatalf("inner layer without signatures must fail, got %v", err)
	}
}

// 4. Duplicate signatures from one key count once toward the threshold.
func TestDuplicateSignature_CountedOnce(t *testing.T) {
	priv, pub := mustKey(t, 8)
	_, dest := mustKey(t, 9)
	tx := mustTx(t, pub, dest)
	sig := signDecorated(PassphraseTestnet, EnvelopeTypeTx, tx, priv)

	raw, _ := buildV1EnvelopeBytes(t, pub, dest, []DecoratedSignature{sig, sig, sig})
	env, err := ParseEnvelope(raw)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	v := &Verifier{
		NetworkPassphrase: PassphraseTestnet,
		Accounts:          lookupFor(staticAccount(pub, 2)),
		Threshold:         func(*Account) uint32 { return 3 },
	}
	res, err := v.VerifyEnvelope(env)
	if !errors.Is(err, ErrThresholdNotMet) {
		t.Fatalf("expected threshold error, got %v", err)
	}
	if res.Outer.Weight != 2 || res.Outer.DuplicateSignatures != 2 || len(res.Outer.UniqueSigners) != 1 {
		t.Fatalf("expected weight 2 and 2 duplicates, got %+v", res.Outer)
	}

	v.Threshold = lowThreshold
	res, err = v.VerifyEnvelope(env)
	if err != nil || !res.Passed {
		t.Fatalf("expected low-threshold pass, err=%v res=%+v", err, res)
	}
}

// 5. A colliding/forged hint never substitutes for ed25519 verification.
func TestForgedHint_Rejected(t *testing.T) {
	priv, pub := mustKey(t, 10)
	_, dest := mustKey(t, 11)
	tx := mustTx(t, pub, dest)
	sig := signDecorated(PassphraseTestnet, EnvelopeTypeTx, tx, priv)
	sig.Hint = [4]byte{0xDE, 0xAD, 0xBE, 0xEF}

	raw, _ := buildV1EnvelopeBytes(t, pub, dest, []DecoratedSignature{sig})
	env, err := ParseEnvelope(raw)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	v := &Verifier{
		NetworkPassphrase: PassphraseTestnet,
		Accounts:          lookupFor(staticAccount(pub, 1)),
		Threshold:         lowThreshold,
	}
	if _, err := v.VerifyEnvelope(env); !errors.Is(err, ErrInvalidSignature) {
		t.Fatalf("forged hint must fail verification, got %v", err)
	}
}

// 6. Tampering with transaction bytes after signing invalidates the signature.
func TestTamperedTransaction_Rejected(t *testing.T) {
	priv, pub := mustKey(t, 12)
	_, dest := mustKey(t, 13)
	sig := signDecorated(PassphraseTestnet, EnvelopeTypeTx, mustTx(t, pub, dest), priv)

	tamperedTx := &xdrWriter{}
	tamperedTx.v1Transaction(pub, 999, 1, dest, 50)
	env := &xdrWriter{}
	env.v1Envelope(tamperedTx.bytes(), sig)

	parsed, err := ParseEnvelope(env.bytes())
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	v := &Verifier{
		NetworkPassphrase: PassphraseTestnet,
		Accounts:          lookupFor(staticAccount(pub, 1)),
		Threshold:         lowThreshold,
	}
	if _, err := v.VerifyEnvelope(parsed); !errors.Is(err, ErrInvalidSignature) {
		t.Fatalf("tampered tx must fail, got %v", err)
	}
}

// 7. Multi-signer weights: distinct keys accumulate; threshold boundary exact.
func TestMultiSignerThresholds(t *testing.T) {
	privA, pubA := mustKey(t, 14)
	privB, pubB := mustKey(t, 15)
	_, dest := mustKey(t, 16)
	tx := mustTx(t, pubA, dest)
	sigA := signDecorated(PassphraseTestnet, EnvelopeTypeTx, tx, privA)
	sigB := signDecorated(PassphraseTestnet, EnvelopeTypeTx, tx, privB)

	account := &Account{
		Low: 1, Medium: 2, High: 3,
		Signers: []Signer{
			{PublicKey: pubA, Weight: 1},
			{PublicKey: pubB, Weight: 1},
		},
	}
	v := &Verifier{NetworkPassphrase: PassphraseTestnet, Accounts: lookupFor(account)}

	rawOne, _ := buildV1EnvelopeBytes(t, pubA, dest, []DecoratedSignature{sigA})
	envOne, _ := ParseEnvelope(rawOne)
	v.Threshold = func(a *Account) uint32 { return a.Medium }
	if _, err := v.VerifyEnvelope(envOne); !errors.Is(err, ErrThresholdNotMet) {
		t.Fatalf("single weight-1 key must miss medium threshold, got %v", err)
	}

	rawTwo, _ := buildV1EnvelopeBytes(t, pubA, dest, []DecoratedSignature{sigA, sigB})
	envTwo, _ := ParseEnvelope(rawTwo)
	res, err := v.VerifyEnvelope(envTwo)
	if err != nil || !res.Passed {
		t.Fatalf("two keys must meet medium threshold, err=%v res=%+v", err, res)
	}
	if res.Outer.Weight != 2 || len(res.Outer.UniqueSigners) != 2 {
		t.Fatalf("unexpected weight outcome: %+v", res.Outer)
	}

	// Equal-to-threshold passes (>=), just one below fails.
	v.Threshold = func(*Account) uint32 { return 2 }
	if _, err := v.VerifyEnvelope(envTwo); err != nil {
		t.Fatalf("weight equal to threshold should pass: %v", err)
	}
}

// 8. Unknown source account fails closed.
func TestUnknownSigner_FailsClosed(t *testing.T) {
	priv, pub := mustKey(t, 17)
	_, dest := mustKey(t, 18)
	raw, _ := buildV1EnvelopeBytes(t, pub, dest, []DecoratedSignature{
		signDecorated(PassphraseTestnet, EnvelopeTypeTx, mustTx(t, pub, dest), priv),
	})
	env, _ := ParseEnvelope(raw)

	v := &Verifier{
		NetworkPassphrase: PassphraseTestnet,
		Accounts:          func([]byte) (*Account, bool) { return nil, false },
		Threshold:         lowThreshold,
	}
	if _, err := v.VerifyEnvelope(env); !errors.Is(err, ErrUnknownSigner) {
		t.Fatalf("expected ErrUnknownSigner, got %v", err)
	}
}

// 9. Base64 entry point and trailing-byte rejection.
func TestParseBase64AndTrailingBytes(t *testing.T) {
	priv, pub := mustKey(t, 19)
	_, dest := mustKey(t, 20)
	raw, _ := buildV1EnvelopeBytes(t, pub, dest, []DecoratedSignature{
		signDecorated(PassphraseTestnet, EnvelopeTypeTx, mustTx(t, pub, dest), priv),
	})

	envB64 := (&xdrWriter{b: raw}).base64()
	if _, err := ParseEnvelopeBase64(envB64); err != nil {
		t.Fatalf("base64 parse: %v", err)
	}
	if _, err := ParseEnvelope(append(append([]byte{}, raw...), 0x00)); err == nil {
		t.Fatal("trailing bytes must be rejected")
	}
	if _, err := ParseEnvelopeBase64("!!!not base64!!!"); !errors.Is(err, ErrMalformedEnvelope) {
		t.Fatalf("expected malformed envelope error, got %v", err)
	}
}

func mustTx(t *testing.T, source, dest []byte) []byte {
	t.Helper()
	w := &xdrWriter{}
	w.v1Transaction(source, 100, 1, dest, 50)
	return w.bytes()
}

func mustUnsignedInner(t *testing.T, source, dest []byte) []byte {
	t.Helper()
	w := &xdrWriter{}
	w.v1Envelope(mustTx(t, source, dest))
	return w.bytes()
}

// An envelope carrying no signatures always misses any positive threshold.
func TestNoSignatures_ThresholdFails(t *testing.T) {
	_, pub := mustKey(t, 60)
	_, dest := mustKey(t, 61)

	env := &xdrWriter{}
	env.v1Envelope(mustTx(t, pub, dest))
	parsed, err := ParseEnvelope(env.bytes())
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	v := &Verifier{
		NetworkPassphrase: PassphraseTestnet,
		Accounts:          lookupFor(staticAccount(pub, 1)),
		Threshold:         lowThreshold,
	}
	res, err := v.VerifyEnvelope(parsed)
	if !errors.Is(err, ErrThresholdNotMet) {
		t.Fatalf("expected threshold failure with no signatures, got %v", err)
	}
	if res.Outer.Weight != 0 || res.Outer.Passed {
		t.Fatalf("zero signatures must yield zero weight: %+v", res.Outer)
	}
}
