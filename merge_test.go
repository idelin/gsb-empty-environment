package patchmerge

import (
	"math/rand"
	"reflect"
	"testing"
)

func TestDedupIdenticalInserts(t *testing.T) {
	res, err := Merge(
		PatchSet{ID: "a", Patches: []Patch{InsertAt(3, "xy")}},
		PatchSet{ID: "b", Patches: []Patch{InsertAt(3, "xy")}},
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Merged) != 1 {
		t.Fatalf("expected 1 merged patch, got %d", len(res.Merged))
	}
	if res.Merged[0].Source != "a" {
		t.Fatalf("deduped insert should keep smaller source id, got %q", res.Merged[0].Source)
	}
	if len(res.Conflicts) != 0 {
		t.Fatalf("unexpected conflicts: %+v", res.Conflicts)
	}
}

func TestSamePointDifferentInsertsOrderedBySource(t *testing.T) {
	// Argument order must not matter: ordering is by source id.
	for _, sets := range [][2]PatchSet{
		{
			{ID: "a", Patches: []Patch{InsertAt(2, "A")}},
			{ID: "b", Patches: []Patch{InsertAt(2, "B")}},
		},
		{
			{ID: "b", Patches: []Patch{InsertAt(2, "B")}},
			{ID: "a", Patches: []Patch{InsertAt(2, "A")}},
		},
	} {
		res, err := Merge(sets[0], sets[1])
		if err != nil {
			t.Fatal(err)
		}
		if len(res.Merged) != 2 {
			t.Fatalf("expected 2 merged patches, got %d", len(res.Merged))
		}
		got, err := Apply([]byte("0123"), res.Merged)
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != "01AB23" {
			t.Fatalf("expected source-id ordering 01AB23, got %q", got)
		}
	}
}

func TestDeleteConflictsWithInternalInsert(t *testing.T) {
	res, err := Merge(
		PatchSet{ID: "a", Patches: []Patch{DeleteRange(2, 6)}},
		PatchSet{ID: "b", Patches: []Patch{InsertAt(4, "X")}},
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Conflicts) != 1 {
		t.Fatalf("expected 1 conflict, got %d", len(res.Conflicts))
	}
	c := res.Conflicts[0]
	if c.Start != 2 || c.End != 6 {
		t.Fatalf("conflict range = [%d,%d), want [2,6)", c.Start, c.End)
	}
	if c.DeleteSrc != "a" || c.InsertSrc != "b" {
		t.Fatalf("conflict sources = %q/%q, want a/b", c.DeleteSrc, c.InsertSrc)
	}
	if len(res.Merged) != 0 {
		t.Fatalf("conflicted patches must be withheld from merge, got %+v", res.Merged)
	}
}

func TestEndpointInsertsDoNotConflict(t *testing.T) {
	res, err := Merge(
		PatchSet{ID: "a", Patches: []Patch{DeleteRange(2, 6)}},
		PatchSet{ID: "b", Patches: []Patch{InsertAt(2, "L"), InsertAt(6, "R")}},
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Conflicts) != 0 {
		t.Fatalf("endpoint inserts must not conflict, got %+v", res.Conflicts)
	}
	if len(res.Merged) != 3 {
		t.Fatalf("expected 3 merged patches, got %d", len(res.Merged))
	}
	got, err := Apply([]byte("0123456789"), res.Merged)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "01LR6789" {
		t.Fatalf("got %q, want %q", got, "01LR6789")
	}
}

func TestOverlappingDeletesAreCompatible(t *testing.T) {
	res, err := Merge(
		PatchSet{ID: "a", Patches: []Patch{DeleteRange(1, 4)}},
		PatchSet{ID: "b", Patches: []Patch{DeleteRange(3, 6)}},
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Conflicts) != 0 {
		t.Fatalf("overlapping deletes must not conflict, got %+v", res.Conflicts)
	}
	got, err := Apply([]byte("0123456"), res.Merged)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "06" {
		t.Fatalf("union of deletes: got %q, want %q", got, "06")
	}
}

