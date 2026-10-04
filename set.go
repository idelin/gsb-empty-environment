// Package idset is a compressed set of uint32 IDs. IDs are grouped by their
// high 16 bits; each group's low 16 bits live in one of three containers:
// sorted array, fixed bitmap, or disjoint inclusive runs. Containers are
// immutable, so snapshots share structure and stay isolated from later
// mutations. Conversion between containers is lazy, but Encode always emits
// the unique canonical form (smallest serialized size; ties break
// array > run > bitmap).
package idset

import (
	"errors"
	"sort"
)

// ErrSelectRange is returned by Select for an out-of-range index.
var ErrSelectRange = errors.New("idset: select index out of range")

// Set is a compressed sorted set of uint32 values.
type Set struct {
	keys []uint16     // high 16 bits, sorted, unique
	cons []*container // cons[i] holds the low 16 bits of keys[i]
	size int
	// shared is set by Snapshot; while set, the slices must be cloned
	// before any in-place mutation (copy-on-write).
	shared bool
}

// New returns an empty set.
func New() *Set { return &Set{} }

// Size returns the number of elements.
func (s *Set) Size() int { return s.size }

func (s *Set) find(hi uint16) (int, bool) {
	i := sort.Search(len(s.keys), func(i int) bool { return s.keys[i] >= hi })
	return i, i < len(s.keys) && s.keys[i] == hi
}

// Snapshot returns a set sharing s's immutable containers. Later mutations
// of either set never affect the other.
func (s *Set) Snapshot() *Set {
	s.shared = true
	return &Set{keys: s.keys, cons: s.cons, size: s.size, shared: true}
}

// unshare ensures s exclusively owns its key/container slices.
func (s *Set) unshare() {
	if !s.shared {
		return
	}
	s.keys = append([]uint16(nil), s.keys...)
	s.cons = append([]*container(nil), s.cons...)
	s.shared = false
}

// Add inserts x.
func (s *Set) Add(x uint32) {
	hi, lo := uint16(x>>16), uint16(x)
	i, found := s.find(hi)
	if found {
		nc, delta := s.cons[i].add(lo)
		if delta == 0 {
			return
		}
		s.unshare()
		s.cons[i] = nc
		s.size += delta
		return
	}
	s.unshare()
	s.keys = append(s.keys, 0)
	copy(s.keys[i+1:], s.keys[i:])
	s.keys[i] = hi
	s.cons = append(s.cons, nil)
	copy(s.cons[i+1:], s.cons[i:])
	s.cons[i] = &container{typ: cArray, card: 1, arr: []uint16{lo}}
	s.size++
}

// Remove deletes x.
func (s *Set) Remove(x uint32) {
	hi, lo := uint16(x>>16), uint16(x)
	i, found := s.find(hi)
	if !found {
		return
	}
	nc, delta := s.cons[i].remove(lo)
	if delta == 0 {
		return
	}
	s.unshare()
	if nc.card == 0 {
		s.keys = append(s.keys[:i], s.keys[i+1:]...)
		s.cons = append(s.cons[:i], s.cons[i+1:]...)
		s.size--
		return
	}
	s.cons[i] = nc
	s.size--
}

// Contains reports whether x is present.
func (s *Set) Contains(x uint32) bool {
	i, found := s.find(uint16(x >> 16))
	return found && s.cons[i].contains(uint16(x))
}

// Rank returns the number of elements <= x.
func (s *Set) Rank(x uint32) int {
	hi, lo := uint16(x>>16), uint16(x)
	i, found := s.find(hi)
	n := 0
	for k := 0; k < i; k++ {
		n += s.cons[k].card
	}
	if found {
		n += s.cons[i].rank(lo)
	}
	return n
}

// Select returns the k-th (0-based) element, or ErrSelectRange.
func (s *Set) Select(k int) (uint32, error) {
	if k < 0 || k >= s.size {
		return 0, ErrSelectRange
	}
	for i, c := range s.cons {
		if k < c.card {
			return uint32(s.keys[i])<<16 | uint32(c.selectK(k)), nil
		}
		k -= c.card
	}
	return 0, ErrSelectRange
}

// ForEach calls f for every element in increasing order.
func (s *Set) ForEach(f func(uint32)) {
	for i, c := range s.cons {
		hi := uint32(s.keys[i]) << 16
		it := c.iter()
		for v, ok := it.next(); ok; v, ok = it.next() {
			f(hi | uint32(v))
		}
	}
}

// Optimize eagerly converts every container to its canonical form. Encoding
// does this implicitly; calling Optimize is never required for correctness.
func (s *Set) Optimize() {
	s.unshare()
	for i, c := range s.cons {
		s.cons[i] = c.canonical()
	}
}

func (s *Set) combine(o *Set, op setOp) *Set {
	r := &Set{}
	appendBlock := func(hi uint16, c *container) {
		r.keys = append(r.keys, hi)
		r.cons = append(r.cons, c)
		r.size += c.card
	}
	i, j := 0, 0
	for i < len(s.keys) && j < len(o.keys) {
		switch {
		case s.keys[i] < o.keys[j]:
			if op != opAnd {
				appendBlock(s.keys[i], s.cons[i])
			}
			i++
		case o.keys[j] < s.keys[i]:
			if op != opAnd {
				appendBlock(o.keys[j], o.cons[j])
			}
			j++
		default:
			if c := combine(s.cons[i], o.cons[j], op); c != nil {
				appendBlock(s.keys[i], c)
			}
			i++
			j++
		}
	}
	if op != opAnd {
		for ; i < len(s.keys); i++ {
			appendBlock(s.keys[i], s.cons[i])
		}
		for ; j < len(o.keys); j++ {
			appendBlock(o.keys[j], o.cons[j])
		}
	}
	return r
}

// And returns the intersection of s and o.
func (s *Set) And(o *Set) *Set { return s.combine(o, opAnd) }

// Or returns the union of s and o.
func (s *Set) Or(o *Set) *Set { return s.combine(o, opOr) }

// Xor returns the symmetric difference of s and o.
func (s *Set) Xor(o *Set) *Set { return s.combine(o, opXor) }
