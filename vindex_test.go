package vindex

import (
	"fmt"
	"math/big"
	"math/rand"
	"testing"
)

func rat(n, d int64) *big.Rat { return big.NewRat(n, d) }

func segStrings[V comparable](segs []Segment[V]) []string {
	out := make([]string, len(segs))
	for i, s := range segs {
		out[i] = fmt.Sprintf("[%s,%s)=%v", s.Lo.RatString(), s.Hi.RatString(), s.Val)
	}
	return out
}

func checkSegments[V comparable](t *testing.T, st *Store[V], version int, want []string) {
	t.Helper()
	got := segStrings(st.Segments(version))
	if len(got) != len(want) {
		t.Fatalf("version %d: got %v, want %v", version, got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("version %d: got %v, want %v", version, got, want)
		}
	}
}

func checkPoint[V comparable](t *testing.T, st *Store[V], version int, p *big.Rat, want V, wantOK bool) {
	t.Helper()
	got, ok := st.Get(version, p)
	if ok != wantOK || (ok && got != want) {
		t.Fatalf("version %d point %s: got (%v,%v), want (%v,%v)",
			version, p.RatString(), got, ok, want, wantOK)
	}
}

// TestCoverageAndSplit: a full-cover write, then a partial overwrite that
// splits the original segment into three.
func TestCoverageAndSplit(t *testing.T) {
	st := New[int]()
	if v := st.Set(rat(0, 1), rat(10, 1), 1); v != 1 {
		t.Fatalf("version = %d, want 1", v)
	}
	checkSegments(t, st, 1, []string{"[0,10)=1"})

	v := st.Set(rat(3, 1), rat(7, 1), 2)
	if v != 2 {
		t.Fatalf("version = %d, want 2", v)
	}
	checkSegments(t, st, 2, []string{"[0,3)=1", "[3,7)=2", "[7,10)=1"})

	// Old snapshot still sees the original segment.
	checkSegments(t, st, 1, []string{"[0,10)=1"})
	checkPoint(t, st, 1, rat(5, 1), 1, true)
	checkPoint(t, st, 2, rat(5, 1), 2, true)
	checkPoint(t, st, 2, rat(2, 1), 1, true)
	checkPoint(t, st, 2, rat(9, 1), 1, true)
	// Half-open boundaries.
	checkPoint(t, st, 2, rat(3, 1), 2, true)
	checkPoint(t, st, 2, rat(7, 1), 1, true)
	checkPoint(t, st, 2, rat(10, 1), 0, false)
	checkPoint(t, st, 2, rat(-1, 1), 0, false)
}

// TestPartialDelete: deleting sub-ranges punches holes and splits segments.
func TestPartialDelete(t *testing.T) {
	st := New[int]()
	st.Set(rat(0, 1), rat(10, 1), 1)
	st.Delete(rat(2, 1), rat(4, 1))
	checkSegments(t, st, 2, []string{"[0,2)=1", "[4,10)=1"})
	st.Delete(rat(6, 1), rat(8, 1))
	checkSegments(t, st, 3, []string{"[0,2)=1", "[4,6)=1", "[8,10)=1"})

	// Holes are really empty, old versions intact.
	checkPoint(t, st, 3, rat(3, 1), 0, false)
	checkPoint(t, st, 3, rat(7, 1), 0, false)
	checkPoint(t, st, 3, rat(5, 1), 1, true)
	checkPoint(t, st, 1, rat(3, 1), 1, true)
	checkPoint(t, st, 1, rat(7, 1), 1, true)

	// Delete spanning several segments and gaps at once.
	st2 := New[int]()
	st2.Set(rat(0, 1), rat(3, 1), 1)
	st2.Set(rat(5, 1), rat(8, 1), 2)
	st2.Set(rat(9, 1), rat(12, 1), 3)
	st2.Delete(rat(1, 1), rat(11, 1))
	checkSegments(t, st2, 4, []string{"[0,1)=1", "[11,12)=3"})
}