func TestIdenticalDeletesDedup(t *testing.T) {
	res, err := Merge(
		PatchSet{ID: "a", Patches: []Patch{DeleteRange(1, 3)}},
		PatchSet{ID: "b", Patches: []Patch{DeleteRange(1, 3)}},
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Merged) != 1 || res.Merged[0].Source != "a" {
		t.Fatalf("identical deletes must dedupe, got %+v", res.Merged)
	}
}

func TestSharedInsertStillConflictsWithOtherSetDelete(t *testing.T) {
	// Both sets insert the same text at the same point, but set a also
	// deletes a range containing that point: still a conflict.
	res, err := Merge(
		PatchSet{ID: "a", Patches: []Patch{InsertAt(4, "X"), DeleteRange(2, 6)}},
		PatchSet{ID: "b", Patches: []Patch{InsertAt(4, "X")}},
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Conflicts) != 1 {
		t.Fatalf("expected 1 conflict, got %+v", res.Conflicts)
	}
	if len(res.Merged) != 0 {
		t.Fatalf("conflicted patches must be withheld, got %+v", res.Merged)
	}
}

// token is one byte of the merged document tagged with its source:
// the baseline index it came from, or -1 for inserted bytes.
type token struct {
	b   byte
	src int
}

// applyModel applies patches to a byte-by-byte source model of the
// baseline, tracking the origin of every output byte.
func applyModel(baseline []byte, patches []MergedPatch) []token {
	n := len(baseline)
	insAt := make(map[int][]MergedPatch)
	covered := make([]bool, n)
	for _, p := range patches {
		switch p.Kind {
		case Insert:
			insAt[p.Start] = append(insAt[p.Start], p)
		case Delete:
			for i := p.Start; i < p.End; i++ {
				covered[i] = true
			}
		}
	}
	for _, list := range insAt {
		sortMerged(list)
	}
	var model []token
	for pos := 0; pos <= n; pos++ {
		for _, ip := range insAt[pos] {
			for i := 0; i < len(ip.Text); i++ {
				model = append(model, token{b: ip.Text[i], src: -1})
			}
		}
		if pos == n {
			break
		}
		if !covered[pos] {
			model = append(model, token{b: baseline[pos], src: pos})
		}
	}
	return model
}

// verifySourceModel checks that the analytic offset conversion MapOffset
// agrees with the byte-by-byte source model for every surviving baseline
// byte, and that the model produces the same bytes as Apply.
func verifySourceModel(t *testing.T, baseline []byte, patches []MergedPatch) {
	t.Helper()
	got, err := Apply(baseline, patches)
	if err != nil {
		t.Fatal(err)
	}
	model := applyModel(baseline, patches)
	if len(model) != len(got) {
		t.Fatalf("model length %d != applied length %d", len(model), len(got))
	}
	survivors := 0
	for i, tok := range model {
		if tok.b != got[i] {
			t.Fatalf("model byte %d = %q, applied = %q", i, tok.b, got[i])
		}
		if tok.src >= 0 {
			survivors++
			if mapped := MapOffset(patches, tok.src); mapped != i {
				t.Fatalf("MapOffset(%d) = %d, source model says %d", tok.src, mapped, i)
			}
		}
	}
	// Every surviving baseline byte must appear exactly once in the model.
	seen := make(map[int]int)
	for _, tok := range model {
		if tok.src >= 0 {
			seen[tok.src]++
		}
	}
	for src, count := range seen {
		if count != 1 {
			t.Fatalf("baseline byte %d appears %d times in model", src, count)
		}
	}
}

func TestSourceModelOffsetConversion(t *testing.T) {
	baseline := []byte("hello world")
	res, err := Merge(
		PatchSet{ID: "a", Patches: []Patch{DeleteRange(0, 6), InsertAt(0, "GOODBYE ")}},
		PatchSet{ID: "b", Patches: []Patch{InsertAt(11, "!"), InsertAt(6, "cruel ")}},
	)
	if err != nil {
		t.Fatal(err)
	}
	// Insert at 6 is an endpoint of delete [0,6): no conflict.
	if len(res.Conflicts) != 0 {
		t.Fatalf("unexpected conflicts: %+v", res.Conflicts)
	}
	verifySourceModel(t, baseline, res.Merged)
	got, err := Apply(baseline, res.Merged)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "GOODBYE cruel world!" {
		t.Fatalf("got %q", got)
	}
}

