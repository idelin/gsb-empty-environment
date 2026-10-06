package infer

import (
	"strings"
	"testing"
)

func mustAdd(t *testing.T, inf *Inferencer, records ...[]string) {
	t.Helper()
	for _, rec := range records {
		if err := inf.AddRecord(rec); err != nil {
			t.Fatalf("AddRecord(%v): %v", rec, err)
		}
	}
}

func mustRemove(t *testing.T, inf *Inferencer, records ...[]string) {
	t.Helper()
	for _, rec := range records {
		if err := inf.RemoveRecord(rec); err != nil {
			t.Fatalf("RemoveRecord(%v): %v", rec, err)
		}
	}
}

func TestBasicTypes(t *testing.T) {
	inf := New([]string{"flag", "count", "ratio", "name"})
	mustAdd(t, inf,
		[]string{"true", "1", "3.14", "alice"},
		[]string{"false", "-42", "0.1", "bob"},
		[]string{"true", "0", "1e5", "carol"},
	)
	got := inf.Types()
	want := []Type{TypeBool, TypeInt, TypeRational, TypeText}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("column %d: got %s, want %s", i, got[i], want[i])
		}
	}
}

func TestEmptyStringDefaultsToText(t *testing.T) {
	inf := New([]string{"n"})
	mustAdd(t, inf, []string{"1"}, []string{"2"}, []string{""})
	if got := inf.Types()[0]; got != TypeText {
		t.Fatalf("empty string should force text by default, got %s", got)
	}
	c := inf.Column(0).Counts()
	if c.Text != 1 || c.Int != 2 || c.Null != 0 {
		t.Errorf("counts = %+v, want Text=1 Int=2 Null=0", c)
	}
}

func TestEmptyAsNull(t *testing.T) {
	inf := New([]string{"n"}, WithEmptyAsNull())
	mustAdd(t, inf, []string{"1"}, []string{"2"}, []string{""})
	if got := inf.Types()[0]; got != TypeInt {
		t.Fatalf("nulls should not affect type, got %s", got)
	}
	c := inf.Column(0).Counts()
	if c.Null != 1 || c.Int != 2 {
		t.Errorf("counts = %+v, want Null=1 Int=2", c)
	}
}

func TestUndoNarrows(t *testing.T) {
	inf := New([]string{"v"})
	steps := []struct {
		rec  []string
		want Type // type while the record is present
	}{
		{[]string{"true"}, TypeBool},
		{[]string{"1"}, TypeInt},
		{[]string{"3.5"}, TypeRational},
		{[]string{"x"}, TypeText},
		{[]string{""}, TypeText}, // empty is text by default
	}
	for _, s := range steps {
		mustAdd(t, inf, s.rec)
		if got := inf.Types()[0]; got != s.want {
			t.Fatalf("after adding %v: got %s, want %s", s.rec, got, s.want)
		}
	}
	// Undo in reverse: each removal narrows the type again.
	wants := []Type{TypeText, TypeRational, TypeInt, TypeBool, TypeText}
	for i := len(steps) - 1; i >= 0; i-- {
		mustRemove(t, inf, steps[i].rec)
		if got := inf.Types()[0]; got != wants[len(steps)-1-i] {
			t.Fatalf("after removing %v: got %s, want %s", steps[i].rec, got, wants[len(steps)-1-i])
		}
	}
	if c := inf.Column(0).Counts(); c.Total() != 0 || c.Null != 0 {
		t.Errorf("counts after full undo = %+v, want all zero", c)
	}
}

func TestEvidenceCounts(t *testing.T) {
	inf := New([]string{"v"})
	mustAdd(t, inf,
		[]string{"true"}, []string{"false"},
		[]string{"7"},
		[]string{"2.5"},
		[]string{"hi"}, []string{""},
	)
	c := inf.Column(0).Counts()
	want := Counts{Bool: 2, Int: 1, Rational: 1, Text: 2}
	if c != want {
		t.Fatalf("counts = %+v, want %+v", c, want)
	}
	mustRemove(t, inf, []string{"hi"})
	if c := inf.Column(0).Counts(); c.Text != 1 {
		t.Errorf("after undo: Text = %d, want 1", c.Text)
	}
}

