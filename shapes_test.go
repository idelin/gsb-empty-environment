package netsec

import "testing"

// Complex transaction bodies must be skipped exactly: wrong span handling
// would corrupt signature-list parsing or leave trailing bytes.
func TestParseComplexTransactionBody(t *testing.T) {
	priv, pub := mustKey(t, 30)
	_, dest := mustKey(t, 31)

	tx := &xdrWriter{}
	tx.v1TransactionMulti(pub, 1, dest)

	sig := signDecorated(PassphraseTestnet, EnvelopeTypeTx, tx.bytes(), priv)
	env := &xdrWriter{}
	env.v1Envelope(tx.bytes(), sig)

	parsed, err := ParseEnvelope(env.bytes())
	if err != nil {
		t.Fatalf("parse complex envelope: %v", err)
	}
	if string(parsed.TransactionXDR) != string(tx.bytes()) {
		t.Fatal("captured transaction XDR span mismatch")
	}

	v := &Verifier{
		NetworkPassphrase: PassphraseTestnet,
		Accounts:          lookupFor(staticAccount(pub, 1)),
		Threshold:         lowThreshold,
	}
	res, err := v.VerifyEnvelope(parsed)
	if err != nil || !res.Passed {
		t.Fatalf("complex envelope must verify: err=%v res=%+v", err, res)
	}
}

// An unknown operation discriminant must fail closed rather than be skipped.
func TestUnknownOperationDiscriminant_Rejected(t *testing.T) {
	_, pub := mustKey(t, 32)

	tx := &xdrWriter{}
	tx.u32(0)
	tx.u32(0)
	tx.fixed(pub)
	tx.u32(100)
	tx.u64(1)
	tx.u32(0) // PRECOND_NONE
	tx.u32(0) // MEMO_NONE
	tx.u32(1) // one operation
	tx.boolean(false)
	tx.u32(99) // unsupported operation type
	tx.u32(0)
	tx.u32(0)

	env := &xdrWriter{}
	env.v1Envelope(tx.bytes())

	if _, err := ParseEnvelope(env.bytes()); err == nil {
		t.Fatal("unknown operation discriminant must be rejected")
	}
}
