package dictagg

import (
	"math/rand"
	"reflect"
	"testing"
)

func assertEqual(t *testing.T, got, want []GroupRow) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("mismatch:\n got: %+v\nwant: %+v", got, want)
	}
}

// Same logical data encoded with two different dictionary orders must give
// identical results, and both must match the fully-decoded naive executor.
func TestGroupSumAcrossDictOrders(t *testing.T) {
	// logical rows: key index -> (key, value); row 2 and 5 have NULL keys,
	// row 4 has a NULL value.
	keys := []string{"banana", "apple", "", "cherry", "apple", "", "banana"}
	keyNull := map[int]bool{2: true, 5: true}
	vals := []int64{1, 10, 99, 5, 20, 88, 3}
	valNull := map[int]bool{4: true}

	dicts := [][]string{
		{"apple", "banana", "cherry"}, // sorted
		{"cherry", "apple", "banana"}, // scrambled
		{"banana", "cherry", "apple"}, // reverse-sorted
	}

	sel := []int{0, 1, 2, 3, 4, 5, 6}
	var reference []GroupRow
	for di, dict := range dicts {
		codeOf := make(map[string]int32)
		for i, s := range dict {
			codeOf[s] = int32(i)
		}
		codes := make([]int32, len(keys))
		var nullRows []int
		for i, k := range keys {
			if keyNull[i] {
				nullRows = append(nullRows, i)
				continue
			}
			codes[i] = codeOf[k]
		}
		kc := NewDictColumn(dict, codes, nullRows...)
		var valNullRows []int
		for i := range vals {
			if valNull[i] {
				valNullRows = append(valNullRows, i)
			}
		}
		vc := NewIntColumn(vals, valNullRows...)

		got := GroupSum(kc, vc, sel)
		naive := NaiveGroupSum(kc, vc, sel)
		assertEqual(t, got, naive)
		if di == 0 {
			reference = got
		} else {
			assertEqual(t, got, reference)
		}
	}

	want := []GroupRow{
		{Key: "apple", Sum: 10, Count: 1, Rows: 2}, // row 4's NULL value excluded
		{Key: "banana", Sum: 4, Count: 2, Rows: 2},
		{Key: "cherry", Sum: 5, Count: 1, Rows: 1},
		{NullKey: true, Sum: 187, Count: 2, Rows: 2},
	}
	assertEqual(t, reference, want)
}

// Groups must be ordered by decoded key, not dictionary code: here the
// dictionary is ["z","m","a"] so code order is the reverse of key order.
func TestSortedByDecodedKeyNotDictCode(t *testing.T) {
	kc := NewDictColumn([]string{"z", "m", "a"}, []int32{0, 1, 2, 0, 2})
	vc := NewIntColumn([]int64{1, 2, 3, 4, 5})
	got := GroupSum(kc, vc, []int{0, 1, 2, 3, 4})
	want := []GroupRow{
		{Key: "a", Sum: 8, Count: 2, Rows: 2},
		{Key: "m", Sum: 2, Count: 1, Rows: 1},
		{Key: "z", Sum: 5, Count: 2, Rows: 2},
	}
	assertEqual(t, got, want)
	assertEqual(t, NaiveGroupSum(kc, vc, []int{0, 1, 2, 3, 4}), want)
}

func TestEmptySelection(t *testing.T) {
	kc := NewDictColumn([]string{"a", "b"}, []int32{0, 1})
	vc := NewIntColumn([]int64{1, 2})
	for _, sel := range [][]int{nil, {}} {
		if got := GroupSum(kc, vc, sel); len(got) != 0 {
			t.Fatalf("expected empty result, got %+v", got)
		}
		if got := NaiveGroupSum(kc, vc, sel); len(got) != 0 {
			t.Fatalf("naive: expected empty result, got %+v", got)
		}
	}
}

// Duplicate row indices in the selection vector must contribute once per
// occurrence.
func TestDuplicateSelection(t *testing.T) {
	kc := NewDictColumn([]string{"a", "b"}, []int32{0, 1, 0})
	vc := NewIntColumn([]int64{1, 2, 3})
	sel := []int{0, 0, 0, 1, 2, 2}
	got := GroupSum(kc, vc, sel)
	want := []GroupRow{
		{Key: "a", Sum: 1*3 + 3*2, Count: 5, Rows: 5},
		{Key: "b", Sum: 2, Count: 1, Rows: 1},
	}
	assertEqual(t, got, want)
	assertEqual(t, NaiveGroupSum(kc, vc, sel), want)
}

