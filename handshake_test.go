package netsec

import (
	"bytes"
	"crypto/ed25519"
	"crypto/elliptic"
	"encoding/binary"
	"errors"
	"testing"
)

func handshakeWithEphemerals(t *testing.T, a, b *Party, sessionID []byte, mode Mode, ephemeralA, ephemeralB *EphemeralKey) (*Initiator, *Responder, KeySchedule) {
	t.Helper()
	initiator, hello, err := NewInitiator(a, InitiatorConfig{
		Mode: mode, SessionID: sessionID, Peer: b.ID.Name, Ephemeral: ephemeralA,
	})
	if err != nil {
		t.Fatalf("NewInitiator: %v", err)
	}
	responder, response, err := NewResponder(b, ResponderConfig{
		Mode: mode, Ephemeral: ephemeralB,
	}, hello)
	if err != nil {
		t.Fatalf("NewResponder: %v", err)
	}
	auth, err := initiator.HandleResponse(response)
	if err != nil {
		t.Fatalf("HandleResponse: %v", err)
	}
	if err := responder.HandleInitiatorAuth(auth); err != nil {
		t.Fatalf("HandleInitiatorAuth: %v", err)
	}
	confirmA, err := initiator.SendConfirmA()
	if err != nil {
		t.Fatalf("SendConfirmA: %v", err)
	}
	if err := responder.HandleInitiatorConfirm(confirmA); err != nil {
		t.Fatalf("HandleInitiatorConfirm: %v", err)
	}
	confirmB, err := responder.SendResponderConfirm()
	if err != nil {
		t.Fatalf("SendResponderConfirm: %v", err)
	}
	if err := initiator.VerifyResponderConfirm(confirmB); err != nil {
		t.Fatalf("VerifyResponderConfirm: %v", err)
	}
	aKeys, err := initiator.Keys()
	if err != nil {
		t.Fatalf("initiator keys: %v", err)
	}
	bKeys, err := responder.Keys()
	if err != nil {
		t.Fatalf("responder keys: %v", err)
	}
	if !aKeys.Equal(bKeys) {
		t.Fatal("endpoints derived different keys")
	}
	return initiator, responder, aKeys
}

func sessionEphemerals(t *testing.T, aScalar, bScalar byte) (*EphemeralKey, *EphemeralKey) {
	t.Helper()
	a, err := EphemeralFromScalar([]byte{aScalar})
	if err != nil {
		t.Fatal(err)
	}
	b, err := EphemeralFromScalar([]byte{bScalar})
	if err != nil {
		t.Fatal(err)
	}
	return a, b
}

func TestHardenedHandshakeAndSessionBinding(t *testing.T) {
	_, parties := testParties(t, "alice", "bob")
	alice, bob := parties["alice"], parties["bob"]
	aEphemeral, bEphemeral := sessionEphemerals(t, 1, 2)
	_, _, keys := handshakeWithEphemerals(t, alice, bob, []byte("session-one"), ModeHardened, aEphemeral, bEphemeral)
	if keys.SendA == keys.SendB || keys.SendA == keys.Confirm || keys.SendB == keys.Confirm {
		t.Fatal("directional and confirmation keys re-used the same HKDF output")
	}
	if !alice.Sessions.Used([]byte("session-one")) || !bob.Sessions.Used([]byte("session-one")) {
		t.Fatal("session ID was not consumed on both hosts")
	}

	_, parties2 := testParties(t, "alice", "bob")
	a2, b2 := parties2["alice"], parties2["bob"]
	aEphemeral2, bEphemeral2 := sessionEphemerals(t, 1, 2)
	_, _, keys2 := handshakeWithEphemerals(t, a2, b2, []byte("session-two"), ModeHardened, aEphemeral2, bEphemeral2)
	if keys.Equal(keys2) {
		t.Fatal("changing the session ID did not change the key schedule")
	}

	if _, err := RunHandshake(alice, bob, []byte("session-one"), ModeHardened); !errors.Is(err, ErrSessionReuse) {
		t.Fatalf("reused session error = %v", err)
	}
}

