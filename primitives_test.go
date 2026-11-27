package noise

import (
	"bytes"
	"encoding/hex"
	"testing"
)

func mustHex(t *testing.T, s string) []byte {
	t.Helper()
	b, err := hex.DecodeString(s)
	if err != nil {
		t.Fatalf("invalid hex: %v", err)
	}
	return b
}

func TestChaCha20BlockRFC8439(t *testing.T) {
	key := bytes.Repeat([]byte{0}, 32)
	for i := range key {
		key[i] = byte(i)
	}
	nonce := mustHex(t, "000000090000004a00000000")
	var out [64]byte
	chacha20Block64(&out, (*[32]byte)(key), 1, nonce)
	want := mustHex(t, ""+
		"10f1e7e4d13b5915500fdd1fa32071c4"+
		"c7d1f4c733c068030422aa9ac3d46c4e"+
		"d2826446079faa0914c2d705d98b02a2"+
		"b5129cd1de164eb9cbd083e8a2503c4e")
	if !bytes.Equal(out[:], want) {
		t.Fatalf("ChaCha20 block mismatch:\n got %x\nwant %x", out, want)
	}
}

func TestPoly1305RFC8439(t *testing.T) {
	keyBytes := mustHex(t, "85d6be7857556d337f4452fe42d506a8"+
		"0103808afb0db2fd4abff6af4149f51b")
	var key [32]byte
	copy(key[:], keyBytes)
	p := newPoly1305(&key)
	p.write([]byte("Cryptographic Forum Research Group"))
	tag := p.sum()
	want := mustHex(t, "a8061dc1305136c6c22b8baf0c0127a9")
	if !bytes.Equal(tag[:], want) {
		t.Fatalf("Poly1305 mismatch: got %x want %x", tag, want)
	}
}

func TestChaCha20Poly1305RFC8439(t *testing.T) {
	key := mustHex(t, "808182838485868788898a8b8c8d8e8f"+
		"909192939495969798999a9b9c9d9e9f")
	nonce := mustHex(t, "070000004041424344454647")
	ad := mustHex(t, "50515253c0c1c2c3c4c5c6c7")
	plaintext := []byte("Ladies and Gentlemen of the class of '99: If I could offer you only one tip for the future, sunscreen would be it.")

	aead, err := NewChaCha20Poly1305(key)
	if err != nil {
		t.Fatal(err)
	}
	sealed := aead.Seal(nil, nonce, plaintext, ad)
	want := mustHex(t, ""+
		"d31a8d34648e60db7b86afbc53ef7ec2"+
		"a4aded51296e08fea9e2b5a736ee62d6"+
		"3dbea45e8ca9671282fafb69da92728b"+
		"1a71de0a9e060b2905d6a5b67ecd3b36"+
		"92ddbd7f2d778b8c9803aee328091b58"+
		"fab324e4fad675945585808b4831d7bc"+
		"3ff4def08e4b7a9de576d26586cec64b"+
		"6116"+
		"1ae10b594f09e26a7e902ecbd0600691")
	if !bytes.Equal(sealed, want) {
		t.Fatalf("ChaCha20-Poly1305 mismatch:\n got %x\nwant %x", sealed, want)
	}

	opened, err := aead.Open(nil, nonce, sealed, ad)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if !bytes.Equal(opened, plaintext) {
		t.Fatalf("plaintext mismatch")
	}

	sealed[0] ^= 1
	if _, err := aead.Open(nil, nonce, sealed, ad); err == nil {
		t.Fatal("tampered ciphertext accepted")
	}
}