// NULL keys form their own group (emitted last); NULL values are excluded
// from Sum/Count but their rows still count toward Rows.
func TestNullHandling(t *testing.T) {
	// rows: 0:"a"=1, 1:NULL key=100, 2:"a"=NULL, 3:NULL key=200, 4:"b"=5
	kc := NewDictColumn([]string{"a", "b"}, []int32{0, 0, 0, 1, 1}, 1, 3)
	vc := NewIntColumn([]int64{1, 100, 999, 200, 5}, 2)
	sel := []int{0, 1, 2, 3, 4}
	got := GroupSum(kc, vc, sel)
	want := []GroupRow{
		{Key: "a", Sum: 1, Count: 1, Rows: 2}, // row 2's NULL value excluded
		{Key: "b", Sum: 5, Count: 1, Rows: 1},
		{NullKey: true, Sum: 300, Count: 2, Rows: 2}, // null-key group last
	}
	assertEqual(t, got, want)
	assertEqual(t, NaiveGroupSum(kc, vc, sel), want)
}

// A selection containing only NULL-key rows yields just the NULL group.
func TestOnlyNullGroup(t *testing.T) {
	kc := NewDictColumn([]string{"a"}, []int32{0, 0, 0}, 0, 2)
	vc := NewIntColumn([]int64{7, 8, 9})
	got := GroupSum(kc, vc, []int{0, 2, 2})
	want := []GroupRow{{NullKey: true, Sum: 25, Count: 3, Rows: 3}}
	assertEqual(t, got, want)
	assertEqual(t, NaiveGroupSum(kc, vc, []int{0, 2, 2}), want)
}

func TestFilters(t *testing.T) {
	kc := NewDictColumn([]string{"x", "y", "z"}, []int32{0, 1, 2, 0, 1}, 2)

	if got := FilterEq(kc, "x"); !reflect.DeepEqual(got, []int{0, 3}) {
		t.Fatalf("FilterEq: %v", got)
	}
	if got := FilterEq(kc, "missing"); len(got) != 0 {
		t.Fatalf("FilterEq missing: %v", got)
	}
	if got := FilterIn(kc, "x", "z"); !reflect.DeepEqual(got, []int{0, 3}) {
		t.Fatalf("FilterIn: %v", got) // row 2 is NULL, must not match "z"
	}
	if got := FilterNotNull(kc); !reflect.DeepEqual(got, []int{0, 1, 3, 4}) {
		t.Fatalf("FilterNotNull: %v", got)
	}
	got := FilterBy(kc, func(k string) bool { return k != "x" })
	if !reflect.DeepEqual(got, []int{1, 4}) {
		t.Fatalf("FilterBy: %v", got)
	}
}

// Filter output feeds the aggregator end to end.
func TestFilterThenGroupSum(t *testing.T) {
	kc := NewDictColumn([]string{"us", "de", "fr"}, []int32{0, 1, 0, 2, 1}, 3)
	vc := NewIntColumn([]int64{10, 20, 30, 40, 50})
	sel := FilterIn(kc, "us", "de")
	got := GroupSum(kc, vc, sel)
	want := []GroupRow{
		{Key: "de", Sum: 70, Count: 2, Rows: 2},
		{Key: "us", Sum: 40, Count: 2, Rows: 2},
	}
	assertEqual(t, got, want)
	assertEqual(t, NaiveGroupSum(kc, vc, sel), want)
}

// Randomized cross-check: random dictionary permutation, random nulls,
// random selection with duplicates; optimized executor must equal naive.
func TestRandomizedCrossCheck(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	for trial := 0; trial < 200; trial++ {
		ndict := 1 + rng.Intn(6)
		dict := make([]string, ndict)
		for i := range dict {
			dict[i] = string(rune('a' + i))
		}
		rng.Shuffle(ndict, func(i, j int) { dict[i], dict[j] = dict[j], dict[i] })

		n := rng.Intn(30)
		codes := make([]int32, n)
		vals := make([]int64, n)
		var keyNulls, valNulls []int
		for i := 0; i < n; i++ {
			codes[i] = int32(rng.Intn(ndict))
			if rng.Intn(4) == 0 {
				keyNulls = append(keyNulls, i)
			}
			vals[i] = int64(rng.Intn(100))
			if rng.Intn(4) == 0 {
				valNulls = append(valNulls, i)
			}
		}
		sel := make([]int, rng.Intn(3*n+1))
		for i := range sel {
			sel[i] = rng.Intn(n + 1) // may hit n -> skip out-of-range guard below
			if sel[i] == n {
				sel[i] = n - 1
			}
		}
		if n == 0 {
			sel = nil
		}

		kc := NewDictColumn(dict, codes, keyNulls...)
		vc := NewIntColumn(vals, valNulls...)
		got := GroupSum(kc, vc, sel)
		want := NaiveGroupSum(kc, vc, sel)
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("trial %d mismatch:\nsel=%v\n got: %+v\nwant: %+v", trial, sel, got, want)
		}
		// groups must be sorted by decoded key with the null group last
		for i := 1; i < len(got); i++ {
			if got[i].NullKey {
				if i != len(got)-1 {
					t.Fatalf("trial %d: null group not last: %+v", trial, got)
				}
			} else if got[i-1].Key >= got[i].Key {
				t.Fatalf("trial %d: keys not sorted: %+v", trial, got)
			}
		}
	}
}
