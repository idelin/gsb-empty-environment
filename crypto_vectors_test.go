package netsec

import (
	"bytes"
	"encoding/hex"
	"testing"
)

// RFC 8439 section 2.3.2: ChaCha20 block and keystream.
func TestChaCha20RFC8439(t *testing.T) {
	key, _ := hex.DecodeString("000102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f")
	nonce, _ := hex.DecodeString("000000090000004a00000000")
	block := chacha20Block(key, nonce, 1)
	want, _ := hex.DecodeString(
		"10f1e7e4d13b5915500fdd1fa32071c4" +
			"c7d1f4c733c068030422aa9ac3d46c4e" +
			"d2826446079faa0914c2d705d98b02a2" +
			"b5129cd1de164eb9cbd083e8a2503c4e")
	if !bytes.Equal(block[:], want) {
		t.Fatalf("chacha20 block mismatch\n got: %x\nwant: %x", block, want)
	}
}

// RFC 8439 section 2.5.2: Poly1305 authentication.
func TestPoly1305RFC8439(t *testing.T) {
	key, _ := hex.DecodeString("85d6be7857556d337f4452fe42d506a80103808afb0db2fd4abff6af4149f51b")
	msg := []byte("Cryptographic Forum Research Group")
	got := poly1305MAC(key, msg)
	want, _ := hex.DecodeString("a8061dc1305136c6c22b8baf0c0127a9")
	if !bytes.Equal(got, want) {
		t.Fatalf("poly1305 mismatch\n got: %x\nwant: %x", got, want)
	}
}

// RFC 8439 section 2.8.2: AEAD ChaCha20-Poly1305.
func TestAEADRFC8439(t *testing.T) {
	key, _ := hex.DecodeString("808182838485868788898a8b8c8d8e8f909192939495969798999a9b9c9d9e9f")
	nonce, _ := hex.DecodeString("070000004041424344454647")
	aad, _ := hex.DecodeString("50515253c0c1c2c3c4c5c6c7")
	plaintext := []byte("Ladies and Gentlemen of the class of '99: If I could offer you only one tip for the future, sunscreen would be it.")

	sealed := aeadSeal(key, nonce, plaintext, aad)
	opened, err := aeadOpen(key, nonce, sealed, aad)
	if err != nil {
		t.Fatalf("aeadOpen: %v", err)
	}
	if !bytes.Equal(opened, plaintext) {
		t.Fatalf("plaintext mismatch: %q", opened)
	}

	// Tampered ciphertext must fail authentication and return no plaintext.
	tampered := bytes.Clone(sealed)
	tampered[0] ^= 1
	if _, err := aeadOpen(key, nonce, tampered, aad); err == nil {
		t.Fatal("expected authentication failure for tampered ciphertext")
	}

	// Empty plaintext: the tag covers just the padded lengths block.
	emptySealed := aeadSeal(key, nonce, nil, nil)
	if len(emptySealed) != 16 {
		t.Fatalf("empty seal length = %d, want 16", len(emptySealed))
	}
	emptyOpened, err := aeadOpen(key, nonce, emptySealed, nil)
	if err != nil || len(emptyOpened) != 0 {
		t.Fatalf("empty open: %v %d", err, len(emptyOpened))
	}
}

// RFC 7914 section 12: scrypt test vectors (the two cheapest ones).
func TestScryptRFC7914(t *testing.T) {
	cases := []struct {
		password, salt string
		n, r, p, len   int
		want           string
	}{
		{
			password: "", salt: "",
			n: 16, r: 1, p: 1, len: 64,
			want: "77d6576238657b203b19ca42c18a0497f16b4844e3074ae8dfdffa3fede21442" +
				"fcd0069ded0948f8326a753a0fc81f17e8d3e0fb2e0d3628cf35e20c38d18906",
		},
		{
			password: "password", salt: "NaCl",
			n: 1024, r: 8, p: 16, len: 64,
			want: "fdbabe1c9d3472007856e7190d01e9fe7c6ad7cbc8237830e77376634b373162" +
				"2eaf30d92e22a3886ff109279d9830dac727afb94a83ee6d8360cbdfa2cc0640",
		},
	}
	for _, tc := range cases {
		got, err := scrypt([]byte(tc.password), []byte(tc.salt), tc.n, tc.r, tc.p, tc.len)
		if err != nil {
			t.Fatalf("scrypt: %v", err)
		}
		if hex.EncodeToString(got) != tc.want {
			t.Fatalf("scrypt mismatch\n got: %s\nwant: %s", got, tc.want)
		}
	}
}