func TestSourceModelRandomized(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	for trial := 0; trial < 500; trial++ {
		n := rng.Intn(40)
		baseline := make([]byte, n)
		for i := range baseline {
			baseline[i] = byte('a' + rng.Intn(26))
		}
		var sets [2]PatchSet
		for s := 0; s < 2; s++ {
			sets[s].ID = string(rune('a' + s))
			for k := 0; k < rng.Intn(5); k++ {
				if rng.Intn(2) == 0 || n == 0 {
					pos := rng.Intn(n + 1)
					text := string(rune('A' + rng.Intn(26)))
					if rng.Intn(2) == 0 {
						text += string(rune('a' + rng.Intn(26)))
					}
					sets[s].Patches = append(sets[s].Patches, InsertAt(pos, text))
				} else {
					start := rng.Intn(n)
					end := start + rng.Intn(n-start+1)
					sets[s].Patches = append(sets[s].Patches, DeleteRange(start, end))
				}
			}
		}
		res, err := Merge(sets[0], sets[1])
		if err != nil {
			t.Fatal(err)
		}
		// Conflicts must reference strictly internal insert points.
		for _, c := range res.Conflicts {
			if !(c.Delete.Start < c.Insert.Start && c.Insert.Start < c.Delete.End) {
				t.Fatalf("trial %d: conflict insert not strictly internal: %+v", trial, c)
			}
			if c.Start != c.Delete.Start || c.End != c.Delete.End {
				t.Fatalf("trial %d: conflict range mismatch: %+v", trial, c)
			}
		}
		// Merged output must contain no cross-set delete/internal-insert pair.
		for _, mp := range res.Merged {
			if mp.Kind != Insert {
				continue
			}
			for _, dp := range res.Merged {
				if dp.Kind == Delete && dp.Source != mp.Source &&
					dp.Start < mp.Start && mp.Start < dp.End {
					t.Fatalf("trial %d: merged output still conflicted: %+v in %+v", trial, mp, dp)
				}
			}
		}
		verifySourceModel(t, baseline, res.Merged)
	}
}

func TestMapOffset(t *testing.T) {
	patches := []MergedPatch{
		{Patch: DeleteRange(2, 5), Source: "a"},   // removes 3 bytes
		{Patch: InsertAt(1, "abcd"), Source: "a"}, // adds 4 bytes before offset 1
		{Patch: InsertAt(8, "xy"), Source: "b"},   // adds 2 bytes before offset 8
	}
	cases := []struct {
		off, want int
	}{
		{0, 0},
		{1, 5},  // insert at 1 shifts byte 1
		{2, 6},  // byte 2 deleted -> position of deletion
		{4, 6},  // inside delete -> clamps to deletion point
		{5, 6},  // first surviving byte after delete
		{8, 11}, // insert at 8 shifts byte 8
		{10, 13},
	}
	for _, c := range cases {
		if got := MapOffset(patches, c.off); got != c.want {
			t.Errorf("MapOffset(%d) = %d, want %d", c.off, got, c.want)
		}
	}
}

func TestMergeResultIsDeterministic(t *testing.T) {
	a := PatchSet{ID: "a", Patches: []Patch{InsertAt(2, "A"), DeleteRange(5, 7), InsertAt(9, "tail")}}
	b := PatchSet{ID: "b", Patches: []Patch{InsertAt(2, "B"), DeleteRange(0, 1), InsertAt(9, "tail")}}
	r1, err := Merge(a, b)
	if err != nil {
		t.Fatal(err)
	}
	r2, err := Merge(b, a)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(r1.Merged, r2.Merged) {
		t.Fatalf("merged output must not depend on argument order:\n%+v\n%+v", r1.Merged, r2.Merged)
	}
}