// TestSplitThenMerge: overwriting the middle split with the original value
// merges the three segments back into one.
func TestSplitThenMerge(t *testing.T) {
	st := New[int]()
	st.Set(rat(0, 1), rat(10, 1), 1)
	st.Set(rat(3, 1), rat(7, 1), 2)
	checkSegments(t, st, 2, []string{"[0,3)=1", "[3,7)=2", "[7,10)=1"})

	st.Set(rat(3, 1), rat(7, 1), 1)
	checkSegments(t, st, 3, []string{"[0,10)=1"})

	// Partial merge: only one neighbor shares the value.
	st.Set(rat(3, 1), rat(7, 1), 2)
	st.Set(rat(5, 1), rat(9, 1), 1)
	checkSegments(t, st, 5, []string{"[0,3)=1", "[3,5)=2", "[5,10)=1"})

	// Merge across a delete boundary: rewrite the gap with the same value.
	st.Delete(rat(2, 1), rat(6, 1))
	checkSegments(t, st, 6, []string{"[0,2)=1", "[6,10)=1"})
	st.Set(rat(2, 1), rat(6, 1), 1)
	checkSegments(t, st, 7, []string{"[0,10)=1"})
}

// TestEmptyRangeNoVersion: empty or inverted ranges never create versions.
func TestEmptyRangeNoVersion(t *testing.T) {
	st := New[int]()
	st.Set(rat(0, 1), rat(10, 1), 1)
	if got := st.Set(rat(5, 1), rat(5, 1), 2); got != 1 {
		t.Fatalf("empty set created version %d", got)
	}
	if got := st.Delete(rat(3, 1), rat(3, 1)); got != 1 {
		t.Fatalf("empty delete created version %d", got)
	}
	if got := st.Set(rat(7, 1), rat(3, 1), 2); got != 1 {
		t.Fatalf("inverted set created version %d", got)
	}
	if got := st.Delete(rat(9, 1), rat(4, 1)); got != 1 {
		t.Fatalf("inverted delete created version %d", got)
	}
	if st.NumVersions() != 2 {
		t.Fatalf("NumVersions = %d, want 2", st.NumVersions())
	}
	checkSegments(t, st, 1, []string{"[0,10)=1"})
}

// TestRationalEndpoints: endpoints may be arbitrary rationals.
func TestRationalEndpoints(t *testing.T) {
	st := New[int]()
	st.Set(rat(1, 2), rat(7, 3), 1) // [1/2, 7/3)
	checkSegments(t, st, 1, []string{"[1/2,7/3)=1"})

	st.Set(rat(2, 3), rat(3, 2), 2) // splits at 2/3 and 3/2
	checkSegments(t, st, 2, []string{"[1/2,2/3)=1", "[2/3,3/2)=2", "[3/2,7/3)=1"})

	checkPoint(t, st, 2, rat(1, 2), 1, true)
	checkPoint(t, st, 2, rat(2, 3), 2, true)
	checkPoint(t, st, 2, rat(3, 2), 1, true)
	checkPoint(t, st, 2, rat(7, 3), 0, false)
	checkPoint(t, st, 2, rat(1, 3), 0, false)
	checkPoint(t, st, 2, rat(1, 1), 2, true)
	// Old snapshot unchanged.
	checkPoint(t, st, 1, rat(1, 1), 1, true)

	// Deleting the middle rational segment leaves a hole; the neighbors
	// are not adjacent, so no merge happens.
	st.Delete(rat(2, 3), rat(3, 2))
	checkSegments(t, st, 3, []string{"[1/2,2/3)=1", "[3/2,7/3)=1"})

	// Filling the hole with the same value merges all three into one.
	st.Set(rat(2, 3), rat(3, 2), 1)
	checkSegments(t, st, 4, []string{"[1/2,7/3)=1"})
}

// pointModel is the reference model: for every version, the set of integer
// points covered, each mapped to its value.
type pointModel struct {
	versions []map[int]int
}

func newPointModel() *pointModel {
	return &pointModel{versions: []map[int]int{{}}}
}

func (m *pointModel) set(lo, hi *big.Rat, val int) {
	if lo.Cmp(hi) >= 0 {
		return
	}
	next := make(map[int]int, len(m.versions[len(m.versions)-1]))
	for p, v := range m.versions[len(m.versions)-1] {
		next[p] = v
	}
	for p := ceilRat(lo); rat(int64(p), 1).Cmp(hi) < 0; p++ {
		next[p] = val
	}
	m.versions = append(m.versions, next)
}

