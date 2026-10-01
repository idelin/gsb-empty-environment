package cdc

import "math/rand"

// WindowSize is the number of recent bytes that participate in the
// rolling hash. Boundary decisions only depend on the last WindowSize
// bytes, which is what makes chunking content-defined.
const WindowSize = 48

// Buzhash implements a Buzhash rolling hash over a sliding byte window.
//
// The hash of window bytes b[0..w-1] (b[0] oldest) is defined as
//
//	h = XOR_i rotl(table[b[i]], w-1-i)
//
// so it can be updated in O(1) when the window slides by one byte.
type Buzhash struct {
	table  [256]uint64
	window [WindowSize]byte
	pos    int
	hash   uint64
}

// NewBuzhash builds a Buzhash hasher. The substitution table is derived
// deterministically from seed so chunk boundaries are reproducible.
func NewBuzhash(seed int64) *Buzhash {
	r := rand.New(rand.NewSource(seed))
	b := &Buzhash{}
	for i := range b.table {
		b.table[i] = r.Uint64()
	}
	b.Reset()
	return b
}

func rotl(x uint64, k uint) uint64 {
	k &= 63
	if k == 0 {
		return x
	}
	return x<<k | x>>(64-k)
}

// Reset clears the sliding window. The hash is initialized to the hash
// of an all-zero window so the invariant hash == hashWindow(window)
// holds from the start.
func (b *Buzhash) Reset() {
	b.pos = 0
	for i := range b.window {
		b.window[i] = 0
	}
	b.hash = b.hashWindow(b.window[:])
}

// Roll slides the window by one byte: the oldest byte leaves, c enters.
// It returns the hash of the new window.
func (b *Buzhash) Roll(c byte) uint64 {
	out := b.window[b.pos]
	b.window[b.pos] = c
	b.pos = (b.pos + 1) % WindowSize
	b.hash = rotl(b.hash, 1) ^ rotl(b.table[out], WindowSize) ^ b.table[c]
	return b.hash
}

// Sum returns the hash of the current window.
func (b *Buzhash) Sum() uint64 { return b.hash }

// hashWindow recomputes the window hash from scratch; used in tests to
// verify the rolling update matches the definition.
func (b *Buzhash) hashWindow(w []byte) uint64 {
	var h uint64
	n := len(w)
	for i := 0; i < n; i++ {
		h ^= rotl(b.table[w[i]], uint(n-1-i))
	}
	return h
}
