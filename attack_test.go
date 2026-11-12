package netsec

import (
	"bytes"
	"errors"
	"math/big"
	"testing"
)

// TestDowngradeAttack shows that even when the attacker edits the offer lists,
// the switch never selects anything below its policy floor and never silently
// drops onto null DH.
func TestDowngradeAttack(t *testing.T) {
	t.Run("weak hashes only", func(t *testing.T) {
		tx, err := NewTransaction(strongPolicy(), "switch")
		if err != nil {
			t.Fatal(err)
		}
		_, keys := mustKeypair(t, DHGroup2048)
		msg := negotiationMessage(keys.Public, bytes.Repeat([]byte{1}, 32),
			[]HashID{HashMD5, HashSHA1}, []DHGroupID{DHGroup2048})

		if _, err := tx.HandleNegotiation(msg); !errors.Is(err, ErrNoCommonHash) {
			t.Fatalf("expected ErrNoCommonHash, got %v", err)
		}
		if tx.State() != StateFailed {
			t.Fatalf("state = %v, want failed", tx.State())
		}
	})

	t.Run("null DH forced", func(t *testing.T) {
		tx, err := NewTransaction(strongPolicy(), "switch")
		if err != nil {
			t.Fatal(err)
		}
		msg := negotiationMessage(nil, bytes.Repeat([]byte{2}, 32),
			[]HashID{HashSHA256}, []DHGroupID{DHGroupNull})

		if _, err := tx.HandleNegotiation(msg); !errors.Is(err, ErrNullDHNotAllowed) {
			t.Fatalf("expected ErrNullDHNotAllowed, got %v", err)
		}
		if tx.State() != StateFailed || tx.IsNullDH() {
			t.Fatal("null DH must not be selected without explicit policy")
		}
	})

	t.Run("surviving floor selection", func(t *testing.T) {
		policy := Policy{
			Hashes:   []HashID{HashSHA512, HashSHA256, HashSHA1},
			DHGroups: []DHGroupID{DHGroup2048, DHGroup1024},
		}
		tx, err := NewTransaction(policy, "switch")
		if err != nil {
			t.Fatal(err)
		}
		_, keys := mustKeypair(t, DHGroup1024)
		msg := negotiationMessage(keys.Public, bytes.Repeat([]byte{3}, 32),
			[]HashID{HashSHA1}, []DHGroupID{DHGroup1024})

		reply, err := tx.HandleNegotiation(msg)
		if err != nil {
			t.Fatalf("floor-level policy should still negotiate: %v", err)
		}
		if tx.HashID() != HashSHA1 || tx.DHGroupID() != DHGroup1024 {
			t.Fatalf("negotiated %v/%v", tx.HashID(), tx.DHGroupID())
		}
		if _, ok := reply.Param(ParamDHPublicValue); !ok {
			t.Fatal("real DH negotiation must include a public value")
		}
	})

	t.Run("null accepted only when explicit", func(t *testing.T) {
		policy := Policy{Hashes: []HashID{HashSHA256}, AllowNullDH: true}
		tx, err := NewTransaction(policy, "switch")
		if err != nil {
			t.Fatal(err)
		}
		msg := negotiationMessage(nil, bytes.Repeat([]byte{4}, 32),
			[]HashID{HashSHA256}, []DHGroupID{DHGroupNull})
		reply, err := tx.HandleNegotiation(msg)
		if err != nil {
			t.Fatalf("explicit null policy should negotiate: %v", err)
		}
		if !tx.IsNullDH() || tx.RawSharedSecret() != nil {
			t.Fatal("expected null DH with no shared secret")
		}
		if _, ok := reply.Param(ParamDHPublicValue); ok {
			t.Fatal("null DH reply must not carry a public value")
		}
	})
}

// TestSmallSubgroupAttack feeds the trivial subgroup values 0, 1 and p-1
// (plus an out-of-modulus value) as the peer DH public value.
func TestSmallSubgroupAttack(t *testing.T) {
	g, _ := mustKeypair(t, DHGroup2048)
	pMinus1 := new(big.Int).Sub(g.P, big.NewInt(1))
	attackerValues := []*big.Int{
		big.NewInt(0),
		big.NewInt(1),
		pMinus1,
		new(big.Int).Set(g.P),
	}
	for _, y := range attackerValues {
		tx, err := NewTransaction(strongPolicy(), "switch")
		if err != nil {
			t.Fatal(err)
		}
		msg := negotiationMessage(y, bytes.Repeat([]byte{5}, 32),
			[]HashID{HashSHA256}, []DHGroupID{DHGroup2048})

		if _, err := tx.HandleNegotiation(msg); !errors.Is(err, ErrInvalidDHPublicValue) {
			t.Fatalf("value %v: expected ErrInvalidDHPublicValue, got %v", y, err)
		}
		if tx.State() != StateFailed || tx.RawSharedSecret() != nil {
			t.Fatalf("value %v: no shared secret may be produced", y)
		}
	}
}

