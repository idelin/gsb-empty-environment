package incjoin

import (
	"fmt"
	"math/rand"
	"reflect"
	"testing"
)

func sp(s string) *string { return &s }

// applyDeltas folds a delta stream into a multiset, failing if any row
// count would go negative.
func applyDeltas(t *testing.T, acc map[JoinRow]int, deltas []Delta) {
	t.Helper()
	for _, d := range deltas {
		if d.Count == 0 {
			t.Fatalf("zero-count delta for %+v", d.Row)
		}
		acc[d.Row] += d.Count
		if acc[d.Row] < 0 {
			t.Fatalf("delta stream drove count of %+v negative", d.Row)
		}
		if acc[d.Row] == 0 {
			delete(acc, d.Row)
		}
	}
}

func TestNullPadRetractOnFirstRightRow(t *testing.T) {
	j := New()
	d := j.InsertLeft(Row{Key: sp("a"), Val: "l1"})
	want := []Delta{{Row: JoinRow{LKey: NS("a"), LVal: "l1"}, Count: 1}}
	if !reflect.DeepEqual(d, want) {
		t.Fatalf("insert left: got %+v want %+v", d, want)
	}

	d = j.InsertRight(Row{Key: sp("a"), Val: "r1"})
	got := map[JoinRow]int{}
	for _, x := range d {
		got[x.Row] += x.Count
	}
	wantM := map[JoinRow]int{
		{LKey: NS("a"), LVal: "l1"}:                 -1, // null pad retracted
		{LKey: NS("a"), LVal: "l1", RVal: NS("r1")}: 1,  // match added
	}
	if !reflect.DeepEqual(got, wantM) {
		t.Fatalf("0->1 transition: got %+v want %+v", got, wantM)
	}
}

func TestNullPadRestoredOnLastRightDelete(t *testing.T) {
	j := New()
	j.InsertLeft(Row{Key: sp("a"), Val: "l1"})
	j.InsertRight(Row{Key: sp("a"), Val: "r1"})
	j.InsertRight(Row{Key: sp("a"), Val: "r2"})

	// Deleting one of two right rows must not restore the null pad.
	d, err := j.DeleteRight(Row{Key: sp("a"), Val: "r1"})
	if err != nil {
		t.Fatal(err)
	}
	got := map[JoinRow]int{}
	for _, x := range d {
		got[x.Row] += x.Count
	}
	want := map[JoinRow]int{{LKey: NS("a"), LVal: "l1", RVal: NS("r1")}: -1}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("non-last delete: got %+v want %+v", got, want)
	}

	// Deleting the last right row restores the null-padded row.
	d, err = j.DeleteRight(Row{Key: sp("a"), Val: "r2"})
	if err != nil {
		t.Fatal(err)
	}
	got = map[JoinRow]int{}
	for _, x := range d {
		got[x.Row] += x.Count
	}
	want = map[JoinRow]int{
		{LKey: NS("a"), LVal: "l1", RVal: NS("r2")}: -1,
		{LKey: NS("a"), LVal: "l1"}:                 1,
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("1->0 transition: got %+v want %+v", got, want)
	}
}

func TestNullKeysNeverMatch(t *testing.T) {
	j := New()
	acc := map[JoinRow]int{}
	applyDeltas(t, acc, j.InsertLeft(Row{Key: nil, Val: "ln"}))
	// Right row with NULL key must produce no delta at all.
	if d := j.InsertRight(Row{Key: nil, Val: "rn"}); len(d) != 0 {
		t.Fatalf("NULL right insert produced delta %+v", d)
	}
	want := map[JoinRow]int{{LVal: "ln"}: 1}
	if !reflect.DeepEqual(acc, want) {
		t.Fatalf("got %+v want %+v", acc, want)
	}
}

