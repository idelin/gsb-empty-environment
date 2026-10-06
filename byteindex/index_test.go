package byteindex

import (
	"reflect"
	"testing"
)

// naiveFindAll is the reference implementation: plain per-position scan.
func naiveFindAll(text, pattern []byte) []int {
	if len(pattern) == 0 {
		pos := make([]int, len(text)+1)
		for i := range pos {
			pos[i] = i
		}
		return pos
	}
	var pos []int
	for i := 0; i+len(pattern) <= len(text); i++ {
		match := true
		for j := 0; j < len(pattern); j++ {
			if text[i+j] != pattern[j] {
				match = false
				break
			}
		}
		if match {
			pos = append(pos, i)
		}
	}
	return pos
}

func equalInts(a, b []int) bool {
	if len(a) == 0 && len(b) == 0 {
		return true
	}
	return reflect.DeepEqual(a, b)
}

func checkAgainstNaive(t *testing.T, s *Snapshot, text []byte, patterns [][]byte) {
	t.Helper()
	for _, p := range patterns {
		got := s.FindAll(p)
		want := naiveFindAll(text, p)
		if !equalInts(got, want) {
			t.Errorf("FindAll(%q) = %v, naive wants %v", p, got, want)
		}
		if len(p) > 0 {
			for k := 1; k < len(got); k++ {
				if got[k] <= got[k-1] {
					t.Errorf("FindAll(%q) not strictly ascending: %v", p, got)
					break
				}
			}
		}
	}
}

func TestCrossAppendBoundary(t *testing.T) {
	ix := New()
	chunks := []string{"abr", "acad", "abra", "abr"}
	full := ""
	for _, c := range chunks {
		ix.Append([]byte(c))
		full += c
	}
	snap := ix.Snapshot()
	defer snap.Release()

	patterns := [][]byte{
		[]byte("abr"),       // inside chunk and across boundary 2/3
		[]byte("cadab"),     // spans chunks 1-2-3
		[]byte("raab"),      // spans boundary 2/3
		[]byte("abracadabraabr"),
		[]byte("aa"),
		[]byte("x"),
		[]byte(""),
	}
	checkAgainstNaive(t, snap, []byte(full), patterns)
}

func TestEmptyPatternAllBoundaries(t *testing.T) {
	ix := New()
	ix.Append([]byte("hello"))
	snap := ix.Snapshot()
	defer snap.Release()

	got := snap.FindAll(nil)
	want := []int{0, 1, 2, 3, 4, 5}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("empty pattern: got %v, want %v", got, want)
	}

	empty := New().Snapshot()
	defer empty.Release()
	if got := empty.FindAll(nil); !reflect.DeepEqual(got, []int{0}) {
		t.Fatalf("empty index, empty pattern: got %v, want [0]", got)
	}
	if got := empty.FindAll([]byte("a")); got != nil {
		t.Fatalf("empty index, non-empty pattern: got %v, want nil", got)
	}
}

func TestSnapshotIsolation(t *testing.T) {
	ix := New()
	ix.Append([]byte("banana"))
	old := ix.Snapshot()
	defer old.Release()

	ix.Append([]byte("banana")) // index is now "bananabanana"

	if got := old.Len(); got != 6 {
		t.Fatalf("old snapshot Len = %d, want 6", got)
	}
	// Old snapshot must not see the new suffix: "ana" occurs at 1,3 only,
	// not at 7,9, and the cross-boundary "ab" at 5 must be invisible.
	checkAgainstNaive(t, old, []byte("banana"), [][]byte{
		[]byte("ana"),
		[]byte("ab"),
		[]byte("bananabanana"),
	})

	fresh := ix.Snapshot()
	defer fresh.Release()
	checkAgainstNaive(t, fresh, []byte("bananabanana"), [][]byte{
		[]byte("ana"),
		[]byte("ab"),
		[]byte("bananabanana"),
	})
}

func TestSnapshotRelease(t *testing.T) {
	ix := New()
	ix.Append([]byte("data"))
	snap := ix.Snapshot()

	if got := snap.FindAll([]byte("at")); !reflect.DeepEqual(got, []int{1}) {
		t.Fatalf("before release: got %v, want [1]", got)
	}

	snap.Release()
	if got := snap.FindAll([]byte("at")); got != nil {
		t.Fatalf("after release: got %v, want nil", got)
	}
	if got := snap.FindAll(nil); got != nil {
		t.Fatalf("after release, empty pattern: got %v, want nil", got)
	}
	if got := snap.Len(); got != 0 {
		t.Fatalf("after release: Len = %d, want 0", got)
	}

	snap.Release() // idempotent, must not panic

	// Releasing one snapshot must not affect others.
	other := ix.Snapshot()
	defer other.Release()
	if got := other.FindAll([]byte("at")); !reflect.DeepEqual(got, []int{1}) {
		t.Fatalf("unaffected snapshot: got %v, want [1]", got)
	}
}

func TestOverlappingOccurrences(t *testing.T) {
	ix := New()
	ix.Append([]byte("aaaaa"))
	snap := ix.Snapshot()
	defer snap.Release()

	if got := snap.FindAll([]byte("aa")); !reflect.DeepEqual(got, []int{0, 1, 2, 3}) {
		t.Fatalf("overlapping: got %v, want [0 1 2 3]", got)
	}
}

func TestFuzzAgainstNaive(t *testing.T) {
	alphabet := []byte("ab")
	seed := uint32(42)
	rand := func(n int) []byte {
		b := make([]byte, n)
		for i := range b {
			seed = seed*1664525 + 1013904223
			b[i] = alphabet[seed>>31]
		}
		return b
	}

	ix := New()
	var full []byte
	var snaps []*Snapshot
	var snapLens []int
	defer func() {
		for _, s := range snaps {
			s.Release()
		}
	}()

	for step := 0; step < 50; step++ {
		chunk := rand(1 + step%7)
		ix.Append(chunk)
		full = append(full, chunk...)
		if step%3 == 0 {
			snaps = append(snaps, ix.Snapshot())
			snapLens = append(snapLens, len(full))
		}
	}

	patterns := [][]byte{nil, {}, []byte("a"), []byte("b"), []byte("aa"), []byte("ab"), []byte("aba"), []byte("bab"), []byte("aabb")}
	for i, s := range snaps {
		checkAgainstNaive(t, s, full[:snapLens[i]], patterns)
	}
}
