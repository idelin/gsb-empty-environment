package lseq

import (
	"math/rand"
	"strings"
)

// OpKind distinguishes insertions from deletions.
type OpKind uint8

const (
	OpInsert OpKind = iota
	OpDelete
)

// Op is a causal broadcast operation. Insertions carry the allocated
// identifier and commute with each other, so replicas may apply them in any
// order and still converge. Deletions reference the target identifier and
// are idempotent.
type Op struct {
	Kind  OpKind
	ID    Identifier
	Value rune
	Site  uint64
	Seq   uint64 // per-site sequence number (causal order within a site)
}

// Element is one character of the sequence together with its immutable
// identifier.
type Element struct {
	ID    Identifier
	Value rune
}

// Sequence is a replica of the LSEQ collaborative sequence.
type Sequence struct {
	site  uint64
	seq   uint64
	elems []Element // kept sorted by identifier
	rng   *rand.Rand
}

// New creates a replica for the given site. A nil rng is replaced by a
// deterministic source derived from the site id.
func New(site uint64, rng *rand.Rand) *Sequence {
	if rng == nil {
		rng = rand.New(rand.NewSource(int64(site)*7919 + 1))
	}
	return &Sequence{site: site, rng: rng}
}

// Len returns the number of visible elements.
func (s *Sequence) Len() int { return len(s.elems) }

// Insert inserts value at index (0..Len) and returns the operation to
// broadcast to the other replicas.
func (s *Sequence) Insert(index int, value rune) Op {
	if index < 0 || index > len(s.elems) {
		panic("lseq: insert index out of range")
	}
	var p, q Identifier
	if index > 0 {
		p = s.elems[index-1].ID
	}
	if index < len(s.elems) {
		q = s.elems[index].ID
	}
	id := Allocate(p, q, s.site, s.rng)
	s.seq++
	op := Op{Kind: OpInsert, ID: id, Value: value, Site: s.site, Seq: s.seq}
	s.Apply(op)
	return op
}

// Delete removes the element at index and returns the operation to broadcast.
func (s *Sequence) Delete(index int) Op {
	if index < 0 || index >= len(s.elems) {
		panic("lseq: delete index out of range")
	}
	s.seq++
	op := Op{Kind: OpDelete, ID: s.elems[index].ID, Site: s.site, Seq: s.seq}
	s.Apply(op)
	return op
}

// Apply integrates a (local or remote) operation. It is idempotent and
// insertion-commutative, so any delivery order converges.
func (s *Sequence) Apply(op Op) bool {
	i, found := s.find(op.ID)
	switch op.Kind {
	case OpInsert:
		if found {
			return false
		}
		s.elems = append(s.elems, Element{})
		copy(s.elems[i+1:], s.elems[i:])
		s.elems[i] = Element{ID: op.ID, Value: op.Value}
		return true
	case OpDelete:
		if !found {
			return false
		}
		s.elems = append(s.elems[:i], s.elems[i+1:]...)
		return true
	}
	return false
}

// find locates id by binary search over the sorted element list.
func (s *Sequence) find(id Identifier) (int, bool) {
	lo, hi := 0, len(s.elems)
	for lo < hi {
		mid := int(uint(lo+hi) >> 1)
		switch Compare(s.elems[mid].ID, id) {
		case 0:
			return mid, true
		case -1:
			lo = mid + 1
		default:
			hi = mid
		}
	}
	return lo, false
}

// String renders the visible sequence.
func (s *Sequence) String() string {
	var b strings.Builder
	for _, e := range s.elems {
		b.WriteRune(e.Value)
	}
	return b.String()
}

// Sorted reports whether the element list is strictly ordered, an invariant
// that must hold on every replica after every merge.
func (s *Sequence) Sorted() bool {
	for i := 1; i < len(s.elems); i++ {
		if Compare(s.elems[i-1].ID, s.elems[i].ID) >= 0 {
			return false
		}
	}
	return true
}

// DepthStats returns the maximum and average identifier depth (bit-width
// proxy) across the sequence. Smooth growth of these numbers under dense
// editing is the key LSEQ property.
func (s *Sequence) DepthStats() (maxDepth int, avgDepth float64) {
	for _, e := range s.elems {
		if len(e.ID) > maxDepth {
			maxDepth = len(e.ID)
		}
		avgDepth += float64(len(e.ID))
	}
	if len(s.elems) > 0 {
		avgDepth /= float64(len(s.elems))
	}
	return maxDepth, avgDepth
}
