package dictcol

import (
	"math/rand"
	"testing"
)

// Acceptance scenario: status column over ['PENDING','PAID','REFUNDED'],
// predicate `status IN ('PAID','REFUNDED')` executed as a vectorized integer
// mask comparison on a compact 1024-byte array (1024 rows, 1 byte/row).
func TestAcceptance_StatusInPaidRefunded_1024Rows(t *testing.T) {
	statuses := []string{"PENDING", "PAID", "REFUNDED"}
	rng := rand.New(rand.NewSource(42))

	raw := make([]string, 1024)
	for i := range raw {
		raw[i] = statuses[rng.Intn(len(statuses))]
	}

	col := Encode(raw)
	if got := len(col.Codes); got != 1024 {
		t.Fatalf("encoded column size = %d bytes, want 1024", got)
	}
	if col.Dict.Len() != 3 {
		t.Fatalf("dictionary size = %d, want 3", col.Dict.Len())
	}

	// Predicate pushdown: strings resolved to integer IDs once.
	paidID := col.Dict.Intern("PAID")
	refundedID := col.Dict.Intern("REFUNDED")
	if paidID < 0 || refundedID < 0 {
		t.Fatal("predicate values must exist in dictionary")
	}

	mask := col.FilterIn("PAID", "REFUNDED")

	// Verify bitmask against row-by-row string semantics.
	want := 0
	for i, s := range raw {
		match := s == "PAID" || s == "REFUNDED"
		if mask.Get(i) != match {
			t.Fatalf("row %d: mask=%v, want %v (status=%s)", i, mask.Get(i), match, s)
		}
		if match {
			want++
		}
	}
	if got := mask.Count(); got != want {
		t.Fatalf("mask count = %d, want %d", got, want)
	}

	// Projection decodes dictionary IDs back to strings only at output.
	out := col.Project(mask)
	if len(out) != want {
		t.Fatalf("projected %d rows, want %d", len(out), want)
	}
	for _, s := range out {
		if s != "PAID" && s != "REFUNDED" {
			t.Fatalf("projected unexpected status %q", s)
		}
	}
}

func TestDictionaryRoundTrip(t *testing.T) {
	d := NewDictionary([]string{"PENDING", "PAID", "REFUNDED", "PAID"})
	if d.Len() != 3 {
		t.Fatalf("Len = %d, want 3", d.Len())
	}
	for _, s := range []string{"PENDING", "PAID", "REFUNDED"} {
		if got := d.Value(d.Intern(s)); got != s {
			t.Fatalf("round trip %q -> %q", s, got)
		}
	}
	if id := d.Intern("CANCELLED"); id != -1 {
		t.Fatalf("Intern(unknown) = %d, want -1", id)
	}
}

func TestFilterIn_TailRows(t *testing.T) {
	// 13 rows: 1 full 8-row word + 5 tail rows exercises the scalar tail.
	raw := []string{
		"PAID", "PENDING", "REFUNDED", "PAID", "PENDING", "PAID", "REFUNDED", "PENDING",
		"PAID", "PENDING", "PENDING", "REFUNDED", "PAID",
	}
	col := Encode(raw)
	mask := col.FilterIn("PAID", "REFUNDED")
	for i, s := range raw {
		want := s == "PAID" || s == "REFUNDED"
		if mask.Get(i) != want {
			t.Fatalf("row %d: mask=%v, want %v", i, mask.Get(i), want)
		}
	}
}

func TestFilterIn_NoMatchAndUnknownValue(t *testing.T) {
	col := Encode([]string{"PENDING", "PENDING", "PENDING"})
	if got := col.FilterIn("PAID").Count(); got != 0 {
		t.Fatalf("Count = %d, want 0", got)
	}
	// Unknown predicate value interns to nothing; result must be empty.
	if got := col.FilterIn("CANCELLED").Count(); got != 0 {
		t.Fatalf("Count = %d, want 0", got)
	}
}

func TestProjectCodes_StaysInIntegerDomain(t *testing.T) {
	col := Encode([]string{"PENDING", "PAID", "REFUNDED", "PAID"})
	mask := col.FilterIn("PAID")
	codes := col.ProjectCodes(mask)
	if len(codes) != 2 {
		t.Fatalf("len = %d, want 2", len(codes))
	}
	paidID := byte(col.Dict.Intern("PAID"))
	for _, code := range codes {
		if code != paidID {
			t.Fatalf("code = %d, want PAID id %d", code, paidID)
		}
	}
}

// Benchmark the vectorized scan over the 1024-byte encoded array.
func BenchmarkFilterIn_1024(b *testing.B) {
	statuses := []string{"PENDING", "PAID", "REFUNDED"}
	rng := rand.New(rand.NewSource(1))
	raw := make([]string, 1024)
	for i := range raw {
		raw[i] = statuses[rng.Intn(3)]
	}
	col := Encode(raw)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = col.FilterIn("PAID", "REFUNDED")
	}
}
