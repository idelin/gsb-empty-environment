package netsec

import (
	"bytes"
	"crypto/ed25519"
	"errors"
	"testing"
)

type testPeerSet struct {
	initStatic *StaticKeyPair
	respStatic *StaticKeyPair
	initID     *IdentityKeyPair
	respID     *IdentityKeyPair
	initPeer   *Peer
	respPeer   *Peer
	initSeen   []*PeerIdentity
	respSeen   []*PeerIdentity
}

func newTestPeers(t *testing.T) *testPeerSet {
	t.Helper()
	initStatic, err := GenerateStaticKeyPair()
	if err != nil {
		t.Fatal(err)
	}
	respStatic, err := GenerateStaticKeyPair()
	if err != nil {
		t.Fatal(err)
	}
	initID, err := GenerateIdentityKeyPair()
	if err != nil {
		t.Fatal(err)
	}
	respID, err := GenerateIdentityKeyPair()
	if err != nil {
		t.Fatal(err)
	}
	set := &testPeerSet{
		initStatic: initStatic,
		respStatic: respStatic,
		initID:     initID,
		respID:     respID,
	}
	set.initPeer, err = NewPeer(PeerConfig{
		Role:            Initiator,
		StaticKeyPair:   initStatic,
		IdentityKeyPair: initID,
		OnPeerIdentity: func(identity *PeerIdentity) error {
			set.initSeen = append(set.initSeen, identity)
			return nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	set.respPeer, err = NewPeer(PeerConfig{
		Role:            Responder,
		StaticKeyPair:   respStatic,
		IdentityKeyPair: respID,
		OnPeerIdentity: func(identity *PeerIdentity) error {
			set.respSeen = append(set.respSeen, identity)
			return nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return set
}

func runXXHandshake(t *testing.T, initiator, responder *Peer) (payload2, payload3 []byte) {
	t.Helper()
	message1, err := initiator.WriteMessage(nil)
	if err != nil {
		t.Fatalf("write message 1: %v", err)
	}
	if _, err := responder.ReadMessage(message1); err != nil {
		t.Fatalf("read message 1: %v", err)
	}

	message2, err := responder.WriteMessage(nil)
	if err != nil {
		t.Fatalf("write message 2: %v", err)
	}
	payload2, err = initiator.ReadMessage(message2)
	if err != nil {
		t.Fatalf("read message 2: %v", err)
	}

	message3, err := initiator.WriteMessage(nil)
	if err != nil {
		t.Fatalf("write message 3: %v", err)
	}
	payload3, err = responder.ReadMessage(message3)
	if err != nil {
		t.Fatalf("read message 3: %v", err)
	}
	return payload2, payload3
}

func TestLegitimateXXHandshakePublishesBoundIdentities(t *testing.T) {
	set := newTestPeers(t)

	message1, err := set.initPeer.WriteMessage(nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := set.respPeer.ReadMessage(message1); err != nil {
		t.Fatal(err)
	}
	message2, err := set.respPeer.WriteMessage(nil)
	if err != nil {
		t.Fatal(err)
	}
	payload2, err := set.initPeer.ReadMessage(message2)
	if err != nil {
		t.Fatal(err)
	}

	// Message two has been received, but the initiator has not sent
	// message three yet. The responder identity must not be published.
	if _, err := set.initPeer.PeerIdentity(); !errors.Is(err, ErrHandshakeIncomplete) {
		t.Fatalf("identity published before completion: %v", err)
	}
	if len(set.initSeen) != 0 {
		t.Fatalf("callback ran before completion: %d calls", len(set.initSeen))
	}

	message3, err := set.initPeer.WriteMessage(nil)
	if err != nil {
		t.Fatal(err)
	}
	payload3, err := set.respPeer.ReadMessage(message3)
	if err != nil {
		t.Fatal(err)
	}

	initIdentity, err := set.initPeer.PeerIdentity()
	if err != nil {
		t.Fatal(err)
	}
	respIdentity, err := set.respPeer.PeerIdentity()
	if err != nil {
		t.Fatal(err)
	}
	if !initIdentity.PublicKey.Equal(set.respID.PublicKey) {
		t.Fatal("initiator authenticated the wrong responder identity")
	}
	if !respIdentity.PublicKey.Equal(set.initID.PublicKey) {
		t.Fatal("responder authenticated the wrong initiator identity")
	}
	if !bytes.Equal(initIdentity.StaticKey, set.respStatic.PublicKey) {
		t.Fatal("responder identity is not bound to its Noise static key")
	}
	if !bytes.Equal(respIdentity.StaticKey, set.initStatic.PublicKey) {
		t.Fatal("initiator identity is not bound to its Noise static key")
	}
	if len(set.initSeen) != 1 || len(set.respSeen) != 1 {
		t.Fatalf("unexpected callback counts: initiator=%d responder=%d", len(set.initSeen), len(set.respSeen))
	}
	if !bytes.Equal(set.initPeer.HandshakeHash(), set.respPeer.HandshakeHash()) {
		t.Fatal("peers derived different handshake hashes")
	}
	if len(payload2) == 0 || len(payload3) == 0 {
		t.Fatal("expected identity payloads on messages two and three")
	}
}

func TestRejectsReplacedStaticKey(t *testing.T) {
	set := newTestPeers(t)

	replacement, err := GenerateStaticKeyPair()
	if err != nil {
		t.Fatal(err)
	}

	// A malicious responder signs an identity payload for a different static
	// key while its handshake uses respStatic.
	forgedBuilder := func(messageNumber int, bindingHash, staticPublicKey []byte) ([]byte, error) {
		if messageNumber != 2 {
			return SignIdentityPayload(bindingHash, staticPublicKey, set.respID.PrivateKey)
		}
		return SignIdentityPayload(bindingHash, replacement.PublicKey, set.respID.PrivateKey)
	}
	attacker, err := NewHandshakeState(HandshakeConfig{
		Role:           Responder,
		StaticKeyPair:  set.respStatic,
		PayloadBuilder: forgedBuilder,
	})
	if err != nil {
		t.Fatal(err)
	}

	verifier, err := NewHandshakeState(HandshakeConfig{
		Role:          Initiator,
		StaticKeyPair: set.initStatic,
	})
	if err != nil {
		t.Fatal(err)
	}

	message1, err := verifier.WriteMessage(nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := attacker.ReadMessage(message1); err != nil {
		t.Fatal(err)
	}
	message2, err := attacker.WriteMessage(nil)
	if err != nil {
		t.Fatal(err)
	}
	payload2, err := verifier.ReadMessage(message2)
	if err != nil {
		t.Fatal(err)
	}
	message3, err := verifier.WriteMessage(nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := attacker.ReadMessage(message3); err != nil {
		t.Fatal(err)
	}

	if _, err := verifier.VerifyPeerIdentity(payload2); !errors.Is(err, ErrStaticKeyMismatch) {
		t.Fatalf("expected static-key mismatch, got %v", err)
	}
	// The payload itself is well formed; it simply names the wrong static key.
	if _, err := VerifyIdentityPayload(payload2, verifier.PayloadBindingHash(2), replacement.PublicKey); err != nil {
		t.Fatalf("control verification failed: %v", err)
	}
}

func TestRejectsIdentityPayloadMovedToAnotherHandshake(t *testing.T) {
	set := newTestPeers(t)
	payload2, payload3 := runXXHandshake(t, set.initPeer, set.respPeer)
	firstHash := set.respPeer.state.PayloadBindingHash(3)

	second := newTestPeers(t)
	secondMessage1, err := second.initPeer.WriteMessage(nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := second.respPeer.ReadMessage(secondMessage1); err != nil {
		t.Fatal(err)
	}
	secondMessage2, err := second.respPeer.WriteMessage(nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := second.initPeer.ReadMessage(secondMessage2); err != nil {
		t.Fatal(err)
	}
	secondHash2 := second.initPeer.state.PayloadBindingHash(2)
	secondMessage3, err := second.initPeer.WriteMessage(nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := second.respPeer.ReadMessage(secondMessage3); err != nil {
		t.Fatal(err)
	}

	// Replay payload three from the first handshake into the same position in
	// the second handshake. Its signature is over a different h.
	secondHash := second.respPeer.state.PayloadBindingHash(3)
	if bytes.Equal(firstHash, secondHash) {
		t.Fatal("test setup produced identical handshake hashes")
	}
	// Message two's payload is also bound to its own handshake hash.
	if _, err := VerifyIdentityPayload(payload2, secondHash2, set.respStatic.PublicKey); !errors.Is(err, ErrInvalidIdentitySignature) {
		t.Fatalf("expected replayed message-two payload to fail, got %v", err)
	}
	if _, err := VerifyIdentityPayload(payload3, secondHash, set.initStatic.PublicKey); !errors.Is(err, ErrInvalidIdentitySignature) {
		t.Fatalf("expected replayed payload to fail signature verification, got %v", err)
	}

	// Message-local binding also prevents moving a message-two payload into
	// message three of the same handshake.
	firstSet := newTestPeers(t)
	payload2, _ = runXXHandshake(t, firstSet.initPeer, firstSet.respPeer)
	if _, err := VerifyIdentityPayload(payload2, firstSet.initPeer.state.PayloadBindingHash(3), firstSet.respStatic.PublicKey); !errors.Is(err, ErrInvalidIdentitySignature) {
		t.Fatalf("expected cross-message move to fail, got %v", err)
	}
}

func TestRejectsReplacedStaticKeyOnMessage3(t *testing.T) {
	set := newTestPeers(t)
	replacement, err := GenerateStaticKeyPair()
	if err != nil {
		t.Fatal(err)
	}

	forgedBuilder := func(messageNumber int, bindingHash, staticPublicKey []byte) ([]byte, error) {
		if messageNumber == 3 {
			return SignIdentityPayload(bindingHash, replacement.PublicKey, set.initID.PrivateKey)
		}
		return SignIdentityPayload(bindingHash, staticPublicKey, set.initID.PrivateKey)
	}
	initiator, err := NewHandshakeState(HandshakeConfig{
		Role:           Initiator,
		StaticKeyPair:  set.initStatic,
		PayloadBuilder: forgedBuilder,
	})
	if err != nil {
		t.Fatal(err)
	}
	responder, err := NewHandshakeState(HandshakeConfig{
		Role:          Responder,
		StaticKeyPair: set.respStatic,
	})
	if err != nil {
		t.Fatal(err)
	}

	message1, err := initiator.WriteMessage(nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := responder.ReadMessage(message1); err != nil {
		t.Fatal(err)
	}
	message2, err := responder.WriteMessage(nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := initiator.ReadMessage(message2); err != nil {
		t.Fatal(err)
	}
	message3, err := initiator.WriteMessage(nil)
	if err != nil {
		t.Fatal(err)
	}
	payload3, err := responder.ReadMessage(message3)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := responder.VerifyPeerIdentity(payload3); !errors.Is(err, ErrStaticKeyMismatch) {
		t.Fatalf("expected static-key mismatch on message three, got %v", err)
	}
	if _, err := VerifyIdentityPayload(payload3, responder.PayloadBindingHash(3), replacement.PublicKey); err != nil {
		t.Fatalf("control verification failed: %v", err)
	}
}

func TestRejectsPayloadOnFirstMessage(t *testing.T) {
	set := newTestPeers(t)
	if _, err := set.initPeer.WriteMessage([]byte("identity")); !errors.Is(err, ErrFirstMessagePayload) {
		t.Fatalf("expected first-message payload rejection, got %v", err)
	}

	initiator, err := NewHandshakeState(HandshakeConfig{
		Role:          Initiator,
		StaticKeyPair: set.initStatic,
	})
	if err != nil {
		t.Fatal(err)
	}
	message1, err := initiator.WriteMessage(nil)
	if err != nil {
		t.Fatal(err)
	}
	injected := append(append([]byte(nil), message1...), []byte("payload")...)
	if _, err := set.respPeer.ReadMessage(injected); !errors.Is(err, ErrFirstMessagePayload) {
		t.Fatalf("expected injected first-message payload rejection, got %v", err)
	}
}

func TestIdentityPayloadSignatureAndEncoding(t *testing.T) {
	identity, err := GenerateIdentityKeyPair()
	if err != nil {
		t.Fatal(err)
	}
	static, err := GenerateStaticKeyPair()
	if err != nil {
		t.Fatal(err)
	}
	hash := bytes.Repeat([]byte{0x42}, HashSize)
	encoded, err := SignIdentityPayload(hash, static.PublicKey, identity.PrivateKey)
	if err != nil {
		t.Fatal(err)
	}
	if len(encoded) != IdentityPayloadSize {
		t.Fatalf("payload length = %d, want %d", len(encoded), IdentityPayloadSize)
	}
	verified, err := VerifyIdentityPayload(encoded, hash, static.PublicKey)
	if err != nil {
		t.Fatal(err)
	}
	if !verified.PublicKey.Equal(identity.PublicKey) {
		t.Fatal("verified identity key differs")
	}

	encoded[1] ^= 0x01
	if _, err := VerifyIdentityPayload(encoded, hash, static.PublicKey); !errors.Is(err, ErrInvalidIdentitySignature) {
		t.Fatalf("expected modified identity key to fail, got %v", err)
	}
}

func TestValidatorWithholdsIdentityUntilCompletion(t *testing.T) {
	identity, err := GenerateIdentityKeyPair()
	if err != nil {
		t.Fatal(err)
	}
	static, err := GenerateStaticKeyPair()
	if err != nil {
		t.Fatal(err)
	}
	hash := bytes.Repeat([]byte{0x7c}, HashSize)
	payload, err := SignIdentityPayload(hash, static.PublicKey, identity.PrivateKey)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ValidateIdentityPayloadAtCompletion(false, hash, static.PublicKey, payload); !errors.Is(err, ErrHandshakeIncomplete) {
		t.Fatalf("expected incomplete rejection, got %v", err)
	}
	verified, err := ValidateIdentityPayloadAtCompletion(true, hash, static.PublicKey, payload)
	if err != nil {
		t.Fatal(err)
	}
	if !verified.PublicKey.Equal(identity.PublicKey) {
		t.Fatal("wrong identity returned")
	}
}

func TestEd25519PublicKeyType(t *testing.T) {
	set := newTestPeers(t)
	runXXHandshake(t, set.initPeer, set.respPeer)
	identity, err := set.initPeer.PeerIdentity()
	if err != nil {
		t.Fatal(err)
	}
	var publicKey ed25519.PublicKey = identity.PublicKey
	if len(publicKey) != ed25519.PublicKeySize {
		t.Fatal("identity key is not a valid Ed25519 public key")
	}
}

func TestPeerWithholdsMovedPayloadIdentity(t *testing.T) {
	origin := newTestPeers(t)
	originPayload2, _ := runXXHandshake(t, origin.initPeer, origin.respPeer)

	target := newTestPeers(t)
	// Re-encrypt the origin handshake's message-two payload into a fresh
	// handshake. The encrypted envelope is valid, but the signature binds the
	// old handshake hash, not the target handshake hash.
	replayer, err := NewHandshakeState(HandshakeConfig{
		Role:          Responder,
		StaticKeyPair: origin.respStatic,
		PayloadBuilder: func(messageNumber int, bindingHash, staticPublicKey []byte) ([]byte, error) {
			if messageNumber != 2 {
				return nil, ErrUnexpectedHandshakeMessage
			}
			return originPayload2, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	message1, err := target.initPeer.WriteMessage(nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := replayer.ReadMessage(message1); err != nil {
		t.Fatal(err)
	}
	message2, err := replayer.WriteMessage(nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := target.initPeer.ReadMessage(message2); err != nil {
		t.Fatal(err)
	}
	if _, err := target.initPeer.PeerIdentity(); !errors.Is(err, ErrHandshakeIncomplete) {
		t.Fatalf("identity published before completion: %v", err)
	}
	if len(target.initSeen) != 0 {
		t.Fatal("callback ran for a moved payload")
	}

	message3, err := target.initPeer.WriteMessage(nil)
	if err == nil {
		// A non-nil message may still be returned together with a
		// verification error; this path must not accept the payload.
		t.Fatal("moved payload was accepted")
	}
	if message3 == nil || !errors.Is(err, ErrInvalidIdentitySignature) {
		t.Fatalf("expected signature failure with message bytes, got message=%v err=%v", message3 != nil, err)
	}
	if len(target.initSeen) != 0 {
		t.Fatal("callback ran for a moved payload")
	}
	if _, err := target.initPeer.PeerIdentity(); !errors.Is(err, ErrInvalidIdentitySignature) {
		t.Fatalf("PeerIdentity did not preserve the verification error: %v", err)
	}
}
