package threshold_test

import (
	"errors"
	"testing"

	tc "formal.local/w27-9f4c534a76"
)

func TestProofOfPossessionDomainSeparation(t *testing.T) {
	w := newDKGWorld(t, 2, 3)
	id := w.identities[0].ID
	msg := w.broadcastRound1(id)

	err := tc.VerifyProofOfPossession(
		w.group, []byte("threshold-test-context"), id, msg.Commitments[0], msg.PoP,
	)
	if err != nil {
		t.Fatalf("valid PoP rejected: %v", err)
	}

	wrongContext := tc.VerifyProofOfPossession(
		w.group, []byte("other-context"), id, msg.Commitments[0], msg.PoP,
	)
	if !errors.Is(wrongContext, tc.ErrProofOfPossession) {
		t.Fatalf("wrong context error=%v", wrongContext)
	}

	wrongID := tc.VerifyProofOfPossession(
		w.group, []byte("threshold-test-context"), "someone-else", msg.Commitments[0], msg.PoP,
	)
	if !errors.Is(wrongID, tc.ErrProofOfPossession) {
		t.Fatalf("wrong participant ID error=%v", wrongID)
	}

	other, err := w.group.RandomScalar(nil)
	if err != nil {
		t.Fatal(err)
	}
	wrongC0 := w.group.Encode(w.group.ScalarBaseMult(other))
	wrongKey := tc.VerifyProofOfPossession(
		w.group, []byte("threshold-test-context"), id, wrongC0, msg.PoP,
	)
	if !errors.Is(wrongKey, tc.ErrProofOfPossession) {
		t.Fatalf("wrong C0 error=%v", wrongKey)
	}
}
