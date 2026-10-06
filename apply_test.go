package patchmerge

import (
	"math/rand"
	"testing"
)

func TestApplyWithProvenance(t *testing.T) {
	baseline := "hello world"
	a := PatchSet{ID: "a", Edits: []Edit{del(6, 11), ins(0, ">> ")}}
	b := PatchSet{ID: "b", Edits: []Edit{ins(6, "gopher"), ins(11, "!")}}
	res := Merge(a, b)
	if len(res.Conflicts) != 0 {
		t.Fatalf("unexpected conflicts: %+v", res.Conflicts)
	}
	out, prov := Apply(baseline, res.Merged)
	want := ">> hello gopher!"
	if out != want {
		t.Fatalf("output = %q, want %q", out, want)
	}
	if err := VerifyOffsets(baseline, res.Merged, out, prov); err != nil {
		t.Fatalf("offset verification failed: %v", err)
	}
	// Spot-check the per-byte provenance model.
	if !prov[0].Insert || prov[0].Source != "a" {
		t.Fatalf("byte 0 should come from a's insertion: %+v", prov[0])
	}
	if prov[3].Insert || prov[3].BaseOff != 0 {
		t.Fatalf("byte 3 should be baseline offset 0: %+v", prov[3])
	}
	if !prov[9].Insert || prov[9].Source != "b" {
		t.Fatalf("byte 9 should come from b's insertion: %+v", prov[9])
	}
}

func TestMapPosArithmetic(t *testing.T) {
	// Baseline 0..9; delete [2,5), insert "ab" at 1, insert "cd" at 7.
	edits := []MergedEdit{
		{Edit: Edit{Kind: Insert, Pos: 1, End: 1, Text: "ab"}, Sources: []string{"a"}},
		{Edit: Edit{Kind: Delete, Pos: 2, End: 5}, Sources: []string{"a"}},
		{Edit: Edit{Kind: Insert, Pos: 7, End: 7, Text: "cd"}, Sources: []string{"b"}},
	}
	// Output: b0 ins b1 [del] b5 b6 ins b7 b8 b9
	//         0   2-3 4        5  6  7-8 9  10 11
	want := map[int]int{
		0: 0, 1: 1, 2: 4, 3: 4, 4: 4, 5: 4, 6: 5, 7: 6, 8: 9, 9: 10, 10: 11,
	}
	for pos, w := range want {
		if got := MapPos(pos, edits); got != w {
			t.Fatalf("MapPos(%d) = %d, want %d", pos, got, w)
		}
	}
}

// TestRandomMergeApplyVerify generates random internally-consistent patch
// sets, merges them, applies the mergeable edits, and verifies the offset
// translation against the per-byte provenance model.
func TestRandomMergeApplyVerify(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	for iter := 0; iter < 500; iter++ {
		n := rng.Intn(60)
		base := make([]byte, n)
		for i := range base {
			base[i] = byte('a' + rng.Intn(26))
		}
		baseline := string(base)
		a := randomPatchSet(rng, "a", n)
		b := randomPatchSet(rng, "b", n)

		res := Merge(a, b)
		out, prov := Apply(baseline, res.Merged)
		if err := VerifyOffsets(baseline, res.Merged, out, prov); err != nil {
			t.Fatalf("iter %d baseline %q: %v\nmerged: %+v", iter, baseline, err, res.Merged)
		}

		// Every conflicting edit must be excluded from the merged output.
		for _, c := range res.Conflicts {
			for _, m := range res.Merged {
				if m.Kind == Delete && m.Pos <= c.Start && m.End >= c.End && c.Start < c.End {
					t.Fatalf("iter %d: conflicting delete [%d,%d) still merged as %+v", iter, c.Start, c.End, m)
				}
				if m.Kind == Insert && c.Start < m.Pos && m.Pos < c.End {
					if insideAnyConflict(m.Pos, res.Conflicts) {
						t.Fatalf("iter %d: conflicting insert at %d still merged", iter, m.Pos)
					}
				}
			}
		}
	}
}

func insideAnyConflict(pos int, cs []Conflict) bool {
	for _, c := range cs {
		if c.Start < pos && pos < c.End {
			return true
		}
	}
	return false
}

// randomPatchSet builds an internally consistent set: non-overlapping
// deletions, and insertions never strictly inside an own deletion.
func randomPatchSet(rng *rand.Rand, id string, n int) PatchSet {
	var edits []Edit
	var dels [][2]int
	for i := 0; i < rng.Intn(4); i++ {
		s := rng.Intn(n + 1)
		e := s + rng.Intn(n+1-s)
		if e > s {
			dels = append(dels, [2]int{s, e})
		}
	}
	// Resolve overlaps by clipping against previously accepted deletions.
	accepted := dels[:0]
	for _, d := range dels {
		ok := true
		for _, o := range accepted {
			if d[0] < o[1] && o[0] < d[1] {
				ok = false
				break
			}
		}
		if ok {
			accepted = append(accepted, d)
			edits = append(edits, Edit{Kind: Delete, Pos: d[0], End: d[1]})
		}
	}
	for i := 0; i < rng.Intn(5); i++ {
		pos := rng.Intn(n + 1)
		strict := false
		for _, d := range accepted {
			if d[0] < pos && pos < d[1] {
				strict = true
				break
			}
		}
		if strict {
			continue
		}
		l := 1 + rng.Intn(4)
		txt := make([]byte, l)
		for j := range txt {
			txt[j] = byte('A' + rng.Intn(26))
		}
		edits = append(edits, Edit{Kind: Insert, Pos: pos, End: pos, Text: string(txt)})
	}
	return PatchSet{ID: id, Edits: edits}
}
