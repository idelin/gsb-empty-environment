package netsec

import (
	"encoding/binary"
	"errors"
	"fmt"
)

const (
	// SHA256BlockSize is the SHA-256 compression block size in bytes.
	SHA256BlockSize = 64
	// SHA256TagSize is the SHA-256 digest/tag size in bytes.
	SHA256TagSize = 32
)

// ErrInvalidTagSize reports a tag that is not exactly SHA256TagSize bytes.
var ErrInvalidTagSize = errors.New("netsec: invalid SHA-256 tag size")

// SHA256State is the eight-word chaining state of SHA-256.  The words are
// ordered exactly like the eight big-endian words in a SHA-256 digest.
type SHA256State [8]uint32

// ImportSHA256State imports the standard SHA-256 chaining state from a 32-byte
// digest/tag.  Importing a digest is valid for length extension because the
// digest is the final chaining state of the original message, not an opaque
// one-way value in this context.
func ImportSHA256State(tag []byte) (SHA256State, error) {
	if len(tag) != SHA256TagSize {
		return SHA256State{}, fmt.Errorf("%w: got %d bytes, want %d", ErrInvalidTagSize, len(tag), SHA256TagSize)
	}

	var state SHA256State
	for i := range state {
		state[i] = binary.BigEndian.Uint32(tag[i*4 : i*4+4])
	}
	return state, nil
}

// Tag exports the state as the conventional eight big-endian digest words.
func (state SHA256State) Tag() []byte {
	tag := make([]byte, SHA256TagSize)
	for i, word := range state {
		binary.BigEndian.PutUint32(tag[i*4:i*4+4], word)
	}
	return tag
}

// SHA256Padding returns the standard SHA-256 padding for a message whose
// unpadded byte length is totalMessageLen.  The last eight bytes encode the
// bit length in big-endian order; multiplication by eight naturally wraps
// modulo 2^64, as SHA-256 specifies.
func SHA256Padding(totalMessageLen uint64) []byte {
	remainder := totalMessageLen % SHA256BlockSize
	paddingLen := uint64(SHA256BlockSize) - remainder
	if remainder > 55 {
		paddingLen += SHA256BlockSize
	}

	padding := make([]byte, paddingLen)
	padding[0] = 0x80
	binary.BigEndian.PutUint64(padding[len(padding)-8:], totalMessageLen<<3)
	return padding
}

// SHA256PaddedLength returns the length after appending SHA-256 padding to a
// message of totalMessageLen bytes.  The result is a multiple of
// SHA256BlockSize and arithmetic wraps modulo 2^64.
func SHA256PaddedLength(totalMessageLen uint64) uint64 {
	remainder := totalMessageLen % SHA256BlockSize
	if remainder <= 55 {
		return totalMessageLen + (SHA256BlockSize - remainder)
	}
	return totalMessageLen + (2*SHA256BlockSize - remainder)
}

// ContinueSHA256 continues SHA-256 from an imported chaining state.
//
// state must be the state after a complete, already-padded SHA-256 message.
// suffix is the bytes appended after that state, and totalMessageLen is the
// total byte length represented by the state plus suffix (that is, the length
// of the bytes that the forged SHA-256 invocation conceptually hashes before
// final padding).  The function appends fresh SHA-256 padding for
// totalMessageLen, compresses suffix plus that padding, and returns the result
// as a 32-byte digest.
//
// This is a standard SHA-256 compression continuation.  It does not search for
// collisions.
func ContinueSHA256(state SHA256State, suffix []byte, totalMessageLen uint64) []byte {
	padding := SHA256Padding(totalMessageLen)

	// state represents a whole number of compressed blocks.  suffix plus the
	// final padding therefore contains a whole number of blocks as well.
	data := make([]byte, len(suffix)+len(padding))
	copy(data, suffix)
	copy(data[len(suffix):], padding)

	for offset := 0; offset < len(data); offset += SHA256BlockSize {
		sha256Compress(&state, data[offset:offset+SHA256BlockSize])
	}
	return state.Tag()
}

