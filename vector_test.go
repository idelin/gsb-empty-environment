package noise

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"testing"
)

func TestDeterministicXXVector(t *testing.T) {
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

	assertHashHex(t, initiator, "f728953a2211f602ef0923541acf3216e769dbcc0fc086baa6b6f51012e94dbc")
	msg1 := writeHex(t, initiator, []byte("hello"), ""+
		"23b7bb8c91ae008711fb12846780bcdf1e065f821bdfec49f57e7c7dcd4c4823"+
		"68656c6c6f")
	readHex(t, responder, msg1, "hello")
	assertHashHex(t, responder, "5469be8f481e50db87f8e17d3c38e069abdbe6caca27f127d970fb75791a4c5b")

	msg2 := writeHex(t, responder, []byte("world"), ""+
		"605a725d2a4adfeeb1a29e17edd621c1b7593ee8cdbc44ac6c4ab6e2f805d23c"+
		"83d397647d7c9af2cd5774e89acd26c5bfd102cc98350e7aff97a46c10cb00c2"+
		"ae0e82d4aee053cf192cf9438b07e370ab30dc6a5b221fdc3735a687e6075d5b"+
		"6c33649ccc")
	readHex(t, initiator, msg2, "world")
	assertHashHex(t, initiator, "2dc0183bea8bff1c96b9592a519ed1a07a48bb98cce934c2ae026bbecb2b2e4c")

	msg3 := writeHex(t, initiator, []byte("done"), ""+
		"ad1fbbd7d456307e8c04780979bc262c39639b5fa08a37a34708708ed1e54706"+
		"8069554379a54f8f7e702cfb84ac18f8d5fc2e06d452953183518ebe55e63b25"+
		"e71680c6")
	readHex(t, responder, msg3, "done")
	assertHashHex(t, responder, "8cd3f05c1f136743eb9ad214cda5300a81c76ae5f335017af4381b707f1ef0e0")

	sendI, recvI, err := initiator.SplitForTransport()
	if err != nil {
		t.Fatal(err)
	}
	sendR, recvR, err := responder.SplitForTransport()
	if err != nil {
		t.Fatal(err)
	}
	assertTransportHex(t, sendI, recvR, sendR, recvI,
		"979b8251d96049826bbe391cdb052ffd8ba265dec8eea0571c92edf0f78f",
		"dbfad5535cf1e822e51746eae86f6f1b3eb429b530148cf9c8d5151a9f03")
}

func TestDeterministicKKVector(t *testing.T) {
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

	msg1 := writeHex(t, initiator, []byte("kk-1"), ""+
		"883186b800b41d5cf0429695da9b3cc4f328ebcd184a6e482fa578c103f06c77"+
		"00d603d2d011105292cd0adfca2ff75c957d47f8")
	readHex(t, responder, msg1, "kk-1")
	msg2 := writeHex(t, responder, []byte("kk-2"), ""+
		"cd700e88f9e99b19c1a8a8dcd58182fd101e5e032a69ce317fde23e8ee265c51"+
		"348950d8130bda8c32504c7d6fe6f12f1ca1573f")
	readHex(t, initiator, msg2, "kk-2")
	assertHashHex(t, initiator, "da68fa163ed8627a8bd899e1ca917d821e61e014178b284ff03c3f8a1cb506da")

	sendI, recvI, err := initiator.SplitForTransport()
	if err != nil {
		t.Fatal(err)
	}
	sendR, recvR, err := responder.SplitForTransport()
	if err != nil {
		t.Fatal(err)
	}
	assertTransportHex(t, sendI, recvR, sendR, recvI,
		"f963d86ccb371bdd0573bcf1975a57a270f9139cc6a9426ccf916fe25a30",
		"0dd93218434e7468903d60b6f45f83529182513768ee1f347dd81e72a5d3")
}

