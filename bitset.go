package main

import (
	"fmt"
	"math/bits"
	"strings"
)

type bitset struct {
	words []uint64
}

func newBitset(size int) *bitset {
	return &bitset{words: make([]uint64, (size+63)/64)}
}

func (b *bitset) set(i int)      { b.words[i>>6] |= 1 << (uint(i) & 63) }
func (b *bitset) get(i int) bool { return b.words[i>>6]&(1<<(uint(i)&63)) != 0 }

func (b *bitset) clone() *bitset {
	c := &bitset{words: make([]uint64, len(b.words))}
	copy(c.words, b.words)
	return c
}

func (b *bitset) or(o *bitset) {
	for i := range b.words {
		b.words[i] |= o.words[i]
	}
}

func (b *bitset) and(o *bitset) *bitset {
	r := b.clone()
	for i := range r.words {
		r.words[i] &= o.words[i]
	}
	return r
}

func (b *bitset) andNot(o *bitset) *bitset {
	r := b.clone()
	for i := range r.words {
		r.words[i] &^= o.words[i]
	}
	return r
}

func (b *bitset) isZero() bool {
	for _, w := range b.words {
		if w != 0 {
			return false
		}
	}
	return true
}

func (b *bitset) count() int {
	n := 0
	for _, w := range b.words {
		n += bits.OnesCount64(w)
	}
	return n
}

func (b *bitset) first() int {
	for i, w := range b.words {
		if w != 0 {
			return i*64 + bits.TrailingZeros64(w)
		}
	}
	return -1
}

func (b *bitset) key() string {
	var sb strings.Builder
	for _, w := range b.words {
		fmt.Fprintf(&sb, "%016x.", w)
	}
	return sb.String()
}