func TestWeakHandshakeCompletesHonestly(t *testing.T) {
	_, parties := testParties(t, "alice", "bob")
	keys, err := RunHandshake(parties["alice"], parties["bob"], []byte("weak-honest"), ModeWeak)
	if err != nil {
		t.Fatal(err)
	}
	if keys.SendA == keys.SendB || keys.Confirm == keys.SendA {
		t.Fatal("weak key schedule did not separate its outputs")
	}
}

func TestHardenedRejectsOuterInitiatorRelabel(t *testing.T) {
	_, parties := testParties(t, "alice", "bob", "carol")
	alice, bob, carol := parties["alice"], parties["bob"], parties["carol"]
	_ = carol

	initiator, hello, err := NewInitiator(alice, InitiatorConfig{
		Mode: ModeHardened, SessionID: []byte("relabel-hardened"), Peer: bob.ID.Name,
	})
	if err != nil {
		t.Fatal(err)
	}
	relabelled := cloneHello(hello)
	relabelled.Initiator = "carol"
	_, response, err := NewResponder(bob, ResponderConfig{Mode: ModeHardened}, relabelled)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := initiator.HandleResponse(response); !errors.Is(err, ErrSignature) {
		t.Fatalf("hardened relabel error = %v, want ErrSignature", err)
	}
	if _, err := initiator.HandleResponse(response); !errors.Is(err, ErrSignature) {
		t.Fatalf("second hardened relabel error = %v", err)
	}
}

func TestResponseIdentityRelabelAndMessageKeySubstitutionRejected(t *testing.T) {
	_, parties := testParties(t, "alice", "bob", "mallory")
	alice, bob, mallory := parties["alice"], parties["bob"], parties["mallory"]
	initiator, hello, err := NewInitiator(alice, InitiatorConfig{
		Mode: ModeHardened, SessionID: []byte("response-relabel"), Peer: bob.ID.Name,
	})
	if err != nil {
		t.Fatal(err)
	}
	responder, response, err := NewResponder(bob, ResponderConfig{Mode: ModeHardened}, hello)
	if err != nil {
		t.Fatal(err)
	}
	relabelled := cloneResponse(response)
	relabelled.Responder = mallory.ID.Name
	if _, err := initiator.HandleResponse(relabelled); !errors.Is(err, ErrIdentityMismatch) {
		t.Fatalf("response relabel error = %v, want ErrIdentityMismatch", err)
	}

	auth, err := initiator.HandleResponse(response)
	if err != nil {
		t.Fatal(err)
	}
	forged := cloneAuth(auth)
	forged.Signature = ed25519.Sign(mallory.PrivateKey, responder.digest[:])
	if err := responder.HandleInitiatorAuth(forged); !errors.Is(err, ErrSignature) {
		t.Fatalf("message-supplied signer key error = %v, want ErrSignature", err)
	}
}

func TestWeakTranscriptAcceptsRelabelButAuthStillNamesSigner(t *testing.T) {
	_, parties := testParties(t, "alice", "bob", "carol")
	alice, bob := parties["alice"], parties["bob"]
	initiator, hello, err := NewInitiator(alice, InitiatorConfig{
		Mode: ModeWeak, SessionID: []byte("relabel-weak"), Peer: bob.ID.Name,
	})
	if err != nil {
		t.Fatal(err)
	}
	relabelled := cloneHello(hello)
	relabelled.Initiator = "carol"
	responder, response, err := NewResponder(bob, ResponderConfig{Mode: ModeWeak}, relabelled)
	if err != nil {
		t.Fatal(err)
	}
	auth, err := initiator.HandleResponse(response)
	if err != nil {
		t.Fatalf("weak response should verify over X,Y only: %v", err)
	}
	if err := responder.HandleInitiatorAuth(auth); !errors.Is(err, ErrIdentityMismatch) {
		t.Fatalf("weak forwarded auth error = %v, want ErrIdentityMismatch", err)
	}
}