// ExtendSHA256 performs the generic SHA-256 length-extension operation.
//
// originalMessageLen is the unpadded length of the byte string whose tag is
// supplied (for a prefix MAC this is secretLen + len(message)).  It returns the
// original SHA-256 padding and the tag of:
//
//	originalMessage || SHA256Padding(originalMessageLen) || suffix
//
// The caller is responsible for this concatenation and must only use the
// result against a verifier that treats the original message as a raw prefix.
func ExtendSHA256(tag, suffix []byte, originalMessageLen uint64) (forgedTag, padding []byte, err error) {
	state, err := ImportSHA256State(tag)
	if err != nil {
		return nil, nil, err
	}

	padding = SHA256Padding(originalMessageLen)
	paddedPrefixLen := originalMessageLen + uint64(len(padding))
	totalMessageLen := paddedPrefixLen + uint64(len(suffix))
	return ContinueSHA256(state, suffix, totalMessageLen), padding, nil
}

func sha256Compress(state *SHA256State, block []byte) {
	var schedule [64]uint32
	for i := 0; i < 16; i++ {
		schedule[i] = binary.BigEndian.Uint32(block[i*4 : i*4+4])
	}
	for i := 16; i < 64; i++ {
		w15 := schedule[i-15]
		w2 := schedule[i-2]
		sigma0 := rotateRight(w15, 7) ^ rotateRight(w15, 18) ^ (w15 >> 3)
		sigma1 := rotateRight(w2, 17) ^ rotateRight(w2, 19) ^ (w2 >> 10)
		schedule[i] = schedule[i-16] + sigma0 + schedule[i-7] + sigma1
	}

	a, b, c, d := state[0], state[1], state[2], state[3]
	e, f, g, h := state[4], state[5], state[6], state[7]

	for i, k := range sha256Constants {
		sum1 := rotateRight(e, 6) ^ rotateRight(e, 11) ^ rotateRight(e, 25)
		choose := (e & f) ^ (^e & g)
		temp1 := h + sum1 + choose + k + schedule[i]
		sum0 := rotateRight(a, 2) ^ rotateRight(a, 13) ^ rotateRight(a, 22)
		majority := (a & b) ^ (a & c) ^ (b & c)
		temp2 := sum0 + majority

		h = g
		g = f
		f = e
		e = d + temp1
		d = c
		c = b
		b = a
		a = temp1 + temp2
	}

	state[0] += a
	state[1] += b
	state[2] += c
	state[3] += d
	state[4] += e
	state[5] += f
	state[6] += g
	state[7] += h
}

func rotateRight(value uint32, shift uint) uint32 {
	return value>>shift | value<<(32-shift)
}

var sha256Constants = [64]uint32{
	0x428a2f98, 0x71374491, 0xb5c0fbcf, 0xe9b5dba5,
	0x3956c25b, 0x59f111f1, 0x923f82a4, 0xab1c5ed5,
	0xd807aa98, 0x12835b01, 0x243185be, 0x550c7dc3,
	0x72be5d74, 0x80deb1fe, 0x9bdc06a7, 0xc19bf174,
	0xe49b69c1, 0xefbe4786, 0x0fc19dc6, 0x240ca1cc,
	0x2de92c6f, 0x4a7484aa, 0x5cb0a9dc, 0x76f988da,
	0x983e5152, 0xa831c66d, 0xb00327c8, 0xbf597fc7,
	0xc6e00bf3, 0xd5a79147, 0x06ca6351, 0x14292967,
	0x27b70a85, 0x2e1b2138, 0x4d2c6dfc, 0x53380d13,
	0x650a7354, 0x766a0abb, 0x81c2c92e, 0x92722c85,
	0xa2bfe8a1, 0xa81a664b, 0xc24b8b70, 0xc76c51a3,
	0xd192e819, 0xd6990624, 0xf40e3585, 0x106aa070,
	0x19a4c116, 0x1e376c08, 0x2748774c, 0x34b0bcb5,
	0x391c0cb3, 0x4ed8aa4a, 0x5b9cca4f, 0x682e6ff3,
	0x748f82ee, 0x78a5636f, 0x84c87814, 0x8cc70208,
	0x90befffa, 0xa4506ceb, 0xbef9a3f7, 0xc67178f2,
}