func TestClassification(t *testing.T) {
	cases := []struct {
		in   string
		want kind
	}{
		{"", kindEmpty},
		{"true", kindBool},
		{"false", kindBool},
		{"True", kindText}, // non-canonical case
		{"TRUE", kindText},
		{"0", kindInt},
		{"-1", kindInt},
		{"42", kindInt},
		{"9223372036854775807", kindInt}, // max int64
		{"-0", kindText},                 // canonical form is "0"
		{"00", kindText},                 // leading zero
		{"01", kindText},                 // leading zero
		{"007", kindText},                // leading zero
		{"01234", kindText},              // zip-code style
		{"+5", kindText},
		{" 5", kindText},
		{"5 ", kindText},
		{"1_000", kindText},
		{"3.14", kindRational},
		{"0.1", kindRational},
		{"-0.5", kindRational},
		{".5", kindRational},
		{"1.", kindRational},
		{"1e5", kindRational},
		{"1E-3", kindRational},
		{"0.5", kindRational},
		{"00.5", kindText}, // leading zero
		{"03.14", kindText},
		{"NaN", kindText},
		{"Inf", kindText},
		{"-Inf", kindText},
		{"0x1p3", kindText}, // hex float
		{"1e400", kindText}, // overflows float64
		{"abc", kindText},
		{"1.2.3", kindText},
	}
	for _, tc := range cases {
		if got := classify(tc.in); got != tc.want {
			t.Errorf("classify(%q) = %v, want %v", tc.in, got, tc.want)
		}
	}
}

func TestHugeIntegers(t *testing.T) {
	cases := []struct {
		in   string
		want Type
	}{
		{"9223372036854775807", TypeInt},        // max int64
		{"-9223372036854775808", TypeInt},       // min int64
		{"9223372036854775808", TypeRational},   // 2^63, exact in float64
		{"-9223372036854775809", TypeText},      // -(2^63)-1, not exact
		{"99999999999999999999", TypeText},      // 1e20-1, not exact
		{"100000000000000000000", TypeRational}, // 1e20, exact in float64
		{"123456789012345678901234567890", TypeText},
	}
	inf := New([]string{"big"})
	var records [][]string
	for _, tc := range cases {
		records = append(records, []string{tc.in})
	}
	mustAdd(t, inf, records...)
	// A column mixing all of them widens to text; check each value in
	// isolation instead.
	for _, tc := range cases {
		solo := New([]string{"big"})
		mustAdd(t, solo, []string{tc.in})
		if got := solo.Types()[0]; got != tc.want {
			t.Errorf("type of %q = %s, want %s", tc.in, got, tc.want)
		}
	}
	// Full-scan verification of the mixed column.
	mismatches, err := inf.Verify(records)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if len(mismatches) != 0 {
		t.Errorf("Verify mismatches: %v", mismatches)
	}
	if got := inf.Types()[0]; got != TypeText {
		t.Errorf("mixed huge-integer column = %s, want text", got)
	}
}