func TestDeterministicXXFallbackVector(t *testing.T) {
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
	ikMessage := writeHex(t, ikInitiator, []byte("ik attempt"), ""+
		"85b7e19402f1359a3dd3be187fd7a985d677f322e9a503a44cdfca1c9563ba2a"+
		"33c3f515433ba6972a5aa0d4a3f9314ee6ed8e8d03dd2c2243b988f661513acf"+
		"953a539dcfb6639966fd26e9e1e30b0fcc6ebe3de998564f34821f0f6e330a60"+
		"f4c53ab35274edd5b37b")

	fallbackInitiator, err := ikInitiator.FallbackInitiator()
	if err != nil {
		t.Fatal(err)
	}
	fallbackResponder, err := NewXXFallbackResponder(
		prologue,
		respStatic,
		ikMessage,
		bytes.NewReader(fixedRandom(0xd0, keySize)),
	)
	if err != nil {
		t.Fatal(err)
	}

	expectedPre := fallbackPreHash(t, prologue, respStatic.PublicKey(), initEphemeral.PublicKey())
	if got := fallbackResponder.HandshakeHash(); !bytes.Equal(got, expectedPre) {
		t.Fatalf("fallback pre-message transcript mismatch:\n got %x\nwant %x", got, expectedPre)
	}
	assertEqualHashes(t, fallbackInitiator, fallbackResponder)
	if !bytes.Equal(fallbackResponder.RemoteEphemeral(), initEphemeral.PublicKey()) {
		t.Fatal("fallback did not retain the IK initiator ephemeral")
	}

	msg2 := writeHex(t, fallbackResponder, []byte("fallback-1"), ""+
		"6b3ee67463583cbe3dc08fe9d0765c2666ff5210dd527c9d8705e44927c80d55"+
		"15204afc31c491f0678a0fc02dc32eeb0494b76d2c272fed61069c51c603519d"+
		"0e4aafe804561d85640e2f9272d24a67e355f9345c6b7a7b9f59742bc1b29231"+
		"dd8fb346d8ecd3b9f057")
	readHex(t, fallbackInitiator, msg2, "fallback-1")
	msg3 := writeHex(t, fallbackInitiator, []byte("fallback-2"), ""+
		"38f9d97a148a7a93348fdf453e9be1477717155bf312be2239420af64cb4da3f"+
		"9d916841fc086d1b02a9ce01977149a80c4be12a80adcaf530aabefb62507043"+
		"b374ac00fa3954e271f9")
	readHex(t, fallbackResponder, msg3, "fallback-2")
	assertHashHex(t, fallbackResponder, "735a200ffe3869a68fa0c6b2e23ed36a16e26f931206456d6819608f364d2f41")

	sendI, recvI, err := fallbackInitiator.SplitForTransport()
	if err != nil {
		t.Fatal(err)
	}
	sendR, recvR, err := fallbackResponder.SplitForTransport()
	if err != nil {
		t.Fatal(err)
	}
	assertTransportHex(t, sendI, recvR, sendR, recvI,
		"1efd27df6031923f9f8d94351d54fd219363ea79819c399efcec76408e05",
		"0c1d14c9eff2e4ac6d536a07d01ca1226697c5b3fcd6867e329acaf72e37")
}

func writeHex(t *testing.T, h *HandshakeState, payload []byte, wantHex string) []byte {
	t.Helper()
	msg, err := h.WriteMessage(payload)
	if err != nil {
		t.Fatalf("WriteMessage: %v", err)
	}
	want, err := hex.DecodeString(wantHex)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(msg, want) {
		t.Fatalf("message mismatch:\n got %x\nwant %x", msg, want)
	}
	return msg
}

func readHex(t *testing.T, h *HandshakeState, msg []byte, want string) {
	t.Helper()
	payload, err := h.ReadMessage(msg)
	if err != nil {
		t.Fatalf("ReadMessage: %v", err)
	}
	if string(payload) != want {
		t.Fatalf("payload = %q want %q", payload, want)
	}
}

func assertHashHex(t *testing.T, h *HandshakeState, wantHex string) {
	t.Helper()
	want, err := hex.DecodeString(wantHex)
	if err != nil {
		t.Fatal(err)
	}
	if got := h.HandshakeHash(); !bytes.Equal(got, want) {
		t.Fatalf("handshake hash mismatch:\n got %x\nwant %x", got, want)
	}
}

func assertTransportHex(t *testing.T, sendI, recvR, sendR, recvI *CipherState, wantI, wantR string) {
	t.Helper()
	msgI, err := sendI.EncryptWithAd([]byte("ad"), []byte("from initiator"))
	if err != nil {
		t.Fatal(err)
	}
	want, err := hex.DecodeString(wantI)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(msgI, want) {
		t.Fatalf("transport I mismatch:\n got %x\nwant %x", msgI, want)
	}
	plain, err := recvR.DecryptWithAd([]byte("ad"), msgI)
	if err != nil {
		t.Fatal(err)
	}
	if string(plain) != "from initiator" {
		t.Fatalf("transport I payload = %q", plain)
	}

	msgR, err := sendR.EncryptWithAd(nil, []byte("from responder"))
	if err != nil {
		t.Fatal(err)
	}
	want, err = hex.DecodeString(wantR)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(msgR, want) {
		t.Fatalf("transport R mismatch:\n got %x\nwant %x", msgR, want)
	}
	plain, err = recvI.DecryptWithAd(nil, msgR)
	if err != nil {
		t.Fatal(err)
	}
	if string(plain) != "from responder" {
		t.Fatalf("transport R payload = %q", plain)
	}
}

func fallbackPreHash(t *testing.T, prologue, responderStatic, initiatorEphemeral []byte) []byte {
	t.Helper()
	name := PatternXXfallback.ProtocolName()
	var h [hashSize]byte
	if len(name) <= hashSize {
		copy(h[:], name)
	} else {
		h = sha256.Sum256([]byte(name))
	}
	h = sha256.Sum256(append(h[:], prologue...))
	h = sha256.Sum256(append(h[:], responderStatic...))
	h = sha256.Sum256(append(h[:], initiatorEphemeral...))
	return append([]byte(nil), h[:]...)
}
