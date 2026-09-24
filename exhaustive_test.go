package dod

import (
	"math"
	"testing"
)

// bruteForceMin enumerates every partition of the sequence into blocks and
// returns the exact minimal total cost (block bytes + index overhead).
func bruteForceMin(ts, vs []int64) int {
	n := len(ts)
	best := math.MaxInt32
	var rec func(i, acc int)
	rec = func(i, acc int) {
		if i == n {
			if acc < best {
				best = acc
			}
			return
		}
		for j := i + 1; j <= n; j++ {
			blk, err := encodeBlock(0, ts[i:j], vs[i:j])
			if err != nil {
				continue
			}
			rec(j, acc+len(blk)+indexEntryBytes)
		}
	}
	rec(0, 0)
	return best
}

// Exhaustively verify short sequences: round-trip equality, and that the
// joint optimizer achieves the brute-force minimal format overhead.
func TestExhaustiveShortSequences(t *testing.T) {
	steps := []int64{1, 2, 3}
	vals := []int64{-2, -1, 0, 1, 2}
	const maxLen = 4
	checked := 0
	var rec func(ts, vs []int64)
	rec = func(ts, vs []int64) {
		if len(ts) > 0 {
			checkOne(t, ts, vs)
			checked++
		}
		if len(ts) == maxLen {
			return
		}
		if len(ts) == 0 {
			for _, v := range vals {
				rec([]int64{1000}, []int64{v})
			}
			return
		}
		for _, s := range steps {
			for _, v := range vals {
				nt := append(append([]int64{}, ts...), ts[len(ts)-1]+s)
				nv := append(append([]int64{}, vs...), v)
				rec(nt, nv)
			}
		}
	}
	rec(nil, nil)
	t.Logf("exhaustively verified %d sequences", checked)
}

func checkOne(t *testing.T, ts, vs []int64) {
	t.Helper()
	st := &MemStore{}
	a := NewArchive(st, RejectRegression)
	for i := range ts {
		if err := a.Add(ts[i], vs[i]); err != nil {
			t.Fatal(err)
		}
	}
	if err := a.Flush(); err != nil {
		t.Fatal(err)
	}
	// Round trip.
	gotT, gotV, err := a.ReadRange(math.MinInt64, math.MaxInt64, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	if !eqI64(gotT, ts) || !eqI64(gotV, vs) {
		t.Fatalf("round trip mismatch for %v/%v", ts, vs)
	}
	// Optimality: total cost equals brute-force minimum.
	total := 0
	for _, e := range a.Index().Entries {
		total += int(e.Length) + indexEntryBytes
	}
	if min := bruteForceMin(ts, vs); total != min {
		t.Fatalf("seq %v/%v: optimizer cost %d, brute-force min %d", ts, vs, total, min)
	}
	// Minimal overhead: actual encoded bytes never worse than a fixed
	// 64-bit dod reference format.
	actual := 0
	for _, e := range a.Index().Entries {
		actual += int(e.Length)
	}
	if actual > referenceSize(ts) {
		t.Fatalf("encoded %d exceeds reference %d for %v", actual, referenceSize(ts), ts)
	}
}

// referenceSize is the size of a trivial independent reference encoding:
// count + t0 + v0 as varints, then every delta and dod as a fixed 8 bytes.
func referenceSize(ts []int64) int {
	n := len(ts)
	size := uvlen(uint64(n)) + 16 // t0, v0 raw
	if n >= 2 {
		size += 16
	}
	if n >= 3 {
		size += 16 * (n - 2)
	}
	return size
}
