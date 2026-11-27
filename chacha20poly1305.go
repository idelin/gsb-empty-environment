package noise

import (
	"crypto/cipher"
	"crypto/subtle"
	"encoding/binary"
	"errors"
	"fmt"
	"math/bits"
)

const (
	chachaKeySize   = 32
	chachaNonceSize = 12
	chachaTagSize   = 16
)

type chacha20Poly1305 struct {
	key [chachaKeySize]byte
}

// NewChaCha20Poly1305 returns an RFC 8439 ChaCha20-Poly1305 AEAD.
func NewChaCha20Poly1305(key []byte) (cipher.AEAD, error) {
	if len(key) != chachaKeySize {
		return nil, fmt.Errorf("%w: ChaCha20-Poly1305 key must be %d bytes", ErrInvalidKey, chachaKeySize)
	}
	c := &chacha20Poly1305{}
	copy(c.key[:], key)
	return c, nil
}

func (c *chacha20Poly1305) NonceSize() int { return chachaNonceSize }

func (c *chacha20Poly1305) Overhead() int { return chachaTagSize }

// Destroy clears the AEAD key. It is intentionally not part of cipher.AEAD.
func (c *chacha20Poly1305) Destroy() {
	if c == nil {
		return
	}
	zero(c.key[:])
}

func (c *chacha20Poly1305) Seal(dst, nonce, plaintext, additionalData []byte) []byte {
	if len(nonce) != chachaNonceSize {
		panic("noise: bad ChaCha20-Poly1305 nonce length")
	}

	ret, out := sliceForAppend(dst, len(plaintext)+chachaTagSize)
	ciphertext := out[:len(plaintext)]
	tag := out[len(plaintext):]

	var polyKey [32]byte
	chacha20Block(&polyKey, &c.key, 0, nonce)
	chacha20XOR(ciphertext, plaintext, &c.key, 1, nonce)
	tagValue := poly1305MAC(&polyKey, additionalData, ciphertext)
	copy(tag, tagValue[:])

	zero(polyKey[:])
	zero(tagValue[:])
	return ret
}

func (c *chacha20Poly1305) Open(dst, nonce, ciphertext, additionalData []byte) ([]byte, error) {
	if len(nonce) != chachaNonceSize {
		panic("noise: bad ChaCha20-Poly1305 nonce length")
	}
	if len(ciphertext) < chachaTagSize {
		return nil, errors.New("noise: ChaCha20-Poly1305 ciphertext too short")
	}

	body := ciphertext[:len(ciphertext)-chachaTagSize]
	tag := ciphertext[len(ciphertext)-chachaTagSize:]
	var polyKey [32]byte
	chacha20Block(&polyKey, &c.key, 0, nonce)
	expected := poly1305MAC(&polyKey, additionalData, body)
	ok := subtle.ConstantTimeCompare(tag, expected[:]) == 1
	zero(polyKey[:])
	zero(expected[:])
	if !ok {
		return nil, errors.New("noise: ChaCha20-Poly1305 authentication failed")
	}

	ret, out := sliceForAppend(dst, len(body))
	chacha20XOR(out, body, &c.key, 1, nonce)
	return ret, nil
}

func sliceForAppend(in []byte, n int) (head, tail []byte) {
	if total := len(in) + n; cap(in) >= total {
		head = in[:total]
	} else {
		head = make([]byte, total)
		copy(head, in)
	}
	tail = head[len(in):]
	return
}

func chacha20XOR(dst, src []byte, key *[32]byte, counter uint32, nonce []byte) {
	var block [64]byte
	for len(src) > 0 {
		chacha20Block64(&block, key, counter, nonce)
		counter++
		n := len(src)
		if n > len(block) {
			n = len(block)
		}
		for i := 0; i < n; i++ {
			dst[i] = src[i] ^ block[i]
		}
		dst = dst[n:]
		src = src[n:]
	}
	zero(block[:])
}

func chacha20Block(out *[32]byte, key *[32]byte, counter uint32, nonce []byte) {
	var full [64]byte
	chacha20Block64(&full, key, counter, nonce)
	copy(out[:], full[:32])
	zero(full[:])
}

func chacha20Block64(out *[64]byte, key *[32]byte, counter uint32, nonce []byte) {
	var state [16]uint32
	state[0] = 0x61707865
	state[1] = 0x3320646e
	state[2] = 0x79622d32
	state[3] = 0x6b206574
	for i := 0; i < 8; i++ {
		state[4+i] = binary.LittleEndian.Uint32(key[i*4:])
	}
	state[12] = counter
	for i := 0; i < 3; i++ {
		state[13+i] = binary.LittleEndian.Uint32(nonce[i*4:])
	}

	x := state
	for i := 0; i < 10; i++ {
		quarterRound(&x, 0, 4, 8, 12)
		quarterRound(&x, 1, 5, 9, 13)
		quarterRound(&x, 2, 6, 10, 14)
		quarterRound(&x, 3, 7, 11, 15)
		quarterRound(&x, 0, 5, 10, 15)
		quarterRound(&x, 1, 6, 11, 12)
		quarterRound(&x, 2, 7, 8, 13)
		quarterRound(&x, 3, 4, 9, 14)
	}
	for i := 0; i < 16; i++ {
		binary.LittleEndian.PutUint32(out[i*4:], x[i]+state[i])
	}
	for i := range state {
		state[i] = 0
		x[i] = 0
	}
}

func quarterRound(x *[16]uint32, a, b, c, d int) {
	x[a] += x[b]
	x[d] ^= x[a]
	x[d] = bits.RotateLeft32(x[d], 16)
	x[c] += x[d]
	x[b] ^= x[c]
	x[b] = bits.RotateLeft32(x[b], 12)
	x[a] += x[b]
	x[d] ^= x[a]
	x[d] = bits.RotateLeft32(x[d], 8)
	x[c] += x[d]
	x[b] ^= x[c]
	x[b] = bits.RotateLeft32(x[b], 7)
}