func TestTranscriptIdentityBindingAndEncoding(t *testing.T) {
	registry, parties := testParties(t, "alice", "bob", "mallory")
	_ = registry
	aEphemeral, bEphemeral := sessionEphemerals(t, 3, 4)
	x := aEphemeral.PublicBytes()
	y := bEphemeral.PublicBytes()
	base := Transcript{
		Mode: ModeHardened, SessionID: []byte("transcript-session"),
		Initiator: parties["alice"].ID, Responder: parties["bob"].ID,
		X: x, Y: y,
	}
	encoded, err := base.Encode()
	if err != nil {
		t.Fatal(err)
	}
	domainLength := int(binary.BigEndian.Uint32(encoded[:4]))
	if got := string(encoded[4 : 4+domainLength]); got != HardenedDomainLabel {
		t.Fatalf("domain label = %q", got)
	}
	baseDigest, err := base.Digest()
	if err != nil {
		t.Fatal(err)
	}

	variants := []Transcript{
		{Mode: ModeHardened, SessionID: []byte("different-session"), Initiator: parties["alice"].ID, Responder: parties["bob"].ID, X: x, Y: y},
		{Mode: ModeHardened, SessionID: []byte("transcript-session"), Initiator: parties["mallory"].ID, Responder: parties["bob"].ID, X: x, Y: y},
		{Mode: ModeHardened, SessionID: []byte("transcript-session"), Initiator: parties["alice"].ID, Responder: parties["mallory"].ID, X: x, Y: y},
		{Mode: ModeHardened, SessionID: []byte("transcript-session"), Initiator: parties["alice"].ID, Responder: parties["bob"].ID, X: y, Y: x},
	}
	for index, variant := range variants {
		digest, err := variant.Digest()
		if err != nil {
			t.Fatalf("variant %d: %v", index, err)
		}
		if digest == baseDigest {
			t.Fatalf("variant %d did not change the hardened digest", index)
		}
	}

	weakBase := Transcript{Mode: ModeWeak, Initiator: parties["alice"].ID, Responder: parties["bob"].ID, X: x, Y: y}
	weakBaseDigest, err := weakBase.Digest()
	if err != nil {
		t.Fatal(err)
	}
	weakChanged := Transcript{Mode: ModeWeak, SessionID: []byte("ignored"), Initiator: parties["mallory"].ID, Responder: parties["alice"].ID, X: x, Y: y}
	weakChangedDigest, err := weakChanged.Digest()
	if err != nil {
		t.Fatal(err)
	}
	if weakBaseDigest != weakChangedDigest {
		t.Fatal("weak transcript unexpectedly bound identities or session ID")
	}
}

