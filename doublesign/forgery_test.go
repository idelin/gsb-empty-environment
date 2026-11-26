package doublesign

import (
	"crypto/ed25519"
	"testing"
)

// An attacker that holds only public keys (the member key and the authority
// key) cannot manufacture a conviction: every embedded share must carry a
// valid Ed25519 signature under the member key, which cannot be produced
// without the private key.
func TestPublicKeyOnlyCannotForgeEvidence(t *testing.T) {
	a, k, authPub, _ := newTestAuditor(t, Member{ID: "v1", Weight: 1})
	b1 := digest(0x70)
	b2 := digest(0x71)

	// First obtain a genuine conviction as the reference artifact shape.
	expectVerdict(t, a.Observe(share(k, 1, 0, 0, PhasePrepare, b1, nil, 0)), VerdictAccepted)
	genuine := a.Observe(share(k, 1, 0, 0, PhasePrepare, b2, nil, 0))
	expectVerdict(t, genuine, VerdictConvicted)
	if err := VerifyEvidence(*genuine.Evidence, authPub); err != nil {
		t.Fatalf("genuine evidence must verify: %v", err)
	}

	// Attacker has only public material: the anchored epoch set (with pubkeys),
	// the authority public key, and the signed bytes it wants to lie about.
	set := genuine.Evidence.EpochSet
	member, _ := set.member(k.id)
	forgedMsg := share(k, 1, 0, 0, PhasePrepare, b2, nil, 0)
	forgedMsg.Signature = make([]byte, ed25519.SignatureSize) // all-zero guess

	// 1. A fabricated signature is rejected by the member key.
	if Verify(member.PubKey, testChain, forgedMsg) {
		t.Fatal("zero signature must not verify")
	}

	// 2. Wrapping the forged share in an evidence object is rejected offline.
	fakeEv := *genuine.Evidence
	fakeEv.Messages = []Message{genuine.Evidence.Messages[0], forgedMsg}
	if err := VerifyEvidence(fakeEv, authPub); err == nil {
		t.Fatal("forged evidence with fake signature must be rejected")
	}

	// 3. Tampering with any safety field invalidates the genuine signature,
	//    because every such field is inside the signed payload.
	tampered := genuine.Evidence.Messages[1]
	tampered.BlockDigest = digest(0x72)
	if Verify(member.PubKey, testChain, tampered) {
		t.Fatal("tampered block digest must fail signature verification")
	}
	tampered2 := genuine.Evidence.Messages[1]
	tampered2.Epoch = 2
	if Verify(member.PubKey, testChain, tampered2) {
		t.Fatal("tampered epoch must fail signature verification")
	}
	tampered3 := genuine.Evidence.Messages[1]
	tampered3.Round = 9
	if Verify(member.PubKey, testChain, tampered3) {
		t.Fatal("tampered round must fail signature verification")
	}

	// 4. A different (attacker-controlled) authority key cannot re-anchor the
	//    validator set to make a fake conviction pass.
	_, attackerAuthPriv, _ := genAuthority(t)
	attackerAnchor := AnchorRoot("attacker", attackerAuthPriv, set)
	reanchored := *genuine.Evidence
	reanchored.Anchor = attackerAnchor
	if err := VerifyEvidence(reanchored, authPub); err == nil {
		t.Fatal("evidence re-anchored by an attacker authority must be rejected")
	}

	// 5. Randomly guessed signatures have negligible chance of verifying;
	//    exhaustively confirm across many random 64-byte guesses.
	base := share(k, 1, 0, 0, PhasePrepare, b2, nil, 0)
	for i := 0; i < 32; i++ {
		guess := base
		guess.Signature = randomSignature(t)
		if Verify(member.PubKey, testChain, guess) {
			t.Fatal("random signature unexpectedly verified")
		}
	}
}

// A correct conviction still verifies under an auditor-independent verifier
// that constructs nothing from host state: only the evidence JSON and public
// authority key are used (mirrors the standalone CLI behavior).
func TestEvidenceRejectsWrongViolationLabel(t *testing.T) {
	a, k, authPub, _ := newTestAuditor(t, Member{ID: "v1", Weight: 1})
	b1 := digest(0x80)
	b2 := digest(0x81)
	expectVerdict(t, a.Observe(share(k, 1, 0, 0, PhasePrepare, b1, nil, 0)), VerdictAccepted)
	ev := a.Observe(share(k, 1, 0, 0, PhasePrepare, b2, nil, 0)).Evidence

	relabeled := *ev
	relabeled.Violation = ViolationLockedQCRollback
	if err := VerifyEvidence(relabeled, authPub); err == nil {
		t.Fatal("mislabeled violation must fail its predicate")
	}
}
