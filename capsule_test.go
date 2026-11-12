package netsec

import (
	"context"
	"crypto/ed25519"
	"encoding/binary"
	"errors"
	"math"
	"testing"
)

// rawCapsule mirrors the on-wire layout used by the test builder.
type rawCapsule struct {
	Magic      uint32
	HeaderLen  uint32
	PayloadLen uint32
	AuthHdrLen uint32
	Payload    []byte
	Counter    uint64
	Version    uint32
	Sig        []byte
}

func encodeRaw(raw rawCapsule) []byte {
	sigLen := uint32(len(raw.Sig))
	buf := make([]byte, 0, OuterHeaderSize+len(raw.Payload)+16+len(raw.Sig))

	var head [OuterHeaderSize]byte
	binary.BigEndian.PutUint32(head[0:4], raw.Magic)
	binary.BigEndian.PutUint32(head[4:8], raw.HeaderLen)
	binary.BigEndian.PutUint32(head[8:12], raw.PayloadLen)
	binary.BigEndian.PutUint32(head[12:16], raw.AuthHdrLen)
	buf = append(buf, head[:]...)

	buf = append(buf, raw.Payload...)

	var auth [16]byte
	binary.BigEndian.PutUint64(auth[0:8], raw.Counter)
	binary.BigEndian.PutUint32(auth[8:12], raw.Version)
	binary.BigEndian.PutUint32(auth[12:16], sigLen)
	buf = append(buf, auth[:]...)
	buf = append(buf, raw.Sig...)
	return buf
}

type testKeys struct {
	public  ed25519.PublicKey
	private ed25519.PrivateKey
}

func newTestKeys(t *testing.T) testKeys {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	return testKeys{public: pub, private: priv}
}

// signedCapsule builds a structurally valid, correctly signed capsule and lets
// the caller mutate individual attacker-controlled fields.
func signedCapsule(keys testKeys, payload []byte, counter uint64, version uint32, mutate func(*rawCapsule)) []byte {
	msg := makeSignedMessage(payload, counter)
	sig := ed25519.Sign(keys.private, msg)
	raw := rawCapsule{
		Magic:      CapsuleMagic,
		HeaderLen:  OuterHeaderSize,
		PayloadLen: uint32(len(payload)),
		AuthHdrLen: AuthHeaderSize,
		Payload:    payload,
		Counter:    counter,
		Version:    version,
		Sig:        sig,
	}
	if mutate != nil {
		mutate(&raw)
	}
	return encodeRaw(raw)
}

func testPolicy(keys testKeys, store CounterStore, minVersion uint32) Policy {
	return Policy{
		Verifier:            ED25519Verifier{PublicKey: keys.public},
		Counter:             store,
		MinSupportedVersion: minVersion,
	}
}

func TestAdmitValidCapsuleAndCommit(t *testing.T) {
	ctx := context.Background()
	keys := newTestKeys(t)
	store := NewMemoryCounter()
	policy := testPolicy(keys, store, 0x00010000)

	payload := []byte("firmware-image-v1")
	capsule := signedCapsule(keys, payload, 1, 0x00010002, nil)

	admitted, err := policy.Admit(ctx, capsule)
	if err != nil {
		t.Fatalf("admit: %v", err)
	}
	if string(admitted.Payload) != string(payload) {
		t.Fatalf("payload mismatch: got %q want %q", admitted.Payload, payload)
	}
	// Admit must not touch the stored counter.
	if got, _ := store.Current(ctx); got != 0 {
		t.Fatalf("Admit committed counter early: %d", got)
	}

	// Mutating the returned payload must not affect any shared state.
	admitted.Payload[0] = 0

	if err := policy.Commit(ctx, admitted); err != nil {
		t.Fatalf("commit: %v", err)
	}
	if got, _ := store.Current(ctx); got != 1 {
		t.Fatalf("stored counter = %d, want 1", got)
	}
}

func TestLengthWraparoundRejected(t *testing.T) {
	ctx := context.Background()
	keys := newTestKeys(t)
	policy := testPolicy(keys, NewMemoryCounter(), 0)
	payload := []byte("firmware")

	t.Run("uint32 field sum wraps", func(t *testing.T) {
		capsule := signedCapsule(keys, payload, 1, 1, func(raw *rawCapsule) {
			raw.PayloadLen = math.MaxUint32 - OuterHeaderSize + 5
		})
		_, err := policy.Admit(ctx, capsule)
		if !errors.Is(err, ErrLengthOverflow) {
			t.Fatalf("err = %v, want ErrLengthOverflow", err)
		}
	})

	t.Run("declared header length wrong", func(t *testing.T) {
		capsule := signedCapsule(keys, payload, 1, 1, func(raw *rawCapsule) {
			raw.HeaderLen = OuterHeaderSize + 4
		})
		_, err := policy.Admit(ctx, capsule)
		if !errors.Is(err, ErrHeaderLength) {
			t.Fatalf("err = %v, want ErrHeaderLength", err)
		}
	})

	t.Run("declared auth length wrong", func(t *testing.T) {
		capsule := signedCapsule(keys, payload, 1, 1, func(raw *rawCapsule) {
			raw.AuthHdrLen = AuthHeaderSize + 8
		})
		_, err := policy.Admit(ctx, capsule)
		if !errors.Is(err, ErrAuthLength) {
			t.Fatalf("err = %v, want ErrAuthLength", err)
		}
	})

	t.Run("declared total exceeds buffer", func(t *testing.T) {
		capsule := signedCapsule(keys, payload, 1, 1, func(raw *rawCapsule) {
			raw.PayloadLen = uint32(len(payload)) + 100
		})
		_, err := policy.Admit(ctx, capsule)
		if !errors.Is(err, ErrTruncatedCapsule) {
			t.Fatalf("err = %v, want ErrTruncatedCapsule", err)
		}
	})

	t.Run("trailing bytes rejected", func(t *testing.T) {
		capsule := append(signedCapsule(keys, payload, 1, 1, nil), 0xAA)
		_, err := policy.Admit(ctx, capsule)
		if !errors.Is(err, ErrTrailingBytes) {
			t.Fatalf("err = %v, want ErrTrailingBytes", err)
		}
	})

	t.Run("shrunk payload length leaves trailing bytes", func(t *testing.T) {
		capsule := signedCapsule(keys, payload, 1, 1, func(raw *rawCapsule) {
			raw.PayloadLen = uint32(len(payload)) - 1
		})
		_, err := policy.Admit(ctx, capsule)
		if !errors.Is(err, ErrTrailingBytes) {
			t.Fatalf("err = %v, want ErrTrailingBytes", err)
		}
	})

	t.Run("short buffer rejected without panic", func(t *testing.T) {
		_, err := policy.Admit(ctx, make([]byte, OuterHeaderSize-1))
		if !errors.Is(err, ErrTruncatedCapsule) {
			t.Fatalf("err = %v, want ErrTruncatedCapsule", err)
		}
	})
}