// TestChallengeReuse verifies a challenge cannot be replayed within one
// transaction and the minimum length is enforced.
func TestChallengeReuse(t *testing.T) {
	t.Run("short challenge rejected", func(t *testing.T) {
		tx, _, keys, _ := newNegotiatedTransaction(t)
		msg := negotiationMessage(keys.Public, bytes.Repeat([]byte{1}, MinChallengeLen-1),
			[]HashID{HashSHA256}, []DHGroupID{DHGroup2048})
		if _, err := tx.HandleNegotiation(msg); !errors.Is(err, ErrChallengeTooShort) {
			t.Fatalf("expected ErrChallengeTooShort, got %v", err)
		}
	})

	t.Run("challenge reused in reply", func(t *testing.T) {
		tx, _, keys, _ := newNegotiatedTransaction(t)
		initialChallenge := bytes.Repeat([]byte{0x0A}, 32)
		msg := negotiationMessage(keys.Public, initialChallenge,
			[]HashID{HashSHA512, HashSHA256}, []DHGroupID{DHGroup2048})
		// First negotiation uses the challenge successfully.
		reply, err := tx.HandleNegotiation(msg)
		if err != nil {
			t.Fatal(err)
		}
		parsed, _ := ParseAuthMessage(mustMarshal(t, reply))
		responderChallenge, _ := parsed.GetChallenge()

		resp, _ := CHAPResponse(HashSHA256, []byte{1}, responderChallenge, []byte("psk"))
		// Replay the initiator's own challenge inside the third message.
		replay := AuthMessage{
			Command: CmdAuthET,
			Status:  StatusInProgress,
			Params: []Param{
				{Type: ParamChallenge, Value: initialChallenge},
				{Type: ParamCHAPResponse, Value: resp},
			},
		}
		err = tx.HandleReply(replay)
		if !errors.Is(err, ErrChallengeReused) {
			t.Fatalf("expected ErrChallengeReused, got %v", err)
		}
		if tx.State() != StateFailed {
			t.Fatal("replay must terminate the transaction")
		}
	})

	t.Run("local challenge generation never repeats", func(t *testing.T) {
		tx, err := NewTransaction(strongPolicy(), "switch")
		if err != nil {
			t.Fatal(err)
		}
		seen := map[string]bool{}
		for i := 0; i < 16; i++ {
			ch, err := tx.GenerateChallenge()
			if err != nil {
				t.Fatal(err)
			}
			if seen[string(ch)] {
				t.Fatal("generated challenge repeated")
			}
			seen[string(ch)] = true
			if len(ch) != challengeLen {
				t.Fatalf("challenge length = %d", len(ch))
			}
		}
	})
}

// TestNoCHAPFallback guarantees a negotiation failure cannot be retried as
// plain CHAP: the transaction is terminal and offers no CHAP-only path.
func TestNoCHAPFallback(t *testing.T) {
	tx, _, keys, _ := newNegotiatedTransaction(t)
	msg := negotiationMessage(keys.Public, bytes.Repeat([]byte{9}, 32),
		[]HashID{HashSHA256}, []DHGroupID{DHGroupNull})

	if _, err := tx.HandleNegotiation(msg); !errors.Is(err, ErrNullDHNotAllowed) {
		t.Fatalf("expected ErrNullDHNotAllowed, got %v", err)
	}

	// Any continuation attempt is rejected; there is no API that restarts the
	// exchange in a weaker mode.
	if err := tx.HandleReply(AuthMessage{}); !errors.Is(err, ErrTransactionFailed) {
		t.Fatalf("expected ErrTransactionFailed, got %v", err)
	}
	if _, err := tx.HandleNegotiation(msg); !errors.Is(err, ErrTransactionFailed) {
		t.Fatalf("retry must fail with ErrTransactionFailed, got %v", err)
	}
	if tx.State() != StateFailed {
		t.Fatalf("state = %v, want failed", tx.State())
	}
}
