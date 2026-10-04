package idset

import (
	"math/rand"
	"sort"
	"testing"
)

// refSet is the reference implementation: a plain map plus sorting.
type refSet map[uint32]struct{}

func (r refSet) add(x uint32)    { r[x] = struct{}{} }
func (r refSet) remove(x uint32) { delete(r, x) }

func (r refSet) sorted() []uint32 {
	out := make([]uint32, 0, len(r))
	for x := range r {
		out = append(out, x)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

func (r refSet) rank(x uint32) int {
	n := 0
	for v := range r {
		if v <= x {
			n++
		}
	}
	return n
}

func (r refSet) and(o refSet) refSet {
	out := refSet{}
	for x := range r {
		if _, ok := o[x]; ok {
			out.add(x)
		}
	}
	return out
}

func (r refSet) or(o refSet) refSet {
	out := refSet{}
	for x := range r {
		out.add(x)
	}
	for x := range o {
		out.add(x)
	}
	return out
}

func (r refSet) xor(o refSet) refSet {
	out := refSet{}
	for x := range r {
		if _, ok := o[x]; !ok {
			out.add(x)
		}
	}
	for x := range o {
		if _, ok := r[x]; !ok {
			out.add(x)
		}
	}
	return out
}

func setFromRef(r refSet) *Set {
	s := New()
	for x := range r {
		s.Add(x)
	}
	return s
}

// checkEqual asserts s and r hold exactly the same elements, with matching
// rank at probe points and matching select for every index.
func checkEqual(t *testing.T, s *Set, r refSet, probes ...uint32) {
	t.Helper()
	if s.Size() != len(r) {
		t.Fatalf("size: got %d, want %d", s.Size(), len(r))
	}
	want := r.sorted()
	got := make([]uint32, 0, len(want))
	s.ForEach(func(x uint32) { got = append(got, x) })
	if len(got) != len(want) {
		t.Fatalf("iteration length: got %d, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("iteration[%d]: got %d, want %d", i, got[i], want[i])
		}
	}
	for _, p := range probes {
		if gr, wr := s.Rank(p), r.rank(p); gr != wr {
			t.Fatalf("rank(%d): got %d, want %d", p, gr, wr)
		}
	}
	for k := range want {
		v, err := s.Select(k)
		if err != nil || v != want[k] {
			t.Fatalf("select(%d): got (%d, %v), want %d", k, v, err, want[k])
		}
	}
}

// randRef builds a random reference set. With probability pCluster values
// fall into a few hot blocks, else they span the full 32-bit domain.
func randRef(rng *rand.Rand, n int, pCluster float64) refSet {
	r := refSet{}
	for i := 0; i < n; i++ {
		if rng.Float64() < pCluster {
			r.add(uint32(rng.Intn(3 << 16)))
		} else {
			r.add(rng.Uint32())
		}
	}
	return r
}
