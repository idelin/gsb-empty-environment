package byteindex

import (
	"math/rand"
	"reflect"
	"testing"
)

// naiveFind is the reference oracle: plain per-position substring search.
func naiveFind(data, pattern []byte) []int {
	var hits []int
	if len(pattern) == 0 {
		for i := 0; i <= len(data); i++ {
			hits = append(hits, i)
		}
		return hits
	}
	for i := 0; i+len(pattern) <= len(data); i++ {
		match := true
		for j := 0; j < len(pattern); j++ {
			if data[i+j] != pattern[j] {
				match = false
				break
			}
		}
		if match {
			hits = append(hits, i)
		}
	}
	return hits
}

func build(t *testing.T, chunks ...[]byte) (*Index, *Snapshot) {
	t.Helper()
	var idx Index
	for _, c := range chunks {
		idx.Append(c)
	}
	return &idx, idx.Freeze()
}

func checkFind(t *testing.T, snap *Snapshot, data, pattern []byte) {
	t.Helper()
	got := snap.Find(pattern)
	want := naiveFind(data, pattern)
	if len(got) == 0 && len(want) == 0 {
		return
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Find(%q) = %v, want %v", pattern, got, want)
	}
	for i := 1; i < len(got); i++ {
		if got[i] <= got[i-1] {
			t.Fatalf("Find(%q) results not strictly increasing: %v", pattern, got)
		}
	}
}

func concat(chunks ...[]byte) []byte {
	var out []byte
	for _, c := range chunks {
		out = append(out, c...)
	}
	return out
}

func TestCrossAppendBoundary(t *testing.T) {
	chunks := [][]byte{
		[]byte("abc"),
		[]byte("abd"),
		[]byte("abcabd"),
		[]byte("xabc"),
	}
	data := concat(chunks...)
	_, snap := build(t, chunks...)
	// Patterns spanning every append boundary, plus overlapping repeats.
	patterns := [][]byte{
		[]byte("abcabd"),     // spans boundary 0|1
		[]byte("abdabc"),     // spans boundary 1|2
		[]byte("abcabdxabc"), // spans all boundaries
		[]byte("abc"),        // overlapping-ish repeats across chunks
		[]byte("cabd"),
		[]byte("xabc"),
		[]byte("abcx"), // absent
	}
	for _, p := range patterns {
		checkFind(t, snap, data, p)
	}
}

func TestOverlappingMatches(t *testing.T) {
	data := []byte("aaaa")
	_, snap := build(t, data)
	checkFind(t, snap, data, []byte("aa"))  // want 0,1,2
	checkFind(t, snap, data, []byte("aaa")) // want 0,1
	checkFind(t, snap, data, []byte("aaaaa"))
}

func TestEmptyPatternAllBoundaries(t *testing.T) {
	data := []byte("hello")
	_, snap := build(t, data)
	got := snap.Find(nil)
	want := make([]int, len(data)+1)
	for i := range want {
		want[i] = i
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Find(nil) = %v, want %v", got, want)
	}
	// Empty snapshot, empty pattern: single boundary at 0.
	var idx Index
	emptySnap := idx.Freeze()
	if got := emptySnap.Find(nil); !reflect.DeepEqual(got, []int{0}) {
		t.Fatalf("Find(nil) on empty snapshot = %v, want [0]", got)
	}
}

func TestSnapshotIsolation(t *testing.T) {
	var idx Index
	idx.Append([]byte("foo"))
	snap1 := idx.Freeze()
	idx.Append([]byte("bar"))
	snap2 := idx.Freeze()
	idx.Append([]byte("foobar"))
	snap3 := idx.Freeze()

	// Old snapshots must not see the new suffixes.
	if got := snap1.Find([]byte("bar")); len(got) != 0 {
		t.Fatalf("snap1 sees appended suffix: %v", got)
	}
	if got := snap1.Find([]byte("foobar")); len(got) != 0 {
		t.Fatalf("snap1 sees cross-append match: %v", got)
	}
	if got := snap2.Find([]byte("barfoobar")); len(got) != 0 {
		t.Fatalf("snap2 sees later appended match: %v", got)
	}
	checkFind(t, snap1, []byte("foo"), []byte("foo"))
	checkFind(t, snap2, []byte("foobar"), []byte("foobar"))
	checkFind(t, snap3, []byte("foobarfoobar"), []byte("foobar"))
	if snap1.Len() != 3 || snap2.Len() != 6 || snap3.Len() != 12 {
		t.Fatalf("snapshot lengths = %d,%d,%d, want 3,6,12",
			snap1.Len(), snap2.Len(), snap3.Len())
	}
}

func TestSnapshotRelease(t *testing.T) {
	var idx Index
	idx.Append([]byte("data"))
	snap := idx.Freeze()
	snap.Release()
	defer func() {
		if recover() == nil {
			t.Fatal("Find on released snapshot did not panic")
		}
	}()
	snap.Find([]byte("data"))
}

func TestRandomizedAgainstNaive(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	for trial := 0; trial < 200; trial++ {
		var idx Index
		var data []byte
		var snaps []*Snapshot
		var snapData [][]byte
		// Random sequence of appends and freezes over a tiny alphabet
		// to force many overlapping and cross-boundary matches.
		for step := 0; step < 10; step++ {
			n := rng.Intn(8)
			chunk := make([]byte, n)
			for i := range chunk {
				chunk[i] = byte('a' + rng.Intn(3))
			}
			idx.Append(chunk)
			data = append(data, chunk...)
			if rng.Intn(2) == 0 {
				snaps = append(snaps, idx.Freeze())
				frozen := make([]byte, len(data))
				copy(frozen, data)
				snapData = append(snapData, frozen)
			}
		}
		for si, snap := range snaps {
			frozen := snapData[si]
			if snap.Len() != len(frozen) {
				t.Fatalf("trial %d snap %d: Len = %d, want %d",
					trial, si, snap.Len(), len(frozen))
			}
			for q := 0; q < 5; q++ {
				m := rng.Intn(6)
				pat := make([]byte, m)
				for i := range pat {
					pat[i] = byte('a' + rng.Intn(3))
				}
				checkFind(t, snap, frozen, pat)
			}
		}
	}
}