func TestInvalidPointsAreRejectedBeforeStateChange(t *testing.T) {
	_, parties := testParties(t, "alice", "bob")
	alice, bob := parties["alice"], parties["bob"]
	invalidPoints := [][]byte{
		nil,
		{0x00},
		bytes.Repeat([]byte{0x00}, 32),
		append([]byte{0x05}, bytes.Repeat([]byte{0x00}, 32)...),
		append([]byte{0x04}, append([]byte{0x00, 0x01}, make([]byte, 63)...)...),
	}
	for index, point := range invalidPoints {
		sessionID := []byte{byte('0' + index)}
		hello := &Hello{SessionID: sessionID, Initiator: alice.ID.Name, X: point}
		if _, _, err := NewResponder(bob, ResponderConfig{Mode: ModeHardened}, hello); !errors.Is(err, ErrInvalidPoint) {
			t.Fatalf("invalid point %d error = %v", index, err)
		}
		if bob.Sessions.Used(sessionID) {
			t.Fatalf("invalid point %d consumed the session ID", index)
		}
	}

	initiator, hello, err := NewInitiator(alice, InitiatorConfig{
		Mode: ModeHardened, SessionID: []byte("invalid-Y"), Peer: bob.ID.Name,
	})
	if err != nil {
		t.Fatal(err)
	}
	_ = hello
	response := &Response{
		SessionID: []byte("invalid-Y"), Responder: bob.ID.Name,
		Y: []byte{0x00}, Signature: make([]byte, ed25519.SignatureSize),
	}
	if _, err := initiator.HandleResponse(response); !errors.Is(err, ErrInvalidPoint) {
		t.Fatalf("invalid Y error = %v", err)
	}

	_, _, valid, err := ParseP256Point(aliceEphemeral(t).PublicBytes())
	if err != nil {
		t.Fatal(err)
	}
	if err := SamePointRule(valid, valid); err != nil {
		t.Fatalf("same valid point: %v", err)
	}
	if err := SamePointRule(valid, []byte{0x00}); !errors.Is(err, ErrInvalidPoint) {
		t.Fatalf("invalid same-point peer error = %v", err)
	}
}

func aliceEphemeral(t *testing.T) *EphemeralKey {
	t.Helper()
	key, err := EphemeralFromScalar([]byte{5})
	if err != nil {
		t.Fatal(err)
	}
	return key
}

func TestSamePointNeedsBothPrivateKeysAndConfirmation(t *testing.T) {
	_, parties := testParties(t, "alice", "bob")
	alice, bob := parties["alice"], parties["bob"]
	sharedScalar := []byte{7}
	aEphemeral, err := EphemeralFromScalar(sharedScalar)
	if err != nil {
		t.Fatal(err)
	}
	bEphemeral, err := EphemeralFromScalar(sharedScalar)
	if err != nil {
		t.Fatal(err)
	}
	initiator, responder, keys := handshakeWithEphemerals(t, alice, bob, []byte("same-point-ok"), ModeHardened, aEphemeral, bEphemeral)
	if !initiator.SamePoint() || !responder.SamePoint() {
		t.Fatal("X == Y was not recognized")
	}
	if !initiator.ephemeral.Matches(responder.y) {
		t.Fatal("the two endpoints did not hold the same point")
	}
	if keys.SendA == ([32]byte{}) {
		t.Fatal("keys were not derived")
	}

	_, reflectionParties := testParties(t, "alice", "bob")
	reflectionAlice, reflectionBob := reflectionParties["alice"], reflectionParties["bob"]
	reflectionEphemeral, err := EphemeralFromScalar(sharedScalar)
	if err != nil {
		t.Fatal(err)
	}
	reflectionInitiator, hello, err := NewInitiator(reflectionAlice, InitiatorConfig{
		Mode: ModeHardened, SessionID: []byte("same-point-reflect"), Peer: reflectionBob.ID.Name,
		Ephemeral: reflectionEphemeral,
	})
	if err != nil {
		t.Fatal(err)
	}
	transcript := Transcript{
		Mode: ModeHardened, SessionID: []byte("same-point-reflect"),
		Initiator: reflectionAlice.ID, Responder: reflectionBob.ID,
		X: hello.X, Y: hello.X,
	}
	digest, err := transcript.Digest()
	if err != nil {
		t.Fatal(err)
	}
	reflected := &Response{
		SessionID: []byte("same-point-reflect"), Responder: reflectionBob.ID.Name,
		Y:         append([]byte(nil), hello.X...),
		Signature: ed25519.Sign(reflectionBob.PrivateKey, digest[:]),
	}
	if _, err := reflectionInitiator.HandleResponse(reflected); err != nil {
		t.Fatalf("reflected point should reach signature verification: %v", err)
	}
	if !reflectionInitiator.SamePoint() {
		t.Fatal("reflection did not set SamePoint")
	}
	if _, err := reflectionInitiator.Keys(); !errors.Is(err, ErrHandshakeState) {
		t.Fatalf("keys released before the responder confirmation: %v", err)
	}
	confirmA, err := reflectionInitiator.SendConfirmA()
	if err != nil {
		t.Fatal(err)
	}
	if err := reflectionInitiator.VerifyResponderConfirm(confirmA); !errors.Is(err, ErrConfirmation) {
		t.Fatalf("reflected role-A value accepted as B confirmation: %v", err)
	}
	if err := reflectionInitiator.VerifyResponderConfirm(&Confirmation{
		SessionID: []byte("same-point-reflect"), Role: RoleB, MAC: make([]byte, 32),
	}); !errors.Is(err, ErrConfirmation) {
		t.Fatalf("fabricated confirmation error = %v", err)
	}
}

