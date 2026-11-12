package netsec

import (
	"bytes"
	"encoding/hex"
	"testing"
)

func mustHex(t *testing.T, value string) []byte {
	t.Helper()
	decoded, err := hex.DecodeString(value)
	if err != nil {
		t.Fatal(err)
	}
	return decoded
}

func TestChaCha20BlockRFC8439(t *testing.T) {
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i)
	}
	nonce := mustHex(t, "000000090000004a00000000")
	block := chacha20Block(key, 1, nonce)
	want := "10f1e7e4d13b5915500fdd1fa32071c4c7d1f4c733c068030422aa9ac3d46c4e" +
		"d2826446079faa0914c2d705d98b02a2b5129cd1de164eb9cbd083e8a2503c4e"
	if got := hex.EncodeToString(block[:]); got != want {
		t.Fatalf("ChaCha20 block mismatch\ngot  %s\nwant %s", got, want)
	}
}

func TestPoly1305RFC8439(t *testing.T) {
	key := mustHex(t, "85d6be7857556d337f4452fe42d506a80103808afb0db2fd4abff6af4149f51b")
	message := []byte("Cryptographic Forum Research Group")
	tag := poly1305Sum(key, message)
	want := "a8061dc1305136c6c22b8baf0c0127a9"
	if got := hex.EncodeToString(tag[:]); got != want {
		t.Fatalf("Poly1305 mismatch\ngot  %s\nwant %s", got, want)
	}
}

func TestChaCha20Poly1305RFC8439(t *testing.T) {
	key := mustHex(t, "808182838485868788898a8b8c8d8e8f909192939495969798999a9b9c9d9e9f")
	nonce := mustHex(t, "070000004041424344454647")
	ad := mustHex(t, "50515253c0c1c2c3c4c5c6c7")
	plaintext := []byte("Ladies and Gentlemen of the class of '99: If I could offer you only one tip for the future, sunscreen would be it.")
	ciphertextAndTag, err := chacha20poly1305Seal(key, nonce, ad, plaintext)
	if err != nil {
		t.Fatal(err)
	}
	want := "d31a8d34648e60db7b86afbc53ef7ec2a4aded51296e08fea9e2b5a736ee62d6" +
		"3dbea45e8ca9671282fafb69da92728b1a71de0a9e060b2905d6a5b67ecd3b36" +
		"92ddbd7f2d778b8c9803aee328091b58fab324e4fad675945585808b4831d7bc" +
		"3ff4def08e4b7a9de576d26586cec64b6116" +
		"1ae10b594f09e26a7e902ecbd0600691"
	if got := hex.EncodeToString(ciphertextAndTag); got != want {
		t.Fatalf("ChaCha20-Poly1305 mismatch\ngot  %s\nwant %s", got, want)
	}

	opened, err := chacha20poly1305Open(key, nonce, ad, ciphertextAndTag)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(opened, plaintext) {
		t.Fatal("AEAD round trip returned different plaintext")
	}
}