func (m *pointModel) del(lo, hi *big.Rat) {
	if lo.Cmp(hi) >= 0 {
		return
	}
	next := make(map[int]int, len(m.versions[len(m.versions)-1]))
	for p, v := range m.versions[len(m.versions)-1] {
		next[p] = v
	}
	for p := ceilRat(lo); rat(int64(p), 1).Cmp(hi) < 0; p++ {
		delete(next, p)
	}
	m.versions = append(m.versions, next)
}

// ceilRat returns the smallest integer >= r.
func ceilRat(r *big.Rat) int {
	q := new(big.Int).Quo(r.Num(), r.Denom()) // truncated toward zero
	if new(big.Rat).SetInt(q).Cmp(r) < 0 {
		q.Add(q, big.NewInt(1))
	}
	return int(q.Int64())
}

// TestAgainstPointModel replays random operation sequences (rational
// endpoints with small denominators) on the store and on the per-integer
// point snapshot model, then compares every integer point of every version.
func TestAgainstPointModel(t *testing.T) {
	const (
		trials   = 200
		ops      = 40
		pointMin = -2
		pointMax = 22
	)
	rng := rand.New(rand.NewSource(1))
	for trial := 0; trial < trials; trial++ {
		st := New[int]()
		model := newPointModel()
		for op := 0; op < ops; op++ {
			// Endpoints are multiples of 1/2 within [0, 20]; occasionally
			// equal or inverted to exercise the no-version path.
			a := rat(int64(rng.Intn(41)), 2)
			b := rat(int64(rng.Intn(41)), 2)
			if rng.Intn(10) == 0 {
				b = a
			}
			if rng.Intn(3) == 0 {
				v1 := st.Delete(a, b)
				before := len(model.versions) - 1
				model.del(a, b)
				checkVersionGrowth(t, st, model, v1, before, a, b)
			} else {
				val := rng.Intn(3) // small value space to force merges
				v1 := st.Set(a, b, val)
				before := len(model.versions) - 1
				model.set(a, b, val)
				checkVersionGrowth(t, st, model, v1, before, a, b)
			}
		}
		if st.NumVersions() != len(model.versions) {
			t.Fatalf("trial %d: store has %d versions, model has %d",
				trial, st.NumVersions(), len(model.versions))
		}
		for v := 0; v < st.NumVersions(); v++ {
			for p := pointMin; p <= pointMax; p++ {
				want, wantOK := model.versions[v][p]
				got, gotOK := st.Get(v, rat(int64(p), 1))
				if gotOK != wantOK || (gotOK && got != want) {
					t.Fatalf("trial %d version %d point %d: got (%d,%v), want (%d,%v)",
						trial, v, p, got, gotOK, want, wantOK)
				}
			}
			checkInvariants(t, st, v)
		}
	}
}

func checkVersionGrowth(t *testing.T, st *Store[int], m *pointModel, gotV, modelBefore int, lo, hi *big.Rat) {
	t.Helper()
	wantV := len(m.versions) - 1
	if gotV != wantV {
		t.Fatalf("op [%s,%s): store version %d, model version %d", lo.RatString(), hi.RatString(), gotV, wantV)
	}
	empty := lo.Cmp(hi) >= 0
	if empty && len(m.versions)-1 != modelBefore {
		t.Fatalf("model bug: empty op grew model")
	}
	if empty && st.NumVersions() != len(m.versions) {
		t.Fatalf("empty op [%s,%s) created a store version", lo.RatString(), hi.RatString())
	}
}

// checkInvariants verifies the segment list is sorted, non-overlapping,
// non-empty, and has no adjacent equal-valued segments.
func checkInvariants(t *testing.T, st *Store[int], version int) {
	t.Helper()
	segs := st.Segments(version)
	for i, s := range segs {
		if s.Lo.Cmp(s.Hi) >= 0 {
			t.Fatalf("version %d: degenerate segment %v", version, segStrings(st.Segments(version))[i])
		}
		if i == 0 {
			continue
		}
		prev := segs[i-1]
		if prev.Hi.Cmp(s.Lo) > 0 {
			t.Fatalf("version %d: overlapping segments at %d", version, i)
		}
		if prev.Hi.Cmp(s.Lo) == 0 && prev.Val == s.Val {
			t.Fatalf("version %d: adjacent equal-valued segments at %d", version, i)
		}
	}
}
