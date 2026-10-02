package cdc

import (
	"crypto/sha256"
	"encoding/binary"
	"math/bits"
)

// windowSize is the number of trailing bytes that participate in the
// rolling hash. 48 is deliberately not a multiple of 32 so the
// rotate-based eviction of outgoing bytes spreads entropy across all
// 32 bits of the hash state.
const windowSize = 48

// buzhash is a rolling hash over a sliding byte window. Each byte is
// mapped through a pseudo-random substitution table; the hash is the
// XOR of the rotated contributions of every byte currently in the
// window. Feeding one byte (and evicting the oldest) costs O(1).
type buzhash struct {
	table  [256]uint32
	window [windowSize]byte
	pos    int
	fill   int
	hash   uint32
}

// newBuzhash builds a buzhash whose substitution table is derived
// deterministically from seed via SHA-256 counter-mode expansion, so
// every process computes identical chunk boundaries without shipping
// a hard-coded table.
func newBuzhash(seed string) *buzhash {
	b := &buzhash{}
	var counter [4]byte
	stream := make([]byte, 0, 256*4)
	for i := 0; len(stream) < 256*4; i++ {
		binary.BigEndian.PutUint32(counter[:], uint32(i))
		sum := sha256.Sum256(append([]byte(seed), counter[:]...))
		stream = append(stream, sum[:]...)
	}
	for i := 0; i < 256; i++ {
		b.table[i] = binary.BigEndian.Uint32(stream[i*4:])
	}
	return b
}

// reset clears the window and hash state. It is called at every chunk
// boundary so each chunk is hashed independently of its prefix.
func (b *buzhash) reset() {
	b.hash = 0
	b.pos = 0
	b.fill = 0
	for i := range b.window {
		b.window[i] = 0
	}
}

// roll feeds one byte into the window, evicts the oldest byte once the
// window is full, and returns the updated 32-bit hash. While the
// window is still filling no eviction term is applied, so once
// windowSize bytes have been seen the hash depends only on the last
// windowSize bytes — never on how long ago the state was reset. This
// is what lets chunk boundaries downstream of an edit re-synchronise.
func (b *buzhash) roll(in byte) uint32 {
	out := b.window[b.pos]
	b.window[b.pos] = in
	b.pos++
	if b.pos == windowSize {
		b.pos = 0
	}
	b.hash = bits.RotateLeft32(b.hash, 1) ^ b.table[in]
	if b.fill < windowSize {
		b.fill++
	} else {
		b.hash ^= bits.RotateLeft32(b.table[out], windowSize)
	}
	return b.hash
}