func TestEmptyColumn(t *testing.T) {
	records := [][]string{{""}, {""}, {""}}

	// Default: empty strings are text evidence.
	inf := New([]string{"e"})
	mustAdd(t, inf, records...)
	if got := inf.Types()[0]; got != TypeText {
		t.Errorf("all-empty column = %s, want text", got)
	}
	if c := inf.Column(0).Counts(); c.Text != 3 {
		t.Errorf("counts = %+v, want Text=3", c)
	}
	if mm, err := inf.Verify(records); err != nil || len(mm) != 0 {
		t.Errorf("Verify = %v, %v; want no mismatches", mm, err)
	}

	// With nulls: no evidence at all, safe fallback is text.
	infNull := New([]string{"e"}, WithEmptyAsNull())
	mustAdd(t, infNull, records...)
	if got := infNull.Types()[0]; got != TypeText {
		t.Errorf("all-null column = %s, want text fallback", got)
	}
	if c := infNull.Column(0).Counts(); c.Total() != 0 || c.Null != 3 {
		t.Errorf("counts = %+v, want Total=0 Null=3", c)
	}
	if mm, err := infNull.Verify(records); err != nil || len(mm) != 0 {
		t.Errorf("Verify = %v, %v; want no mismatches", mm, err)
	}
}

func TestLeadingZeroColumnStaysText(t *testing.T) {
	records := [][]string{{"0001"}, {"0042"}, {"0123"}}
	inf := New([]string{"zip"})
	mustAdd(t, inf, records...)
	if got := inf.Types()[0]; got != TypeText {
		t.Fatalf("leading-zero column = %s, want text", got)
	}
	// Undo all but one record: still text, never collapses to int.
	mustRemove(t, inf, records[0], records[1])
	if got := inf.Types()[0]; got != TypeText {
		t.Fatalf("after undo = %s, want text", got)
	}
	if mm, err := inf.Verify(records[2:]); err != nil || len(mm) != 0 {
		t.Errorf("Verify = %v, %v; want no mismatches", mm, err)
	}
}

func TestVerifyDetectsDivergence(t *testing.T) {
	inf := New([]string{"a", "b"})
	all := [][]string{{"1", "true"}, {"2", "false"}, {"3.5", "1"}}
	mustAdd(t, inf, all...)
	mustRemove(t, inf, []string{"3.5", "1"})

	// Verifying against the original full record set must flag both
	// columns: the incremental state no longer contains the removed row.
	mm, err := inf.Verify(all)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if len(mm) != 2 {
		t.Fatalf("got %d mismatches, want 2: %v", len(mm), mm)
	}
	if mm[0].Got != TypeInt || mm[0].Want != TypeRational {
		t.Errorf("mismatch[0] = %v, want int vs rational", mm[0])
	}
	if mm[1].Got != TypeBool || mm[1].Want != TypeInt {
		t.Errorf("mismatch[1] = %v, want bool vs int", mm[1])
	}

	// Verifying against the remaining records is consistent.
	if mm, err := inf.Verify(all[:2]); err != nil || len(mm) != 0 {
		t.Errorf("Verify(remaining) = %v, %v; want no mismatches", mm, err)
	}
}

func TestRemoveWithoutEvidence(t *testing.T) {
	inf := New([]string{"a", "b"})
	mustAdd(t, inf, []string{"1", "true"})

	if err := inf.RemoveRecord([]string{"1", "nope"}); err == nil {
		t.Fatal("removing a value never added should fail")
	}
	// Atomicity: the first column must be untouched.
	if c := inf.Column(0).Counts(); c.Int != 1 {
		t.Errorf("column 0 counts = %+v after failed remove, want Int=1", c)
	}
	if err := inf.RemoveRecord([]string{"1"}); err == nil {
		t.Fatal("wrong-width record should fail")
	}
	if err := inf.AddRecord([]string{"1", "2", "3"}); err == nil {
		t.Fatal("wrong-width record should fail")
	}
}

func TestTypeString(t *testing.T) {
	names := map[Type]string{
		TypeBool:     "bool",
		TypeInt:      "int",
		TypeRational: "rational",
		TypeText:     "text",
	}
	for typ, want := range names {
		if got := typ.String(); got != want {
			t.Errorf("%d.String() = %q, want %q", int(typ), got, want)
		}
	}
	if got := Type(99).String(); !strings.Contains(got, "unknown") {
		t.Errorf("invalid type String() = %q", got)
	}
}
