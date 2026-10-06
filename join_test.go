package incjoin

import (
	"fmt"
	"math/rand"
	"testing"
)

type event struct {
	op  string // "IL", "DL", "IR", "DR"
	row Row
}

func run(t *testing.T, events []event) (map[OutRow]int, []Row, []Row) {
	t.Helper()
	j := New()
	out := make(map[OutRow]int)
	var left, right []Row
	remove := func(rows []Row, r Row) []Row {
		for i, x := range rows {
			if x == r {
				return append(rows[:i], rows[i+1:]...)
			}
		}
		t.Fatalf("remove of absent row %+v", r)
		return nil
	}
	for i, ev := range events {
		var deltas []Delta
		var err error
		switch ev.op {
		case "IL":
			deltas = j.InsertLeft(ev.row)
			left = append(left, ev.row)
		case "DL":
			deltas, err = j.DeleteLeft(ev.row)
			left = remove(left, ev.row)
		case "IR":
			deltas = j.InsertRight(ev.row)
			right = append(right, ev.row)
		case "DR":
			deltas, err = j.DeleteRight(ev.row)
			right = remove(right, ev.row)
		}
		if err != nil {
			t.Fatalf("event %d (%s %+v): %v", i, ev.op, ev.row, err)
		}
		Apply(out, deltas)
		want := FullJoin(left, right)
		if !equalMultiset(out, want) {
			t.Fatalf("after event %d (%s %+v):\n got %v\nwant %v", i, ev.op, ev.row, out, want)
		}
	}
	return out, left, right
}

func equalMultiset(a, b map[OutRow]int) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}

func lr(key, payload string) Row { return Row{Key: key, HasKey: true, Payload: payload} }
func nullRow(payload string) Row { return Row{Payload: payload} }

// TestZeroToOneRetract: right side going 0 -> 1 must retract the null-padded
// row; deleting the last right row must restore it.
func TestZeroToOneRetract(t *testing.T) {
	out, _, _ := run(t, []event{
		{"IL", lr("a", "L1")},
		{"IR", lr("a", "R1")}, // retract (L1,NULL), emit (L1,R1)
		{"DR", lr("a", "R1")}, // retract (L1,R1), restore (L1,NULL)
	})
	want := map[OutRow]int{{Left: lr("a", "L1")}: 1}
	if !equalMultiset(out, want) {
		t.Fatalf("got %v want %v", out, want)
	}
}

// TestMultiplicity: duplicate rows on both sides multiply out.
func TestMultiplicity(t *testing.T) {
	out, _, _ := run(t, []event{
		{"IL", lr("a", "L1")},
		{"IL", lr("a", "L1")}, // same row twice
		{"IL", lr("a", "L2")},
		{"IR", lr("a", "R1")},
		{"IR", lr("a", "R1")}, // duplicate right row
		{"IR", lr("a", "R2")},
		{"DL", lr("a", "L1")}, // one copy of L1 left
		{"DR", lr("a", "R1")}, // one copy of R1 left
	})
	want := map[OutRow]int{
		{Left: lr("a", "L1"), Right: lr("a", "R1"), HasRight: true}: 1,
		{Left: lr("a", "L1"), Right: lr("a", "R2"), HasRight: true}: 1,
		{Left: lr("a", "L2"), Right: lr("a", "R1"), HasRight: true}: 1,
		{Left: lr("a", "L2"), Right: lr("a", "R2"), HasRight: true}: 1,
	}
	if !equalMultiset(out, want) {
		t.Fatalf("got %v want %v", out, want)
	}
}

// TestNullKeysNeverMatch: null keys on either side never join; left null-key
// rows always produce null-padded output.
func TestNullKeysNeverMatch(t *testing.T) {
	out, _, _ := run(t, []event{
		{"IL", nullRow("LN")},
		{"IR", nullRow("RN")}, // must not match LN
		{"IL", lr("a", "L1")},
		{"IR", lr("a", "R1")},
		{"DR", nullRow("RN")},
	})
	want := map[OutRow]int{
		{Left: nullRow("LN")}: 1,
		{Left: lr("a", "L1"), Right: lr("a", "R1"), HasRight: true}: 1,
	}
	if !equalMultiset(out, want) {
		t.Fatalf("got %v want %v", out, want)
	}
}

// TestDeleteAbsentRowErrors.
func TestDeleteAbsentRowErrors(t *testing.T) {
	j := New()
	if _, err := j.DeleteLeft(lr("a", "L1")); err == nil {
		t.Fatal("expected error deleting absent left row")
	}
	if _, err := j.DeleteRight(nullRow("RN")); err == nil {
		t.Fatal("expected error deleting absent null-key right row")
	}
}

// TestRandomized: stream random inserts/deletes, apply each event's deltas,
// and compare against a full join after every event and at the end.
func TestRandomized(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	j := New()
	out := make(map[OutRow]int)
	leftCount := make(map[Row]int)
	rightCount := make(map[Row]int)

	rows := make([]Row, 0, 12)
	for _, k := range []string{"a", "b", "c"} {
		for _, p := range []string{"p1", "p2"} {
			rows = append(rows, lr(k, p))
		}
	}
	rows = append(rows, nullRow("n1"), nullRow("n2"))

	var leftList, rightList []Row
	drop := func(list []Row, r Row) []Row {
		for i, x := range list {
			if x == r {
				return append(list[:i], list[i+1:]...)
			}
		}
		t.Fatalf("drop of absent row %+v", r)
		return nil
	}

	for step := 0; step < 4000; step++ {
		r := rows[rng.Intn(len(rows))]
		var deltas []Delta
		var err error
		var desc string
		switch rng.Intn(4) {
		case 0:
			deltas = j.InsertLeft(r)
			leftCount[r]++
			leftList = append(leftList, r)
			desc = "IL"
		case 1:
			deltas = j.InsertRight(r)
			rightCount[r]++
			rightList = append(rightList, r)
			desc = "IR"
		case 2:
			if leftCount[r] == 0 {
				continue
			}
			deltas, err = j.DeleteLeft(r)
			leftCount[r]--
			leftList = drop(leftList, r)
			desc = "DL"
		case 3:
			if rightCount[r] == 0 {
				continue
			}
			deltas, err = j.DeleteRight(r)
			rightCount[r]--
			rightList = drop(rightList, r)
			desc = "DR"
		}
		if err != nil {
			t.Fatalf("step %d (%s %+v): %v", step, desc, r, err)
		}
		Apply(out, deltas)
		if want := FullJoin(leftList, rightList); !equalMultiset(out, want) {
			t.Fatalf("step %d (%s %+v):\n got %v\nwant %v", step, desc, r, out, want)
		}
	}

	// Final explicit comparison of accumulated deltas vs full join.
	if want := FullJoin(leftList, rightList); !equalMultiset(out, want) {
		t.Fatalf("final mismatch:\n got %v\nwant %v", out, want)
	}
	fmt.Printf("final result rows: %d distinct\n", len(out))
}
