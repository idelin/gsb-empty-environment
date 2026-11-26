package hsm

import (
	"bytes"
	"encoding/hex"
	"testing"
)

func mustHex(t *testing.T, value string) []byte {
	t.Helper()
	decoded, err := hex.DecodeString(value)
	if err != nil {
		t.Fatalf("decode hex: %v", err)
	}
	return decoded
}

func TestAESKeyWrapRFC3394Vector(t *testing.T) {
	key := mustHex(t, "000102030405060708090a0b0c0d0e0f")
	plaintext := mustHex(t, "00112233445566778899aabbccddeeff")
	want := mustHex(t, "1fa68b0a8112b447aef34bd8fb5a7b829d3e862371d2cfe5")

	got, err := aesKeyWrap(key, plaintext)
	if err != nil {
		t.Fatalf("wrap: %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("wrap mismatch\n got %x\nwant %x", got, want)
	}
	opened, err := aesKeyUnwrap(key, got)
	if err != nil {
		t.Fatalf("unwrap: %v", err)
	}
	if !bytes.Equal(opened, plaintext) {
		t.Fatalf("unwrap mismatch: got %x want %x", opened, plaintext)
	}
}

func TestAESKeyWrapRejectsTampering(t *testing.T) {
	key := mustHex(t, "000102030405060708090a0b0c0d0e0f")
	plaintext := mustHex(t, "00112233445566778899aabbccddeeff")
	wrapped, err := aesKeyWrap(key, plaintext)
	if err != nil {
		t.Fatalf("wrap: %v", err)
	}
	wrapped[len(wrapped)-1] ^= 1
	if _, err := aesKeyUnwrap(key, wrapped); err == nil {
		t.Fatal("tampered AES-KW ciphertext was accepted")
	}
}

func TestAESCBCAndGCMRoundTrip(t *testing.T) {
	key := bytes.Repeat([]byte{0x42}, 32)
	plaintext := []byte("a protected key or message")
	iv := make([]byte, 16)
	cbc, err := aesCBCEncrypt(key, plaintext, iv)
	if err != nil {
		t.Fatalf("CBC encrypt: %v", err)
	}
	got, err := aesCBCDecrypt(key, cbc)
	if err != nil {
		t.Fatalf("CBC decrypt: %v", err)
	}
	if !bytes.Equal(got, plaintext) {
		t.Fatalf("CBC mismatch: got %q", got)
	}

	nonce := make([]byte, 12)
	gcm, err := aesGCMEncrypt(key, plaintext, nonce)
	if err != nil {
		t.Fatalf("GCM encrypt: %v", err)
	}
	got, err = aesGCMDecrypt(key, gcm)
	if err != nil {
		t.Fatalf("GCM decrypt: %v", err)
	}
	if !bytes.Equal(got, plaintext) {
		t.Fatalf("GCM mismatch: got %q", got)
	}
}
