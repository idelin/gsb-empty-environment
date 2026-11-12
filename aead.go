package netsec

import (
	"crypto/subtle"
	"errors"
)

// ChaCha20-Poly1305 AEAD as defined in RFC 8439 section 2.8.

const (
	polyKeySize   = 32
	polyTagSize   = 16
	aeadNonceSize = 12
)

var errOpen = errors.New("netsec: chacha20poly1305: message authentication failed")

// poly1305Key derives the Poly1305 one-time key by running ChaCha20 with the
// AEAD nonce and counter 0 and taking the first 32 bytes.
func poly1305Key(key, nonce []byte) []byte {
	block := chacha20Block(key, nonce, 0)
	return block[:polyKeySize]
}

// aeadSeal encrypts plaintext and appends the 16-byte Poly1305 tag over the
// construction from RFC 8439 section 2.8. additionalData is authenticated but
// not encrypted (age passes nil).
func aeadSeal(key, nonce, plaintext, additionalData []byte) []byte {
	// Ciphertext uses the keystream starting at counter 1.
	ciphertext := chacha20XORWithCounter(key, nonce, plaintext, 1)
	macInput := buildMACInput(additionalData, ciphertext)
	tag := poly1305MAC(poly1305Key(key, nonce), macInput)
	return append(ciphertext, tag...)
}

// aeadOpen verifies the tag and decrypts ciphertext (which includes the
// trailing 16-byte tag). It returns an error rather than any plaintext on
// authentication failure.
func aeadOpen(key, nonce, sealed, additionalData []byte) ([]byte, error) {
	if len(sealed) < polyTagSize {
		return nil, errOpen
	}
	ciphertext := sealed[:len(sealed)-polyTagSize]
	tag := sealed[len(sealed)-polyTagSize:]
	macInput := buildMACInput(additionalData, ciphertext)
	expected := poly1305MAC(poly1305Key(key, nonce), macInput)
	if subtle.ConstantTimeCompare(tag, expected) != 1 {
		return nil, errOpen
	}
	return chacha20XORWithCounter(key, nonce, ciphertext, 1), nil
}

// buildMACInput lays out the authenticated data, ciphertext and the two
// little-endian 64-bit lengths per RFC 8439 section 2.8.
func buildMACInput(additionalData, ciphertext []byte) []byte {
	adLen := len(additionalData)
	ctLen := len(ciphertext)
	out := make([]byte, 0, pad16(adLen)+pad16(ctLen)+16)
	out = append(out, additionalData...)
	out = appendZeroPad(out, pad16(adLen)-adLen)
	out = append(out, ciphertext...)
	out = appendZeroPad(out, pad16(ctLen)-ctLen)
	var lengths [16]byte
	putUint64LE(lengths[:8], uint64(adLen))
	putUint64LE(lengths[8:], uint64(ctLen))
	out = append(out, lengths[:]...)
	return out
}

func pad16(n int) int {
	return (n + 15) &^ 15
}

func appendZeroPad(b []byte, n int) []byte {
	for i := 0; i < n; i++ {
		b = append(b, 0)
	}
	return b
}

func putUint64LE(b []byte, v uint64) {
	for i := 0; i < 8; i++ {
		b[i] = byte(v >> (8 * i))
	}
}

// chacha20XORWithCounter is chacha20XOR with an explicit initial counter.
func chacha20XORWithCounter(key, nonce, plaintext []byte, counter uint32) []byte {
	out := make([]byte, len(plaintext))
	c := counter
	for offset := 0; offset < len(plaintext); offset += 64 {
		block := chacha20Block(key, nonce, c)
		end := offset + 64
		if end > len(plaintext) {
			end = len(plaintext)
		}
		for i := offset; i < end; i++ {
			out[i] = plaintext[i] ^ block[i-offset]
		}
		c++
	}
	return out
}
