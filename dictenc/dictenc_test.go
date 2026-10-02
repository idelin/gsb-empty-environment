package dictenc

import (
	"math/rand"
	"reflect"
	"testing"
)

// statuses is the acceptance-test domain for the status column.
var statuses = []string{"PENDING", "PAID", "REFUNDED"}

func makeStatusColumn(n int) *Column {
	dict := NewDictionary(statuses...)
	values := make([]string, n)
	for i := range values {
		values[i] = statuses[i%len(statuses)]
	}
	return NewColumn(dict, values)
}

func TestDictionaryRoundTrip(t *testing.T) {
	dict := NewDictionary("PENDING", "PAID", "REFUNDED")
	if dict.Len() != 3 {
		t.Fatalf("Len = %d, want 3", dict.Len())
	}
	for i, s := range statuses {
		id, ok := dict.Lookup(s)
		if !ok || int(id) != i {
			t.Fatalf("Lookup(%q) = %d,%v; want %d,true", s, id, ok, i)
		}
		if got := dict.Decode(id); got != s {
			t.Fatalf("Decode(%d) = %q, want %q", id, got, s)
		}
	}
	if _, ok := dict.Lookup("CANCELLED"); ok {
		t.Fatal("Lookup(CANCELLED) should miss")
	}
}

// TestAcceptanceStatusInFilter is the acceptance scenario:
// status IN ('PAID','REFUNDED') over a 1024-byte ID vector must compile to a
// pure integer-table scan and select exactly the PAID/REFUNDED rows.
func TestAcceptanceStatusInFilter(t *testing.T) {
	const rows = 1024 // 1024 rows => 1024-byte dictionary ID array
	col := makeStatusColumn(rows)
	if len(col.IDs) != 1024 {
		t.Fatalf("ID vector = %d bytes, want 1024", len(col.IDs))
	}

	// String predicate pushed down to an integer mask, evaluated vectorized.
	mask := col.In("PAID", "REFUNDED")

	// Expected: rows 1,2,4,5,7,8,... (i%3 != 0).
	want := 0
	for i := 0; i < rows; i++ {
		expect := byte(0)
		if i%3 != 0 {
			expect = 1
			want++
		}
		if mask[i] != expect {
			t.Fatalf("mask[%d] = %d, want %d", i, mask[i], expect)
		}
	}
	if got := mask.Count(); got != want {
		t.Fatalf("selected %d rows, want %d", got, want)
	}

	// Projection stays dictionary-encoded; strings are decoded only at output.
	projected := col.Project(mask)
	if projected.Dict != col.Dict {
		t.Fatal("projection must share the dictionary (no re-encode)")
	}
	got := projected.Materialize()
	wantVals := make([]string, 0, want)
	for i := 0; i < rows; i++ {
		if i%3 != 0 {
			wantVals = append(wantVals, statuses[i%3])
		}
	}
	if !reflect.DeepEqual(got, wantVals) {
		t.Fatalf("materialized output mismatch:\n got %v\nwant %v", got[:8], wantVals[:8])
	}
}

func TestCompareOperators(t *testing.T) {
	col := makeStatusColumn(9) // PENDING,PAID,REFUNDED x3; IDs 0,1,2
	cases := []struct {
		op   Op
		lit  string
		want []byte
	}{
		{EQ, "PAID", []byte{0, 1, 0, 0, 1, 0, 0, 1, 0}},
		{NE, "PAID", []byte{1, 0, 1, 1, 0, 1, 1, 0, 1}},
		{LT, "PAID", []byte{1, 0, 0, 1, 0, 0, 1, 0, 0}},
		{LE, "PAID", []byte{1, 1, 0, 1, 1, 0, 1, 1, 0}},
		{GT, "PAID", []byte{0, 0, 1, 0, 0, 1, 0, 0, 1}},
		{GE, "PAID", []byte{0, 1, 1, 0, 1, 1, 0, 1, 1}},
	}
	for _, tc := range cases {
		got := col.Compare(tc.op, tc.lit)
		if !reflect.DeepEqual([]byte(got), tc.want) {
			t.Errorf("Compare(%v,%q) = %v, want %v", tc.op, tc.lit, []byte(got), tc.want)
		}
	}
}

func TestAbsentLiteralPushdown(t *testing.T) {
	col := makeStatusColumn(4)
	if got := col.Compare(EQ, "CANCELLED").Count(); got != 0 {
		t.Fatalf("EQ missing literal selected %d rows, want 0", got)
	}
	if got := col.Compare(NE, "CANCELLED").Count(); got != 4 {
		t.Fatalf("NE missing literal selected %d rows, want 4", got)
	}
	if got := col.In("CANCELLED", "UNKNOWN").Count(); got != 0 {
		t.Fatalf("IN with only missing literals selected %d rows, want 0", got)
	}
}

// TestEqSWARAgainstNaive verifies the 8-rows-per-iteration SWAR kernel
// against a scalar reference across lengths that exercise the tail loop.
func TestEqSWARAgainstNaive(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	for n := 0; n <= 130; n++ {
		ids := make([]byte, n)
		for i := range ids {
			ids[i] = byte(rng.Intn(4))
		}
		for target := byte(0); target < 4; target++ {
			got := make(Mask, n)
			eqSWAR(ids, target, got)
			for i, v := range ids {
				want := byte(0)
				if v == target {
					want = 1
				}
				if got[i] != want {
					t.Fatalf("n=%d target=%d: eqSWAR[%d]=%d, want %d", n, target, i, got[i], want)
				}
			}
		}
	}
}

func TestProjectEmptyMask(t *testing.T) {
	col := makeStatusColumn(5)
	out := col.Project(make(Mask, 5))
	if out.Len() != 0 {
		t.Fatalf("projected %d rows, want 0", out.Len())
	}
}

func BenchmarkStatusInFilter1024(b *testing.B) {
	col := makeStatusColumn(1024)
	b.SetBytes(int64(len(col.IDs)))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = col.In("PAID", "REFUNDED")
	}
}

func BenchmarkStatusEQ1M(b *testing.B) {
	col := makeStatusColumn(1 << 20)
	b.SetBytes(int64(len(col.IDs)))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = col.Compare(EQ, "PAID")
	}
}