func TestSessionIDValidationAndReuse(t *testing.T) {
	if err := ValidateSessionID(nil); !errors.Is(err, ErrInvalidSessionID) {
		t.Fatalf("empty session error = %v", err)
	}
	if err := ValidateSessionID(make([]byte, MaxSessionIDLength+1)); !errors.Is(err, ErrInvalidSessionID) {
		t.Fatalf("long session error = %v", err)
	}
	if err := ValidateSessionID([]byte("ok")); err != nil {
		t.Fatalf("valid session: %v", err)
	}

	_, parties := testParties(t, "alice", "bob")
	alice := parties["alice"]
	_, hello, err := NewInitiator(alice, InitiatorConfig{
		Mode: ModeHardened, SessionID: []byte("reuse-init"), Peer: "bob",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := NewInitiator(alice, InitiatorConfig{
		Mode: ModeHardened, SessionID: []byte("reuse-init"), Peer: "bob",
	}); !errors.Is(err, ErrSessionReuse) {
		t.Fatalf("initiator reuse error = %v", err)
	}
	_ = hello

	bob := parties["bob"]
	_, _, err = NewResponder(bob, ResponderConfig{Mode: ModeHardened}, &Hello{
		SessionID: []byte("reuse-resp"), Initiator: "alice", X: aliceEphemeral(t).PublicBytes(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := NewResponder(bob, ResponderConfig{Mode: ModeHardened}, &Hello{
		SessionID: []byte("reuse-resp"), Initiator: "alice", X: aliceEphemeral(t).PublicBytes(),
	}); !errors.Is(err, ErrSessionReuse) {
		t.Fatalf("responder reuse error = %v", err)
	}
}

func TestConfirmationOrderingRolesAndTampering(t *testing.T) {
	_, parties := testParties(t, "alice", "bob")
	alice, bob := parties["alice"], parties["bob"]
	aEphemeral, bEphemeral := sessionEphemerals(t, 11, 12)
	initiator, hello, err := NewInitiator(alice, InitiatorConfig{
		Mode: ModeHardened, SessionID: []byte("confirm-order"), Peer: bob.ID.Name, Ephemeral: aEphemeral,
	})
	if err != nil {
		t.Fatal(err)
	}
	responder, response, err := NewResponder(bob, ResponderConfig{Mode: ModeHardened, Ephemeral: bEphemeral}, hello)
	if err != nil {
		t.Fatal(err)
	}
	auth, err := initiator.HandleResponse(response)
	if err != nil {
		t.Fatal(err)
	}
	if err := responder.HandleInitiatorAuth(auth); err != nil {
		t.Fatal(err)
	}

	if err := initiator.VerifyResponderConfirm(&Confirmation{
		SessionID: []byte("confirm-order"), Role: RoleB, MAC: make([]byte, 32),
	}); !errors.Is(err, ErrHandshakeState) {
		t.Fatalf("B confirmation before A confirmation error = %v", err)
	}
	if _, err := responder.SendResponderConfirm(); !errors.Is(err, ErrHandshakeState) {
		t.Fatalf("B confirm before A proof error = %v", err)
	}
	confirmA, err := initiator.SendConfirmA()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := initiator.SendConfirmA(); !errors.Is(err, ErrHandshakeState) {
		t.Fatalf("duplicate A confirmation error = %v", err)
	}
	if err := responder.HandleInitiatorConfirm(confirmA); err != nil {
		t.Fatal(err)
	}
	if err := responder.HandleInitiatorConfirm(confirmA); !errors.Is(err, ErrHandshakeState) {
		t.Fatalf("duplicate A confirmation at B error = %v", err)
	}
	confirmB, err := responder.SendResponderConfirm()
	if err != nil {
		t.Fatal(err)
	}
	if err := initiator.VerifyResponderConfirm(confirmA); !errors.Is(err, ErrConfirmation) {
		t.Fatalf("role A value accepted as B confirmation: %v", err)
	}
	tampered := cloneConfirmation(confirmB)
	tampered.MAC[0] ^= 0x01
	if err := initiator.VerifyResponderConfirm(tampered); !errors.Is(err, ErrConfirmation) {
		t.Fatalf("tampered B confirmation error = %v", err)
	}
	if err := initiator.VerifyResponderConfirm(confirmB); err != nil {
		t.Fatal(err)
	}
	if err := initiator.VerifyResponderConfirm(confirmB); !errors.Is(err, ErrHandshakeState) {
		t.Fatalf("duplicate B confirmation error = %v", err)
	}
	if _, err := initiator.Keys(); err != nil {
		t.Fatalf("initiator keys: %v", err)
	}
	if _, err := responder.Keys(); err != nil {
		t.Fatalf("responder keys: %v", err)
	}

	_, freshParties := testParties(t, "alice", "bob")
	freshAlice, freshBob := freshParties["alice"], freshParties["bob"]
	aE, bE := sessionEphemerals(t, 11, 12)
	freshInitiator, freshHello, err := NewInitiator(freshAlice, InitiatorConfig{
		Mode: ModeHardened, SessionID: []byte("confirm-role"), Peer: freshBob.ID.Name, Ephemeral: aE,
	})
	if err != nil {
		t.Fatal(err)
	}
	freshResponder, freshResponse, err := NewResponder(freshBob, ResponderConfig{Mode: ModeHardened, Ephemeral: bE}, freshHello)
	if err != nil {
		t.Fatal(err)
	}
	freshAuth, err := freshInitiator.HandleResponse(freshResponse)
	if err != nil {
		t.Fatal(err)
	}
	if err := freshResponder.HandleInitiatorAuth(freshAuth); err != nil {
		t.Fatal(err)
	}
	if err := freshResponder.HandleInitiatorConfirm(&Confirmation{
		SessionID: []byte("confirm-role"), Role: RoleB, MAC: make([]byte, 32),
	}); !errors.Is(err, ErrConfirmation) {
		t.Fatalf("role B value accepted as A confirmation: %v", err)
	}
	freshConfirmA, err := freshInitiator.SendConfirmA()
	if err != nil {
		t.Fatal(err)
	}
	if err := freshInitiator.VerifyResponderConfirm(freshConfirmA); !errors.Is(err, ErrConfirmation) {
		t.Fatalf("role A value accepted as B confirmation: %v", err)
	}
}

func TestParsePointAcceptsCanonicalBothForms(t *testing.T) {
	ephemeral, err := EphemeralFromScalar([]byte{42})
	if err != nil {
		t.Fatal(err)
	}
	x, y, compressed, err := ParseP256Point(ephemeral.PublicBytes())
	if err != nil {
		t.Fatal(err)
	}
	uncompressed := elliptic.Marshal(elliptic.P256(), x, y)
	_, _, canonical, err := ParseP256Point(uncompressed)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(compressed, canonical) {
		t.Fatal("compressed and uncompressed encodings canonicalized differently")
	}
	if _, err := EncodeCompressedPoint(x, y); err != nil {
		t.Fatal(err)
	}
}
