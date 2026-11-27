package noise

import (
	"bytes"
	"errors"
	"math"
	"testing"
)

func TestCipherStateNonceExhaustion(t *testing.T) {
	key := bytes.Repeat([]byte{0x5a}, keySize)
	send, err := NewCipherState(key)
	if err != nil {
		t.Fatal(err)
	}
	send.nonce = math.MaxUint64 - 1
	last, err := send.EncryptWithAd([]byte("ad"), []byte("last"))
	if err != nil {
		t.Fatalf("last permitted encryption: %v", err)
	}
	if send.Nonce() != math.MaxUint64 {
		t.Fatalf("nonce = %d, want MaxUint64", send.Nonce())
	}
	if out, err := send.EncryptWithAd(nil, []byte("overflow")); !errors.Is(err, ErrNonceExhausted) || out != nil {
		t.Fatalf("encryption at MaxUint64 = (%x, %v), want nonce exhaustion", out, err)
	}
	if send.Nonce() != math.MaxUint64 {
		t.Fatal("rejected encryption changed nonce")
	}

	receive, err := NewCipherState(key)
	if err != nil {
		t.Fatal(err)
	}
	receive.nonce = math.MaxUint64 - 1
	plain, err := receive.DecryptWithAd([]byte("ad"), last)
	if err != nil || string(plain) != "last" {
		t.Fatalf("last permitted decryption = (%q, %v)", plain, err)
	}
	if out, err := receive.DecryptWithAd(nil, last); !errors.Is(err, ErrNonceExhausted) || out != nil {
		t.Fatalf("decryption at MaxUint64 = (%x, %v), want nonce exhaustion", out, err)
	}
}

func TestCipherStateFailedAuthDoesNotAdvanceNonce(t *testing.T) {
	key := bytes.Repeat([]byte{0x6b}, keySize)
	send, err := NewCipherState(key)
	if err != nil {
		t.Fatal(err)
	}
	msg, err := send.EncryptWithAd([]byte("ad"), []byte("payload"))
	if err != nil {
		t.Fatal(err)
	}
	receive, err := NewCipherState(key)
	if err != nil {
		t.Fatal(err)
	}
	tampered := append([]byte(nil), msg...)
	tampered[0] ^= 1
	if _, err := receive.DecryptWithAd([]byte("ad"), tampered); !errors.Is(err, ErrAuthenticationFailed) {
		t.Fatalf("tampered packet error = %v", err)
	}
	if receive.Nonce() != 0 {
		t.Fatalf("failed authentication advanced nonce to %d", receive.Nonce())
	}
	if _, err := receive.DecryptWithAd([]byte("ad"), msg); err != nil {
		t.Fatalf("valid packet after failed auth: %v", err)
	}
}

func TestCipherStateRekey(t *testing.T) {
	c, err := NewCipherState(bytes.Repeat([]byte{0x7c}, keySize))
	if err != nil {
		t.Fatal(err)
	}
	oldKey := c.key
	before, err := c.EncryptWithAd(nil, []byte("same"))
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Rekey(); err != nil {
		t.Fatalf("Rekey: %v", err)
	}
	if c.Nonce() != 0 {
		t.Fatalf("Rekey nonce = %d", c.Nonce())
	}
	if c.key == oldKey {
		t.Fatal("Rekey did not change the key")
	}
	after, err := c.EncryptWithAd(nil, []byte("same"))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(before, after) {
		t.Fatal("Rekey did not change ciphertext")
	}
	want := mustHex(t, "a634ef00a33a0f1c6165a2b3f5c7805f02993429")
	if !bytes.Equal(after, want) {
		t.Fatalf("Rekey ciphertext mismatch:\n got %x\nwant %x", after, want)
	}
}

