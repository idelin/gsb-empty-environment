package envguard

import "math/bits"

// bset is an immutable identity set indexed by origin id. Origin ids are
// dense, so a plain word slice keeps union/intersection/difference cheap even
// for graphs that reach the 2^44 byte length ceiling.
type bset struct {
	w []uint64
}

func newBset(n int) bset {
	return bset{w: make([]uint64, (n+63)/64)}
}

func (s bset) clone() bset {
	out := bset{w: append([]uint64(nil), s.w...)}
	return out
}

func (s bset) set(bit int) bset {
	out := s.clone()
	out.w[bit>>6] |= 1 << uint(bit&63)
	return out
}

func (s bset) has(bit int) bool {
	if bit < 0 || bit>>6 >= len(s.w) {
		return false
	}
	return s.w[bit>>6]&(1<<uint(bit&63)) != 0
}

func (s bset) union(o bset) bset {
	n := len(s.w)
	if len(o.w) > n {
		n = len(o.w)
	}
	out := bset{w: make([]uint64, n)}
	for i := 0; i < n; i++ {
		var a, b uint64
		if i < len(s.w) {
			a = s.w[i]
		}
		if i < len(o.w) {
			b = o.w[i]
		}
		out.w[i] = a | b
	}
	return out
}

func (s bset) minus(o bset) bset {
	out := bset{w: make([]uint64, len(s.w))}
	for i := range s.w {
		var b uint64
		if i < len(o.w) {
			b = o.w[i]
		}
		out.w[i] = s.w[i] &^ b
	}
	return out
}

func (s bset) empty() bool {
	for _, w := range s.w {
		if w != 0 {
			return false
		}
	}
	return true
}

// first returns the smallest set bit, or -1.
func (s bset) first() int {
	for i, w := range s.w {
		if w != 0 {
			return i*64 + bits.TrailingZeros64(w)
		}
	}
	return -1
}

// anyOutside reports whether any set bit belongs to an untrusted origin.
func (s bset) anyOutside(reg *registry) bool {
	for i, w := range s.w {
		if w&^reg.trustedMask[i] != 0 {
			return true
		}
	}
	return false
}
