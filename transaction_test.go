package netsec

import (
	"bytes"
	"errors"
	"math/big"
	"testing"
)

func mustKeypair(t *testing.T, id DHGroupID) (GroupParams, KeyPair) {
	t.Helper()
	g, err := Group(id)
	if err != nil {
		t.Fatal(err)
	}
	keys, err := g.GenerateKeyPair()
	if err != nil {
		t.Fatal(err)
	}
	return g, keys
}

func negotiationMessage(pubY *big.Int, challenge []byte, hashes []HashID, groups []DHGroupID) AuthMessage {
	params := []Param{
		{Type: ParamAuthProto, Value: []byte{ProtoDHCHAP}},
		{Type: ParamHashFunction, Value: hashIDBytes(hashes)},
		{Type: ParamDHGroup, Value: groupIDBytes(groups)},
		{Type: ParamCHAPIdentifier, Value: []byte{0x21}},
		{Type: ParamChallenge, Value: challenge},
		{Type: ParamLocalName, Value: []byte("initiator.example")},
	}
	if pubY != nil {
		params = append(params[:4], append([]Param{
			{Type: ParamDHPublicValue, Value: pubY.Bytes()},
		}, params[4:]...)...)
	}
	return AuthMessage{
		Command: CmdAuthET,
		Status:  StatusInProgress,
		Proto:   ProtoDHCHAP,
		Params:  params,
	}
}

func hashIDBytes(ids []HashID) []byte {
	out := make([]byte, len(ids))
	for i, id := range ids {
		out[i] = byte(id)
	}
	return out
}

func groupIDBytes(ids []DHGroupID) []byte {
	out := make([]byte, len(ids))
	for i, id := range ids {
		out[i] = byte(id)
	}
	return out
}

func TestCompliantNegotiationAndCHAP(t *testing.T) {
	policy := strongPolicy()
	tx, err := NewTransaction(policy, "switch.example")
	if err != nil {
		t.Fatal(err)
	}

	g, initKeys := mustKeypair(t, DHGroup2048)
	initChallenge := bytes.Repeat([]byte{0x0C}, 32)
	msg := negotiationMessage(initKeys.Public, initChallenge,
		[]HashID{HashSHA512, HashSHA256, HashSHA1},
		[]DHGroupID{DHGroup2048, DHGroup1024})

	reply, err := tx.HandleNegotiation(msg)
	if err != nil {
		t.Fatalf("negotiation failed: %v", err)
	}
	if tx.State() != StateNegotiated {
		t.Fatalf("state = %v, want negotiated", tx.State())
	}
	if tx.HashID() != HashSHA512 {
		t.Fatalf("hash = %v, want SHA-512", tx.HashID())
	}
	if tx.DHGroupID() != DHGroup2048 || tx.IsNullDH() {
		t.Fatalf("group = %v, want MODP-2048", tx.DHGroupID())
	}
	if tx.PeerName() != "initiator.example" {
		t.Fatalf("peer name = %q", tx.PeerName())
	}

	// Reply must carry the negotiated selectors, a valid public value and a
	// fresh challenge.
	replyRaw, err := MarshalAuthMessage(reply)
	if err != nil {
		t.Fatal(err)
	}
	parsedReply, err := ParseAuthMessage(replyRaw)
	if err != nil {
		t.Fatal(err)
	}
	peerPubBytes, ok := parsedReply.DHPublicValue()
	if !ok {
		t.Fatal("reply missing DH public value")
	}
	responderY := new(big.Int).SetBytes(peerPubBytes)
	if err := g.ValidatePublicValue(responderY); err != nil {
		t.Fatalf("responder public value invalid: %v", err)
	}
	responderChallenge, ok := parsedReply.GetChallenge()
	if !ok || len(responderChallenge) < MinChallengeLen {
		t.Fatal("reply missing valid challenge")
	}

	// Both sides derive the same DH shared secret.
	initiatorShared := new(big.Int).Exp(responderY, initKeys.Private, g.P)
	if !bytes.Equal(initiatorShared.Bytes(), tx.RawSharedSecret()) {
		t.Fatal("shared secret mismatch between peers")
	}

	// DeriveKey binds the shared secret, then CHAP verifies the initiator.
	secret := []byte("super-secret-psk")
	derived, err := DeriveKey(tx.HashID(), []byte("fc-dhchap"), tx.RawSharedSecret())
	if err != nil {
		t.Fatal(err)
	}
	if len(derived) != 64 {
		t.Fatalf("derived key length = %d, want 64 (SHA-512)", len(derived))
	}

	// The initiator answers the responder's challenge using the responder's
	// CHAP identifier (standard CHAP: H(id || secret || challenge)).
	responderID, _ := parsedReply.Param(ParamCHAPIdentifier)
	expected, err := CHAPResponse(HashSHA512, responderID.Value, responderChallenge, secret)
	if err != nil {
		t.Fatal(err)
	}
	thirdMsg := AuthMessage{
		Command: CmdAuthET,
		Status:  StatusInProgress,
		Proto:   ProtoDHCHAP,
		Params:  []Param{{Type: ParamCHAPResponse, Value: expected}},
	}
	if err := tx.HandleReply(thirdMsg); err != nil {
		t.Fatalf("reply handling failed: %v", err)
	}
	if err := tx.VerifyPeerCHAPResponse(secret); err != nil {
		t.Fatalf("CHAP verification failed: %v", err)
	}
	if tx.State() != StateVerified {
		t.Fatalf("state = %v, want verified", tx.State())
	}

	done := tx.DoneMessage(true)
	if done.Command != CmdAuthDone || done.Status != StatusSuccess {
		t.Fatalf("bad done message: %+v", done)
	}

	// Mutual response to the initiator's challenge.
	localResp, err := tx.ComputeLocalCHAPResponse(secret)
	if err != nil {
		t.Fatal(err)
	}
	wantResp, _ := CHAPResponse(HashSHA512, []byte{0x21}, initChallenge, secret)
	if !bytes.Equal(localResp, wantResp) {
		t.Fatal("mutual CHAP response mismatch")
	}
}