func TestMultiplicity(t *testing.T) {
	j := New()
	acc := map[JoinRow]int{}
	// 2 identical left rows, 3 identical right rows -> count 6.
	for i := 0; i < 2; i++ {
		applyDeltas(t, acc, j.InsertLeft(Row{Key: sp("k"), Val: "l"}))
	}
	for i := 0; i < 3; i++ {
		applyDeltas(t, acc, j.InsertRight(Row{Key: sp("k"), Val: "r"}))
	}
	want := map[JoinRow]int{{LKey: NS("k"), LVal: "l", RVal: NS("r")}: 6}
	if !reflect.DeepEqual(acc, want) {
		t.Fatalf("got %+v want %+v", acc, want)
	}
	// Remove one left copy: 6 -> 3.
	d, err := j.DeleteLeft(Row{Key: sp("k"), Val: "l"})
	if err != nil {
		t.Fatal(err)
	}
	applyDeltas(t, acc, d)
	want[JoinRow{LKey: NS("k"), LVal: "l", RVal: NS("r")}] = 3
	if !reflect.DeepEqual(acc, want) {
		t.Fatalf("after delete: got %+v want %+v", acc, want)
	}
}

func TestDeleteAbsentRowFails(t *testing.T) {
	j := New()
	if _, err := j.DeleteLeft(Row{Key: sp("x"), Val: "l"}); err == nil {
		t.Fatal("expected error deleting absent left row")
	}
	if _, err := j.DeleteRight(Row{Key: sp("x"), Val: "r"}); err == nil {
		t.Fatal("expected error deleting absent right row")
	}
}

// TestRandomEventStream replays random insert/delete workloads, folds every
// emitted delta into an accumulator, and compares both the accumulator and
// the engine's materialized view against a from-scratch full join.
func TestRandomEventStream(t *testing.T) {
	keys := []string{"a", "b", "c"}
	vals := []string{"v1", "v2", "v3"}

	for seed := int64(0); seed < 50; seed++ {
		rng := rand.New(rand.NewSource(seed))
		j := New()
		acc := map[JoinRow]int{}
		var leftRows, rightRows []Row

		remove := func(rows *[]Row, i int) Row {
			r := (*rows)[i]
			*rows = append((*rows)[:i], (*rows)[i+1:]...)
			return r
		}

		for step := 0; step < 400; step++ {
			var key *string
			if rng.Intn(4) != 0 { // 25% NULL keys
				key = sp(keys[rng.Intn(len(keys))])
			}
			row := Row{Key: key, Val: vals[rng.Intn(len(vals))]}
			side := rng.Intn(2)
			canDelete := (side == 0 && len(leftRows) > 0) || (side == 1 && len(rightRows) > 0)

			switch {
			case canDelete && rng.Intn(2) == 0:
				if side == 0 {
					r := remove(&leftRows, rng.Intn(len(leftRows)))
					d, err := j.DeleteLeft(r)
					if err != nil {
						t.Fatalf("seed %d step %d: %v", seed, step, err)
					}
					applyDeltas(t, acc, d)
				} else {
					r := remove(&rightRows, rng.Intn(len(rightRows)))
					d, err := j.DeleteRight(r)
					if err != nil {
						t.Fatalf("seed %d step %d: %v", seed, step, err)
					}
					applyDeltas(t, acc, d)
				}
			default:
				if side == 0 {
					leftRows = append(leftRows, row)
					applyDeltas(t, acc, j.InsertLeft(row))
				} else {
					rightRows = append(rightRows, row)
					applyDeltas(t, acc, j.InsertRight(row))
				}
			}
		}

		want := FullLeftJoin(leftRows, rightRows)
		name := fmt.Sprintf("seed %d", seed)
		if !reflect.DeepEqual(acc, want) {
			t.Fatalf("%s: delta accumulator mismatch\ngot  %+v\nwant %+v", name, acc, want)
		}
		if got := j.Rows(); !reflect.DeepEqual(got, want) {
			t.Fatalf("%s: materialized view mismatch\ngot  %+v\nwant %+v", name, got, want)
		}
	}
}
