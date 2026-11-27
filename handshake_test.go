package noise

import (
	"bytes"
	"errors"
	"testing"
)

func testKey(t *testing.T, seed byte) *KeyPair {
	t.Helper()
	raw := make([]byte, keySize)
	for i := range raw {
		raw[i] = seed + byte(i)
	}
	kp, err := NewKeyPair(raw)
	if err != nil {
		t.Fatalf("NewKeyPair: %v", err)
	}
	return kp
}

func TestXXHandshakeAndTransport(t *testing.T) {
	prologue := []byte("XX prologue")
	initStatic := testKey(t, 0x10)
	respStatic := testKey(t, 0x40)
	initEphemeral := testKey(t, 0x70)
	respEphemeral := testKey(t, 0xa0)

	initiator, err := NewHandshake(Config{
		Pattern:              PatternXX,
		Initiator:            true,
		Prologue:             prologue,
		LocalStatic:          initStatic,
		Ephemeral:            initEphemeral,
		ExpectedRemoteStatic: respStatic.PublicKey(),
	})
	if err != nil {
		t.Fatal(err)
	}
	responder, err := NewHandshake(Config{
		Pattern:     PatternXX,
		Initiator:   false,
		Prologue:    prologue,
		LocalStatic: respStatic,
		Ephemeral:   respEphemeral,
	})
	if err != nil {
		t.Fatal(err)
	}

	assertEqualHashes(t, initiator, responder)
	msg1, err := initiator.WriteMessage([]byte("hello"))
	if err != nil {
		t.Fatal(err)
	}
	payload, err := responder.ReadMessage(msg1)
	if err != nil {
		t.Fatal(err)
	}
	if string(payload) != "hello" {
		t.Fatalf("payload 1 = %q", payload)
	}
	assertEqualHashes(t, initiator, responder)

	msg2, err := responder.WriteMessage([]byte("world"))
	if err != nil {
		t.Fatal(err)
	}
	payload, err = initiator.ReadMessage(msg2)
	if err != nil {
		t.Fatal(err)
	}
	if string(payload) != "world" {
		t.Fatalf("payload 2 = %q", payload)
	}
	assertEqualHashes(t, initiator, responder)

	msg3, err := initiator.WriteMessage([]byte("done"))
	if err != nil {
		t.Fatal(err)
	}
	payload, err = responder.ReadMessage(msg3)
	if err != nil {
		t.Fatal(err)
	}
	if string(payload) != "done" {
		t.Fatalf("payload 3 = %q", payload)
	}
	assertEqualHashes(t, initiator, responder)

	initEphemeralState := initiator.localEphemeral
	initSymmetricState := initiator.symmetric
	respEphemeralState := responder.localEphemeral
	respSymmetricState := responder.symmetric
	sendI, recvI, err := initiator.SplitForTransport()
	if err != nil {
		t.Fatal(err)
	}
	sendR, recvR, err := responder.SplitForTransport()
	if err != nil {
		t.Fatal(err)
	}
	assertTransportPair(t, sendI, recvR, sendR, recvI)

	if !initiator.destroyed || !initiator.split || initiator.symmetric != nil ||
		initiator.localEphemeral != nil || initiator.localStatic != nil {
		t.Fatalf("initiator handshake state was not destroyed: %+v", initiator)
	}
	if !responder.destroyed || responder.symmetric != nil ||
		responder.localEphemeral != nil || responder.localStatic != nil {
		t.Fatalf("responder handshake state was not destroyed: %+v", responder)
	}
	if initiator.HandshakeHash() != nil || initiator.RemoteEphemeral() != nil {
		t.Fatal("destroyed handshake still exposes handshake data")
	}
	if _, err := initiator.WriteMessage(nil); !errors.Is(err, ErrStateDestroyed) {
		t.Fatalf("WriteMessage after Split error = %v", err)
	}
	if initEphemeralState == nil || initEphemeralState.private != nil ||
		!isZero(initEphemeralState.raw[:]) {
		t.Fatal("initiator ephemeral private key was not cleared")
	}
	if respEphemeralState == nil || respEphemeralState.private != nil ||
		!isZero(respEphemeralState.raw[:]) {
		t.Fatal("responder ephemeral private key was not cleared")
	}
	if initSymmetricState == nil || !initSymmetricState.destroyed ||
		!isZero(initSymmetricState.ck[:]) || !isZero(initSymmetricState.h[:]) {
		t.Fatal("initiator handshake symmetric state was not cleared")
	}
	if respSymmetricState == nil || !respSymmetricState.destroyed ||
		!isZero(respSymmetricState.ck[:]) || !isZero(respSymmetricState.h[:]) {
		t.Fatal("responder handshake symmetric state was not cleared")
	}
}

