package dictagg

import (
	"math/rand"
	"reflect"
	"testing"
)

func buildColumn(dict []string, keys []interface{}) *DictColumn {
	codeOf := make(map[string]int32)
	for i, s := range dict {
		codeOf[s] = int32(i)
	}
	col := &DictColumn{
		Dict:  dict,
		Codes: make([]int32, len(keys)),
		Nulls: make([]bool, len(keys)),
	}
	for i, k := range keys {
		if k == nil {
			col.Nulls[i] = true
			continue
		}
		col.Codes[i] = codeOf[k.(string)]
	}
	return col
}

func buildValues(vals []interface{}) *Values {
	out := &Values{V: make([]float64, len(vals)), Nulls: make([]bool, len(vals))}
	for i, v := range vals {
		if v == nil {
			out.Nulls[i] = true
			continue
		}
		out.V[i] = v.(float64)
	}
	return out
}

// The dictionary is deliberately NOT in sorted order ("pear" < "banana" <
// "apple" by code, but "apple" < "banana" < "pear" decoded), so any
// implementation that sorts by dictionary code fails this test.
var testDict = []string{"pear", "banana", "apple"}

func TestGroupBySumSortsByDecodedKeyNotCode(t *testing.T) {
	keys := buildColumn(testDict, []interface{}{
		"pear", "banana", "apple", "pear", "apple",
	})
	vals := buildValues([]interface{}{1.0, 2.0, 3.0, 4.0, 5.0})
	sel := Selection{0, 1, 2, 3, 4}

	got := GroupBySum(keys, vals, sel)
	want := []Group{
		{Key: "apple", HasKey: true, Sum: 8, HasSum: true, Count: 2},
		{Key: "banana", HasKey: true, Sum: 2, HasSum: true, Count: 1},
		{Key: "pear", HasKey: true, Sum: 5, HasSum: true, Count: 2},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}

func TestGroupBySumNullKeyFormsOwnGroupAndIsNotSummed(t *testing.T) {
	keys := buildColumn(testDict, []interface{}{
		"apple", nil, "banana", nil,
	})
	vals := buildValues([]interface{}{10.0, 100.0, 20.0, 200.0})
	sel := Selection{0, 1, 2, 3}

	got := GroupBySum(keys, vals, sel)
	want := []Group{
		// Null group first; the null rows' values (100, 200) are not summed.
		{HasKey: false, Count: 2},
		{Key: "apple", HasKey: true, Sum: 10, HasSum: true, Count: 1},
		{Key: "banana", HasKey: true, Sum: 20, HasSum: true, Count: 1},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}

func TestGroupBySumNullValuesCountedButNotSummed(t *testing.T) {
	keys := buildColumn(testDict, []interface{}{"apple", "apple", "banana"})
	vals := buildValues([]interface{}{nil, nil, 7.0})
	sel := Selection{0, 1, 2}

	got := GroupBySum(keys, vals, sel)
	want := []Group{
		{Key: "apple", HasKey: true, Sum: 0, HasSum: false, Count: 2},
		{Key: "banana", HasKey: true, Sum: 7, HasSum: true, Count: 1},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}

func TestGroupBySumEmptySelection(t *testing.T) {
	keys := buildColumn(testDict, []interface{}{"apple", nil})
	vals := buildValues([]interface{}{1.0, 2.0})

	for _, sel := range []Selection{nil, {}} {
		if got := GroupBySum(keys, vals, sel); len(got) != 0 {
			t.Fatalf("empty selection: got %+v, want no groups", got)
		}
	}
}

func TestGroupBySumDuplicateSelection(t *testing.T) {
	keys := buildColumn(testDict, []interface{}{"apple", nil, "banana"})
	vals := buildValues([]interface{}{1.0, 50.0, 2.0})
	// Row 0 selected three times, null row 1 twice, row 2 once.
	sel := Selection{0, 0, 1, 0, 1, 2}

	got := GroupBySum(keys, vals, sel)
	want := []Group{
		{HasKey: false, Count: 2},
		{Key: "apple", HasKey: true, Sum: 3, HasSum: true, Count: 3},
		{Key: "banana", HasKey: true, Sum: 2, HasSum: true, Count: 1},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}

func TestFilterAppliesSelectionWithDuplicates(t *testing.T) {
	keys := buildColumn(testDict, []interface{}{"apple", nil, "pear"})
	sel := Selection{2, 2, 1, 0}

	got := Filter(keys, sel)
	if got.Len() != 4 {
		t.Fatalf("filtered length = %d, want 4", got.Len())
	}
	wantKeys := []interface{}{"pear", "pear", nil, "apple"}
	for i, w := range wantKeys {
		k, ok := got.KeyAt(i)
		if w == nil {
			if ok {
				t.Fatalf("row %d: got key %q, want null", i, k)
			}
			continue
		}
		if !ok || k != w.(string) {
			t.Fatalf("row %d: got (%q, %v), want %q", i, k, ok, w)
		}
	}
}

// Cross-check the vectorized executor against the fully decoded naive
// executor over randomized inputs: shuffled dictionary orders, random null
// bitmaps, and random selections including empty and duplicate-heavy ones.
func TestGroupBySumMatchesNaive(t *testing.T) {
	rng := rand.New(rand.NewSource(42))

	for trial := 0; trial < 500; trial++ {
		// Random dictionary in a random (usually unsorted) order.
		dictLen := 1 + rng.Intn(8)
		dict := make([]string, dictLen)
		perm := rng.Perm(dictLen)
		for i := range dict {
			dict[i] = string(rune('a' + perm[i]))
		}

		n := rng.Intn(40)
		keys := &DictColumn{
			Dict:  dict,
			Codes: make([]int32, n),
			Nulls: make([]bool, n),
		}
		vals := &Values{V: make([]float64, n), Nulls: make([]bool, n)}
		for i := 0; i < n; i++ {
			if rng.Intn(4) == 0 {
				keys.Nulls[i] = true
			} else {
				keys.Codes[i] = int32(rng.Intn(dictLen))
			}
			if rng.Intn(4) == 0 {
				vals.Nulls[i] = true
			} else {
				vals.V[i] = float64(rng.Intn(1000)) / 8
			}
		}

		// Random selection: sometimes empty, sometimes with many duplicates.
		var sel Selection
		switch rng.Intn(4) {
		case 0:
			sel = Selection{} // empty
		case 1:
			if n > 0 { // heavy duplicates of a few rows
				for j := 0; j < 3*n; j++ {
					sel = append(sel, uint32(rng.Intn(n)))
				}
			}
		default:
			for j := 0; j < rng.Intn(n+1); j++ {
				sel = append(sel, uint32(rng.Intn(n)))
			}
		}

		got := GroupBySum(keys, vals, sel)
		want := NaiveGroupBySum(keys, vals, sel)
		if len(got) == 0 && len(want) == 0 {
			continue
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("trial %d mismatch\ndict=%v\nsel=%v\ngot  %+v\nwant %+v",
				trial, dict, sel, got, want)
		}
	}
}