func TestWrongCHAPSecretFailsTransaction(t *testing.T) {
	tx, _, _, msg := newNegotiatedTransaction(t)

	reply, err := tx.HandleNegotiation(msg)
	if err != nil {
		t.Fatal(err)
	}
	parsed, _ := ParseAuthMessage(mustMarshal(t, reply))
	id, _ := parsed.Param(ParamCHAPIdentifier)
	ch, _ := parsed.GetChallenge()
	wrong, _ := CHAPResponse(HashSHA256, id.Value, ch, []byte("attacker"))

	err = tx.HandleReply(AuthMessage{
		Command: CmdAuthET,
		Status:  StatusInProgress,
		Params:  []Param{{Type: ParamCHAPResponse, Value: wrong}},
	})
	if err != nil {
		t.Fatalf("HandleReply: %v", err)
	}
	if err := tx.VerifyPeerCHAPResponse([]byte("real-secret")); !errors.Is(err, ErrBadCredentials) {
		t.Fatalf("expected ErrBadCredentials, got %v", err)
	}
	if tx.State() != StateFailed {
		t.Fatalf("state = %v, want failed", tx.State())
	}
	if err := tx.HandleReply(AuthMessage{}); !errors.Is(err, ErrTransactionFailed) {
		t.Fatalf("expected ErrTransactionFailed after failure, got %v", err)
	}
}

func newNegotiatedTransaction(t *testing.T) (*Transaction, GroupParams, KeyPair, AuthMessage) {
	t.Helper()
	tx, err := NewTransaction(strongPolicy(), "switch")
	if err != nil {
		t.Fatal(err)
	}
	g, keys := mustKeypair(t, DHGroup2048)
	msg := negotiationMessage(keys.Public, bytes.Repeat([]byte{7}, 32),
		[]HashID{HashSHA512, HashSHA256}, []DHGroupID{DHGroup2048})
	return tx, g, keys, msg
}

func mustMarshal(t *testing.T, m AuthMessage) []byte {
	t.Helper()
	raw, err := MarshalAuthMessage(m)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}
