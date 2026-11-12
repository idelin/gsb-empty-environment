package netsec

import "encoding/binary"

// ChaCha20 stream cipher as defined in RFC 8439 section 2.3.
//
// The implementation is the portable, constant-index variant; it is not
// performance tuned but follows the reference construction exactly so it can
// be checked against the RFC 8439 test vectors.

func quarterRound(state []uint32, a, b, c, d int) {
	state[a] += state[b]
	state[d] ^= state[a]
	state[d] = (state[d] << 16) | (state[d] >> 16)

	state[c] += state[d]
	state[b] ^= state[c]
	state[b] = (state[b] << 12) | (state[b] >> 20)

	state[a] += state[b]
	state[d] ^= state[a]
	state[d] = (state[d] << 8) | (state[d] >> 24)

	state[c] += state[d]
	state[b] ^= state[c]
	state[b] = (state[b] << 7) | (state[b] >> 25)
}

// chacha20Block computes one 64-byte keystream block for the given 32-byte
// key, 12-byte nonce and 32-bit block counter.
func chacha20Block(key, nonce []byte, counter uint32) [64]byte {
	state := make([]uint32, 16)
	state[0] = 0x61707865
	state[1] = 0x3320646e
	state[2] = 0x79622d32
	state[3] = 0x6b206574
	for i := 0; i < 8; i++ {
		state[4+i] = binary.LittleEndian.Uint32(key[4*i:])
	}
	state[12] = counter
	state[13] = binary.LittleEndian.Uint32(nonce[0:])
	state[14] = binary.LittleEndian.Uint32(nonce[4:])
	state[15] = binary.LittleEndian.Uint32(nonce[8:])

	working := make([]uint32, 16)
	copy(working, state)
	for i := 0; i < 10; i++ {
		quarterRound(working, 0, 4, 8, 12)
		quarterRound(working, 1, 5, 9, 13)
		quarterRound(working, 2, 6, 10, 14)
		quarterRound(working, 3, 7, 11, 15)
		quarterRound(working, 0, 5, 10, 15)
		quarterRound(working, 1, 6, 11, 12)
		quarterRound(working, 2, 7, 8, 13)
		quarterRound(working, 3, 4, 9, 14)
	}

	var block [64]byte
	for i := 0; i < 16; i++ {
		binary.LittleEndian.PutUint32(block[4*i:], working[i]+state[i])
	}
	return block
}

// chacha20XOR XORs plaintext with the ChaCha20 keystream produced from key,
// nonce and an initial counter. key must be 32 bytes and nonce 12 bytes.
func chacha20XOR(key, nonce, plaintext []byte) []byte {
	out := make([]byte, len(plaintext))
	counter := uint32(0)
	for offset := 0; offset < len(plaintext); offset += 64 {
		block := chacha20Block(key, nonce, counter)
		end := offset + 64
		if end > len(plaintext) {
			end = len(plaintext)
		}
		for i := offset; i < end; i++ {
			out[i] = plaintext[i] ^ block[i-offset]
		}
		counter++
	}
	return out
}
