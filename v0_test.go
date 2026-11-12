package netsec

import (
	"errors"
	"testing"
)

// Legacy v0 envelopes parse and verify, but their digest is bound to the
// distinct ENVELOPE_TYPE_TX_V0 discriminant.
func TestV0Envelope_VerifiesAndDomainSeparated(t *testing.T) {
	priv, pub := mustKey(t, 40)
	_, dest := mustKey(t, 41)

	tx := &xdrWriter{}
	tx.v0Transaction(pub, 100, 1, dest)
	sig := signDecorated(PassphraseTestnet, EnvelopeTypeTxV0, tx.bytes(), priv)

	env := &xdrWriter{}
	env.v0Envelope(tx.bytes(), sig)

	parsed, err := ParseEnvelope(env.bytes())
	if err != nil {
		t.Fatalf("parse v0: %v", err)
	}
	if parsed.Type != EnvelopeTypeTxV0 {
		t.Fatalf("expected v0 type, got %d", parsed.Type)
	}
	if string(parsed.TransactionXDR) != string(tx.bytes()) {
		t.Fatal("v0 transaction XDR span mismatch")
	}

	v := &Verifier{
		NetworkPassphrase: PassphraseTestnet,
		Accounts:          lookupFor(staticAccount(pub, 1)),
		Threshold:         lowThreshold,
	}
	if _, err := v.VerifyEnvelope(parsed); err != nil {
		t.Fatalf("v0 envelope must verify: %v", err)
	}

	// Same tx bytes under the v1 envelope type produce a different digest,
	// so a v0 signature cannot be replayed as a v1 signature.
	dV0 := PayloadDigest(PassphraseTestnet, EnvelopeTypeTxV0, tx.bytes())
	dV1 := PayloadDigest(PassphraseTestnet, EnvelopeTypeTx, tx.bytes())
	if dV0 == dV1 {
		t.Fatal("v0 and v1 digests must be domain separated")
	}

	// Re-sign the identical transaction bytes but label them v1: must fail.
	fraud := &xdrWriter{}
	fraud.v1Envelope(tx.bytes(), sig)
	fraudEnv, err := ParseEnvelope(fraud.bytes())
	if err == nil {
		if _, err := v.VerifyEnvelope(fraudEnv); !errors.Is(err, ErrInvalidSignature) {
			t.Fatalf("v0 signature replayed on v1 must fail, got %v", err)
		}
	}
}