func TestLowOrderEphemeralRejected(t *testing.T) {
	prologue := []byte("low-order e")
	initStatic := testKey(t, 0x12)
	respStatic := testKey(t, 0x42)
	initEphemeral := testKey(t, 0x72)
	respEphemeral := testKey(t, 0xa2)

	initiator, err := NewHandshake(Config{
		Pattern:     PatternXX,
		Initiator:   true,
		Prologue:    prologue,
		LocalStatic: initStatic,
		Ephemeral:   initEphemeral,
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

	msg1, err := initiator.WriteMessage(nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := responder.ReadMessage(msg1); err != nil {
		t.Fatal(err)
	}
	msg2, err := responder.WriteMessage(nil)
	if err != nil {
		t.Fatal(err)
	}
	attacked := append([]byte(nil), msg2...)
	for i := 0; i < keySize; i++ {
		attacked[i] = 0
	}
	if _, err := initiator.ReadMessage(attacked); !errors.Is(err, ErrLowOrderPoint) {
		t.Fatalf("all-zero ephemeral error = %v", err)
	}
	if !initiator.destroyed {
		t.Fatal("low-order failure did not destroy initiator state")
	}
}

func TestLowOrderStaticRejected(t *testing.T) {
	initStatic := testKey(t, 0x14)
	initEphemeral := testKey(t, 0x74)
	initiator, err := NewHandshake(Config{
		Pattern:      PatternKK,
		Initiator:    true,
		Prologue:     []byte("low-order s"),
		LocalStatic:  initStatic,
		RemoteStatic: make([]byte, keySize),
		Ephemeral:    initEphemeral,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := initiator.WriteMessage(nil); !errors.Is(err, ErrLowOrderPoint) {
		t.Fatalf("all-zero static error = %v", err)
	}
}

func TestTamperedPrologueRejected(t *testing.T) {
	initStatic := testKey(t, 0x16)
	respStatic := testKey(t, 0x46)
	initEphemeral := testKey(t, 0x76)
	respEphemeral := testKey(t, 0xa6)

	initiator, err := NewHandshake(Config{
		Pattern:     PatternXX,
		Initiator:   true,
		Prologue:    []byte("initiator prologue"),
		LocalStatic: initStatic,
		Ephemeral:   initEphemeral,
	})
	if err != nil {
		t.Fatal(err)
	}
	responder, err := NewHandshake(Config{
		Pattern:     PatternXX,
		Initiator:   false,
		Prologue:    []byte("responder prologue"),
		LocalStatic: respStatic,
		Ephemeral:   respEphemeral,
	})
	if err != nil {
		t.Fatal(err)
	}

	msg1, err := initiator.WriteMessage([]byte("plain first flight"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := responder.ReadMessage(msg1); err != nil {
		t.Fatalf("plaintext first flight: %v", err)
	}
	msg2, err := responder.WriteMessage(nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := initiator.ReadMessage(msg2); !errors.Is(err, ErrAuthenticationFailed) {
		t.Fatalf("tampered prologue error = %v", err)
	}
}

func TestFallbackTamperedPrologueRejected(t *testing.T) {
	prologue := []byte("fallback prologue")
	initStatic := testKey(t, 0x18)
	respStatic := testKey(t, 0x48)
	initEphemeral := testKey(t, 0x78)

	ik, err := NewHandshake(Config{
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
	ikMessage, err := ik.WriteMessage(nil)
	if err != nil {
		t.Fatal(err)
	}
	responder, err := NewXXFallbackResponder(
		prologue,
		respStatic,
		ikMessage,
		bytes.NewReader(fixedRandom(0xd8, keySize)),
	)
	if err != nil {
		t.Fatal(err)
	}
	initiator, err := NewXXFallbackInitiator(
		[]byte("tampered prologue"),
		initStatic,
		respStatic.PublicKey(),
		initEphemeral,
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	msg2, err := responder.WriteMessage(nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := initiator.ReadMessage(msg2); !errors.Is(err, ErrAuthenticationFailed) {
		t.Fatalf("fallback tampered prologue error = %v", err)
	}
}

func TestXXFallbackPinsResponderStatic(t *testing.T) {
	prologue := []byte("fallback static pin")
	initStatic := testKey(t, 0x19)
	respStatic := testKey(t, 0x49)
	attackerStatic := testKey(t, 0x89)
	initEphemeral := testKey(t, 0x79)
	attackerEphemeral := testKey(t, 0xb9)

	initiator, err := NewXXFallbackInitiator(
		prologue,
		initStatic,
		respStatic.PublicKey(),
		initEphemeral,
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	if initiator.expected == nil {
		t.Fatal("XXfallback initiator did not pin the responder pre-message static key")
	}

	eiPub, err := parsePublicKey(initEphemeral.PublicKey())
	if err != nil {
		t.Fatal(err)
	}
	sym, err := NewSymmetricState(PatternXXfallback.ProtocolName())
	if err != nil {
		t.Fatal(err)
	}
	if err := sym.MixHash(prologue); err != nil {
		t.Fatal(err)
	}
	if err := sym.MixHash(respStatic.PublicKey()); err != nil {
		t.Fatal(err)
	}
	if err := sym.MixHash(initEphemeral.PublicKey()); err != nil {
		t.Fatal(err)
	}

	var malicious []byte
	malicious = append(malicious, attackerEphemeral.PublicKey()...)
	if err := sym.MixHash(attackerEphemeral.PublicKey()); err != nil {
		t.Fatal(err)
	}
	shared, err := dh(attackerEphemeral, eiPub)
	if err != nil {
		t.Fatal(err)
	}
	if err := sym.MixKey(shared[:]); err != nil {
		t.Fatal(err)
	}
	zero(shared[:])
	encStatic, err := sym.EncryptAndHash(attackerStatic.PublicKey())
	if err != nil {
		t.Fatal(err)
	}
	malicious = append(malicious, encStatic...)
	shared, err = dh(attackerStatic, eiPub)
	if err != nil {
		t.Fatal(err)
	}
	if err := sym.MixKey(shared[:]); err != nil {
		t.Fatal(err)
	}
	zero(shared[:])
	encPayload, err := sym.EncryptAndHash(nil)
	if err != nil {
		t.Fatal(err)
	}
	malicious = append(malicious, encPayload...)

	if _, err := initiator.ReadMessage(malicious); !errors.Is(err, ErrAuthenticationFailed) {
		t.Fatalf("forged fallback static error = %v", err)
	}
}

func TestCrossPatternDowngradeRejected(t *testing.T) {
	prologue := []byte("cross pattern")
	initStatic := testKey(t, 0x1a)
	respStatic := testKey(t, 0x4a)
	initEphemeral := testKey(t, 0x7a)

	t.Run("XX initiator rejects XXfallback response", func(t *testing.T) {
		xx, err := NewHandshake(Config{
			Pattern:     PatternXX,
			Initiator:   true,
			Prologue:    prologue,
			LocalStatic: initStatic,
			Ephemeral:   initEphemeral,
		})
		if err != nil {
			t.Fatal(err)
		}
		msg1, err := xx.WriteMessage([]byte("xx"))
		if err != nil {
			t.Fatal(err)
		}
		fb, err := NewXXFallbackResponder(
			prologue,
			respStatic,
			msg1,
			bytes.NewReader(fixedRandom(0xe0, keySize)),
		)
		if err != nil {
			t.Fatal(err)
		}
		downgrade, err := fb.WriteMessage([]byte("fallback"))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := xx.ReadMessage(downgrade); !errors.Is(err, ErrAuthenticationFailed) {
			t.Fatalf("cross-pattern message error = %v", err)
		}
	})

	t.Run("XXfallback initiator rejects XX response", func(t *testing.T) {
		xxResponder, err := NewHandshake(Config{
			Pattern:     PatternXX,
			Initiator:   false,
			Prologue:    prologue,
			LocalStatic: respStatic,
			Ephemeral:   testKey(t, 0xaa),
		})
		if err != nil {
			t.Fatal(err)
		}
		first := make([]byte, keySize)
		copy(first, initEphemeral.PublicKey())
		first = append(first, fixedRandom(0x33, 16)...)
		if _, err := xxResponder.ReadMessage(first); err != nil {
			t.Fatal(err)
		}
		xxResponse, err := xxResponder.WriteMessage(nil)
		if err != nil {
			t.Fatal(err)
		}

		fb, err := NewXXFallbackInitiator(
			prologue,
			initStatic,
			respStatic.PublicKey(),
			initEphemeral,
			nil,
		)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := fb.ReadMessage(xxResponse); !errors.Is(err, ErrAuthenticationFailed) {
			t.Fatalf("cross-pattern message error = %v", err)
		}
	})
}

func TestFallbackResponderRequiresExactIKMessage(t *testing.T) {
	prologue := []byte("fallback transition")
	initStatic := testKey(t, 0x1c)
	respStatic := testKey(t, 0x4c)
	initEphemeral := testKey(t, 0x7c)

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
	ikMessage, err := initiator.WriteMessage(nil)
	if err != nil {
		t.Fatal(err)
	}
	responder, err := NewHandshake(Config{
		Pattern:      PatternIK,
		Initiator:    false,
		Prologue:     prologue,
		LocalStatic:  respStatic,
		RemoteStatic: initStatic.PublicKey(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := responder.ReadMessage(ikMessage); err != nil {
		t.Fatal(err)
	}
	fallback, err := responder.FallbackResponder(ikMessage)
	if err != nil {
		t.Fatal(err)
	}
	if !responder.destroyed {
		t.Fatal("source IK responder was not destroyed by fallback conversion")
	}
	want := fallbackPreHash(t, prologue, respStatic.PublicKey(), initEphemeral.PublicKey())
	if got := fallback.HandshakeHash(); !bytes.Equal(got, want) {
		t.Fatalf("fallback transcript mismatch:\n got %x\nwant %x", got, want)
	}
	if !bytes.Equal(fallback.RemoteEphemeral(), initEphemeral.PublicKey()) {
		t.Fatal("fallback lost the IK initiator ephemeral")
	}
	if fallback.expected == nil || !equalKey(fallback.expected.raw[:], initStatic.PublicKey()) {
		t.Fatal("fallback did not inherit the initiator static trust anchor")
	}

	responder2, err := NewHandshake(Config{
		Pattern:      PatternIK,
		Initiator:    false,
		Prologue:     prologue,
		LocalStatic:  respStatic,
		RemoteStatic: initStatic.PublicKey(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := responder2.ReadMessage(ikMessage); err != nil {
		t.Fatal(err)
	}
	bad := append([]byte(nil), ikMessage...)
	bad[0] ^= 1
	if _, err := responder2.FallbackResponder(bad); !errors.Is(err, ErrInvalidMessage) {
		t.Fatalf("mismatched IK ephemeral error = %v", err)
	}
	if !responder2.destroyed {
		t.Fatal("failed fallback conversion did not destroy source state")
	}
}

func TestSymmetricSplitDestroysState(t *testing.T) {
	s, err := NewSymmetricState(PatternXX.ProtocolName())
	if err != nil {
		t.Fatal(err)
	}
	if err := s.MixHash([]byte("prologue")); err != nil {
		t.Fatal(err)
	}
	if err := s.MixKey(bytes.Repeat([]byte{0x9d}, keySize)); err != nil {
		t.Fatal(err)
	}
	first, second, err := s.Split()
	if err != nil {
		t.Fatal(err)
	}
	if first == nil || second == nil {
		t.Fatal("Split returned nil cipher state")
	}
	if !s.destroyed || s.initialized || s.cipher != nil {
		t.Fatal("Split did not destroy SymmetricState")
	}
	if !isZero(s.ck[:]) || !isZero(s.h[:]) {
		t.Fatal("Split left chaining key or transcript hash")
	}
	if _, err := s.EncryptAndHash([]byte("after split")); !errors.Is(err, ErrStateDestroyed) {
		t.Fatalf("EncryptAndHash after Split error = %v", err)
	}
}
