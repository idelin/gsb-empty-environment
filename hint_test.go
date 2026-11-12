package netsec

import (
	"bytes"
	"testing"
)

// Two distinct keys that happen to share the same 4-byte hint. The hint must
// only shortlist candidates; full ed25519 verification still decides.
func TestHintCollision_FullVerificationStillRequired(t *testing.T) {
	privA, pubA := mustKey(t, 50)
	_, pubB := mustKey(t, 51)

	// Force identical trailing 4 bytes without changing the key material used
	// for signing: only the account table copies are adjusted for the hint.
	copy(pubB[28:], pubA[28:])

	_, dest := mustKey(t, 52)
	tx := mustTx(t, pubA, dest)

	// Signature produced by A.
	sigA := signDecorated(PassphraseTestnet, EnvelopeTypeTx, tx, privA)

	account := &Account{
		Low: 1,
		Signers: []Signer{
			{PublicKey: pubA, Weight: 1},
			{PublicKey: pubB, Weight: 1},
		},
	}
	v := &Verifier{
		NetworkPassphrase: PassphraseTestnet,
		Accounts:          lookupFor(account),
		Threshold:         lowThreshold,
	}

	raw, _ := buildV1EnvelopeBytes(t, pubA, dest, []DecoratedSignature{sigA})
	env, _ := ParseEnvelope(raw)
	res, err := v.VerifyEnvelope(env)
	if err != nil || !res.Passed {
		t.Fatalf("signature by A must verify despite shared hint: %v", err)
	}
	if !bytes.Equal(res.Outer.UniqueSigners[0], pubA) {
		t.Fatal("verifier must attribute the signature to key A, not the colliding key B")
	}

	// SelectByHint returns both, demonstrating preselection is not verification.
	if got := SelectByHint(account, sigA.Hint); len(got) != 2 {
		t.Fatalf("expected two hint candidates under collision, got %d", len(got))
	}
}
