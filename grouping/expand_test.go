package grouping

import (
	"reflect"
	"testing"
)

func setsEqual(got, want [][]string) bool {
	norm := func(sets [][]string) [][]string {
		out := make([][]string, len(sets))
		for i, s := range sets {
			if s == nil {
				s = []string{}
			}
			out[i] = s
		}
		return out
	}
	return reflect.DeepEqual(norm(got), norm(want))
}

func TestExpandRollupCompositeTupleCartesian(t *testing.T) {
	// GROUP BY ROLLUP(a, (b, c)), GROUPING SETS((d), ()):
	// (b, c) is atomic, so ROLLUP yields 3 sets (never 4), and the
	// cartesian product with [(d), ()] yields exactly 6 sets.
	got := Expand([]Element{
		Rollup(Col("a"), Tuple("b", "c")),
		GroupingSets([]string{"d"}, []string{}),
	}, false)
	want := [][]string{
		{"a", "b", "c", "d"},
		{"a", "b", "c"},
		{"a", "d"},
		{"a"},
		{"d"},
		{},
	}
	if !setsEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestExpandCubeCompositeTuple(t *testing.T) {
	// GROUP BY CUBE(a, (b, c)): the tuple stays atomic.
	got := Expand([]Element{Cube(Col("a"), Tuple("b", "c"))}, false)
	want := [][]string{
		{"a", "b", "c"},
		{"a"},
		{"b", "c"},
		{},
	}
	if !setsEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestExpandNestedAndColumnDedup(t *testing.T) {
	// Nested elements and duplicate columns inside one set: duplicates
	// collapse in canonical (first-appearance) order.
	got := Expand([]Element{
		Rollup(Col("a"), Rollup(Col("b"), Col("a"))),
	}, false)
	// ROLLUP(b, a) -> [(b a), (b), ()]
	// ROLLUP(a, X) -> [a]x[X] ++ [(a), ()]
	want := [][]string{
		{"a", "b"},
		{"a", "b"},
		{"a"},
		{"a"},
		{},
	}
	if !setsEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestAllVsDistinctDuplicateSets(t *testing.T) {
	// GROUP BY CUBE(a), ROLLUP(a): the cartesian product of
	// [(a), ()] x [(a), ()] yields (a) three times and () once.
	all := Expand([]Element{Cube(Col("a")), Rollup(Col("a"))}, false)
	wantAll := [][]string{{"a"}, {"a"}, {"a"}, {}}
	if !setsEqual(all, wantAll) {
		t.Fatalf("ALL: got %v, want %v", all, wantAll)
	}
	distinct := Expand([]Element{Cube(Col("a")), Rollup(Col("a"))}, true)
	wantDistinct := [][]string{{"a"}, {}}
	if !setsEqual(distinct, wantDistinct) {
		t.Fatalf("DISTINCT: got %v, want %v", distinct, wantDistinct)
	}
}

func TestAllVsDistinctOutputRows(t *testing.T) {
	input := []Row{{"a": "p"}, {"a": "q"}}
	q := Query{GroupBy: []Element{Cube(Col("a")), Rollup(Col("a"))},
		Aggregates: []Aggregate{{Name: "c", Func: Count}}}

	all := Execute(input, q)
	if got := len(all.AllRows()); got != 7 { // 3 sets x 2 keys + 1 total
		t.Fatalf("ALL row count = %d, want 7", got)
	}
	// The three (a) sets emit identical rows.
	for i := 0; i < 3; i++ {
		if all.Sets[i].Set[0] != "a" || len(all.Sets[i].Rows) != 2 {
			t.Fatalf("set %d = %v with %d rows", i, all.Sets[i].Set, len(all.Sets[i].Rows))
		}
	}
	if !reflect.DeepEqual(all.Sets[0].Rows, all.Sets[1].Rows) ||
		!reflect.DeepEqual(all.Sets[1].Rows, all.Sets[2].Rows) {
		t.Fatalf("duplicate (a) sets must emit duplicate rows: %v", all.Sets)
	}

	q.DistinctSets = true
	d := Execute(input, q)
	if got := len(d.AllRows()); got != 3 { // 2 keys + 1 total
		t.Fatalf("DISTINCT row count = %d, want 3", got)
	}
}
