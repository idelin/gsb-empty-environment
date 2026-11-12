package netsec

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/binary"
	"errors"
)

// scrypt key derivation function, RFC 7914.
//
// The standard library does not provide scrypt, so the full construction is
// implemented here on top of crypto/sha256 (PBKDF2-HMAC-SHA256) and the
// Salsa20/8 core. Correctness is pinned to the RFC 7914 test vectors.

// scrypt derives a key of length keyLen from password and salt with the
// parameters N (CPU/memory cost, power of two), r and p.
func scrypt(password, salt []byte, n, r, p, keyLen int) ([]byte, error) {
	if n <= 1 || n&(n-1) != 0 {
		return nil, errors.New("netsec: scrypt: N must be a power of two greater than 1")
	}
	if r <= 0 || p <= 0 {
		return nil, errors.New("netsec: scrypt: r and p must be positive")
	}
	if uint64(r)*uint64(p) >= 1<<30 || uint64(r)*uint64(n) >= 1<<30 {
		return nil, errors.New("netsec: scrypt: parameters too large")
	}

	blockSize := 128 * r
	b := pbkdf2HMACSHA256(password, salt, 1, p*blockSize)

	v := make([][]uint32, n)
	for i := range v {
		v[i] = make([]uint32, 32*r)
	}
	xy := make([]uint32, 2*32*r)

	for block := 0; block < p; block++ {
		b0 := b[block*blockSize : (block+1)*blockSize]
		bWords := bytesToWords(b0)

		x := xy[:32*r]
		copy(x, bWords)

		// ROMix: fill V then apply the random-looking mixing sequence.
		for i := 0; i < n; i++ {
			copy(v[i], x)
			blockMix(x, r, xy[32*r:])
		}
		for i := 0; i < n; i++ {
			j := int(x[len(x)-16] & uint32(n-1))
			for k := range x {
				x[k] ^= v[j][k]
			}
			blockMix(x, r, xy[32*r:])
		}

		wordsToBytes(x, b0)
	}

	return pbkdf2HMACSHA256(password, b, 1, keyLen), nil
}

// blockMix performs scryptBlockMix (Salsa20/8 based) on block x of 2r chunks.
// scratch must have room for 32*r words.
func blockMix(x []uint32, r int, scratch []uint32) {
	// x contains 2r blocks of 16 words.
	y := scratch[:32*r]
	var x16 [16]uint32
	copy(x16[:], x[(2*r-1)*16:])

	for i := 0; i < 2*r; i++ {
		var in [16]uint32
		copy(in[:], x[i*16:])
		for k := range in {
			in[k] ^= x16[k]
		}
		salsa208(&in)
		copy(x16[:], in[:])

		outIdx := i / 2
		if i%2 == 0 {
			copy(y[outIdx*16:], in[:])
		} else {
			copy(y[(r+outIdx)*16:], in[:])
		}
	}
	copy(x, y)
}

// salsa208 applies the Salsa20/8 core (four double rounds) to a 16-word block.
func salsa208(in *[16]uint32) {
	x := *in
	for i := 0; i < 4; i++ {
		x[4] ^= rotl(x[0]+x[12], 7)
		x[8] ^= rotl(x[4]+x[0], 9)
		x[12] ^= rotl(x[8]+x[4], 13)
		x[0] ^= rotl(x[12]+x[8], 18)

		x[9] ^= rotl(x[5]+x[1], 7)
		x[13] ^= rotl(x[9]+x[5], 9)
		x[1] ^= rotl(x[13]+x[9], 13)
		x[5] ^= rotl(x[1]+x[13], 18)

		x[14] ^= rotl(x[10]+x[6], 7)
		x[2] ^= rotl(x[14]+x[10], 9)
		x[6] ^= rotl(x[2]+x[14], 13)
		x[10] ^= rotl(x[6]+x[2], 18)

		x[3] ^= rotl(x[15]+x[11], 7)
		x[7] ^= rotl(x[3]+x[15], 9)
		x[11] ^= rotl(x[7]+x[3], 13)
		x[15] ^= rotl(x[11]+x[7], 18)

		x[1] ^= rotl(x[0]+x[3], 7)
		x[2] ^= rotl(x[1]+x[0], 9)
		x[3] ^= rotl(x[2]+x[1], 13)
		x[0] ^= rotl(x[3]+x[2], 18)

		x[6] ^= rotl(x[5]+x[4], 7)
		x[7] ^= rotl(x[6]+x[5], 9)
		x[4] ^= rotl(x[7]+x[6], 13)
		x[5] ^= rotl(x[4]+x[7], 18)

		x[11] ^= rotl(x[10]+x[9], 7)
		x[8] ^= rotl(x[11]+x[10], 9)
		x[9] ^= rotl(x[8]+x[11], 13)
		x[10] ^= rotl(x[9]+x[8], 18)

		x[12] ^= rotl(x[15]+x[14], 7)
		x[13] ^= rotl(x[12]+x[15], 9)
		x[14] ^= rotl(x[13]+x[12], 13)
		x[15] ^= rotl(x[14]+x[13], 18)
	}
	for i := range x {
		in[i] = x[i] + in[i]
	}
}

func rotl(v uint32, n uint) uint32 {
	return (v << n) | (v >> (32 - n))
}

func bytesToWords(b []byte) []uint32 {
	words := make([]uint32, len(b)/4)
	for i := range words {
		words[i] = binary.LittleEndian.Uint32(b[4*i:])
	}
	return words
}

func wordsToBytes(words []uint32, b []byte) {
	for i, w := range words {
		binary.LittleEndian.PutUint32(b[4*i:], w)
	}
}

// pbkdf2HMACSHA256 implements PBKDF2 with HMAC-SHA256 per RFC 8018.
func pbkdf2HMACSHA256(password, salt []byte, iterations, keyLen int) []byte {
	hashLen := sha256.Size
	numBlocks := (keyLen + hashLen - 1) / hashLen
	out := make([]byte, 0, numBlocks*hashLen)

	var blockIndex [4]byte
	for block := 1; block <= numBlocks; block++ {
		binary.BigEndian.PutUint32(blockIndex[:], uint32(block))
		mac := hmac.New(sha256.New, password)
		mac.Write(salt)
		mac.Write(blockIndex[:])
		u := mac.Sum(nil)
		t := make([]byte, hashLen)
		copy(t, u)

		for iter := 1; iter < iterations; iter++ {
			mac = hmac.New(sha256.New, password)
			mac.Write(u)
			u = mac.Sum(nil)
			for i := range t {
				t[i] ^= u[i]
			}
		}
		out = append(out, t...)
	}
	return out[:keyLen]
}