func TestKKHandshakeAndTransport(t *testing.T) {
	prologue := []byte("KK prologue")
	initStatic := testKey(t, 0x21)
	respStatic := testKey(t, 0x51)
	initEphemeral := testKey(t, 0x81)
	respEphemeral := testKey(t, 0xb1)

	initiator, err := NewHandshake(Config{
		Pattern:      PatternKK,
		Initiator:    true,
		Prologue:     prologue,
		LocalStatic:  initStatic,
		RemoteStatic: respStatic.PublicKey(),
		Ephemeral:    initEphemeral,
	})
	if err != nil {
		t.Fatal(err)
	}
	responder, err := NewHandshake(Config{
		Pattern:      PatternKK,
		Initiator:    false,
		Prologue:     prologue,
		LocalStatic:  respStatic,
		RemoteStatic: initStatic.PublicKey(),
		Ephemeral:    respEphemeral,
	})
	if err != nil {
		t.Fatal(err)
	}

	assertEqualHashes(t, initiator, responder)
	msg1, err := initiator.WriteMessage([]byte("kk-1"))
	if err != nil {
		t.Fatal(err)
	}
	payload, err := responder.ReadMessage(msg1)
	if err != nil {
		t.Fatal(err)
	}
	if string(payload) != "kk-1" {
		t.Fatalf("payload 1 = %q", payload)
	}
	msg2, err := responder.WriteMessage([]byte("kk-2"))
	if err != nil {
		t.Fatal(err)
	}
	payload, err = initiator.ReadMessage(msg2)
	if err != nil {
		t.Fatal(err)
	}
	if string(payload) != "kk-2" {
		t.Fatalf("payload 2 = %q", payload)
	}
	assertEqualHashes(t, initiator, responder)

	sendI, recvI, err := initiator.SplitForTransport()
	if err != nil {
		t.Fatal(err)
	}
	sendR, recvR, err := responder.SplitForTransport()
	if err != nil {
		t.Fatal(err)
	}
	assertTransportPair(t, sendI, recvR, sendR, recvI)
}

func TestIKHandshakeAndTransport(t *testing.T) {
	prologue := []byte("IK prologue")
	initStatic := testKey(t, 0x23)
	respStatic := testKey(t, 0x53)
	initEphemeral := testKey(t, 0x83)
	respEphemeral := testKey(t, 0xb3)

	initiator, err := NewHandshake(Config{
		Pattern:      PatternIK,
		Initiator:    true,
		Prologue:     prologue,
		LocalStatic:  initStatic,
		RemoteStatic: respStatic.PublicKey(),
		Ephemeral:    initEphemeral,
	})
	if err != nil {
		t.Fatal(err)
	}
	responder, err := NewHandshake(Config{
		Pattern:      PatternIK,
		Initiator:    false,
		Prologue:     prologue,
		LocalStatic:  respStatic,
		RemoteStatic: initStatic.PublicKey(),
		Ephemeral:    respEphemeral,
	})
	if err != nil {
		t.Fatal(err)
	}

	msg1, err := initiator.WriteMessage([]byte("ik-1"))
	if err != nil {
		t.Fatal(err)
	}
	payload, err := responder.ReadMessage(msg1)
	if err != nil {
		t.Fatal(err)
	}
	if string(payload) != "ik-1" {
		t.Fatalf("payload 1 = %q", payload)
	}
	msg2, err := responder.WriteMessage([]byte("ik-2"))
	if err != nil {
		t.Fatal(err)
	}
	payload, err = initiator.ReadMessage(msg2)
	if err != nil {
		t.Fatal(err)
	}
	if string(payload) != "ik-2" {
		t.Fatalf("payload 2 = %q", payload)
	}
	assertEqualHashes(t, initiator, responder)
	sendI, recvI, err := initiator.SplitForTransport()
	if err != nil {
		t.Fatal(err)
	}
	sendR, recvR, err := responder.SplitForTransport()
	if err != nil {
		t.Fatal(err)
	}
	assertTransportPair(t, sendI, recvR, sendR, recvI)
}

func TestXXFallbackFromFailedIK(t *testing.T) {
	prologue := []byte("fallback prologue")
	initStatic := testKey(t, 0x13)
	respStatic := testKey(t, 0x43)
	initEphemeral := testKey(t, 0x73)

	ikInitiator, err := NewHandshake(Config{
		Pattern:      PatternIK,
		Initiator:    true,
		Prologue:     prologue,
		LocalStatic:  initStatic,
		RemoteStatic: respStatic.PublicKey(),
		Ephemeral:    initEphemeral,
	})
	if err != nil {
		t.Fatal(err)
	}
	ikMessage, err := ikInitiator.WriteMessage([]byte("ik attempt"))
	if err != nil {
		t.Fatal(err)
	}
	if len(ikMessage) <= keySize {
		t.Fatalf("short IK message: %d", len(ikMessage))
	}

	fallbackInitiator, err := ikInitiator.FallbackInitiator()
	if err != nil {
		t.Fatal(err)
	}
	fallbackResponder, err := NewXXFallbackResponder(
		prologue,
		respStatic,
		ikMessage,
		bytes.NewReader(fixedRandom(0xd0, 32)),
	)
	if err != nil {
		t.Fatal(err)
	}
	assertEqualHashes(t, fallbackInitiator, fallbackResponder)
	if got := fallbackResponder.RemoteEphemeral(); !bytes.Equal(got, initEphemeral.PublicKey()) {
		t.Fatalf("fallback re not preserved: got %x want %x", got, initEphemeral.PublicKey())
	}

	msg2, err := fallbackResponder.WriteMessage([]byte("fallback-1"))
	if err != nil {
		t.Fatal(err)
	}
	payload, err := fallbackInitiator.ReadMessage(msg2)
	if err != nil {
		t.Fatal(err)
	}
	if string(payload) != "fallback-1" {
		t.Fatalf("payload = %q", payload)
	}
	msg3, err := fallbackInitiator.WriteMessage([]byte("fallback-2"))
	if err != nil {
		t.Fatal(err)
	}
	payload, err = fallbackResponder.ReadMessage(msg3)
	if err != nil {
		t.Fatal(err)
	}
	if string(payload) != "fallback-2" {
		t.Fatalf("payload = %q", payload)
	}
	assertEqualHashes(t, fallbackInitiator, fallbackResponder)

	sendI, recvI, err := fallbackInitiator.SplitForTransport()
	if err != nil {
		t.Fatal(err)
	}
	sendR, recvR, err := fallbackResponder.SplitForTransport()
	if err != nil {
		t.Fatal(err)
	}
	assertTransportPair(t, sendI, recvR, sendR, recvI)
}

func fixedRandom(start byte, n int) []byte {
	out := make([]byte, n)
	for i := range out {
		out[i] = start + byte(i)
	}
	return out
}

func assertEqualHashes(t *testing.T, a, b *HandshakeState) {
	t.Helper()
	ha := a.HandshakeHash()
	hb := b.HandshakeHash()
	if len(ha) == 0 || !bytes.Equal(ha, hb) {
		t.Fatalf("handshake hashes differ:\n%x\n%x", ha, hb)
	}
}

func assertTransportPair(t *testing.T, sendI, recvR, sendR, recvI *CipherState) {
	t.Helper()
	msg, err := sendI.EncryptWithAd([]byte("ad"), []byte("from initiator"))
	if err != nil {
		t.Fatal(err)
	}
	plain, err := recvR.DecryptWithAd([]byte("ad"), msg)
	if err != nil {
		t.Fatal(err)
	}
	if string(plain) != "from initiator" {
		t.Fatalf("initiator transport payload = %q", plain)
	}

	msg, err = sendR.EncryptWithAd(nil, []byte("from responder"))
	if err != nil {
		t.Fatal(err)
	}
	plain, err = recvI.DecryptWithAd(nil, msg)
	if err != nil {
		t.Fatal(err)
	}
	if string(plain) != "from responder" {
		t.Fatalf("responder transport payload = %q", plain)
	}

	if _, err := recvR.DecryptWithAd([]byte("wrong"), msg); !errors.Is(err, ErrAuthenticationFailed) {
		t.Fatalf("wrong AD error = %v", err)
	}
}
