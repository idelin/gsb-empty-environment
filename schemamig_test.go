package schemamig

import (
	"math/big"
	"strings"
	"testing"
)

func rat(s string) *big.Rat {
	r, ok := new(big.Rat).SetString(s)
	if !ok {
		panic("bad rational: " + s)
	}
	return r
}

// Round-trip property: for every value and every target kind, a
// successful conversion must reproduce the original value on the way
// back. Narrowing conversions that would lose information must fail.
func TestConvertRoundTrip(t *testing.T) {
	values := []Value{
		IntVal(0),
		IntVal(-42),
		IntVal(1 << 40),
		RatVal(rat("3/1")),
		RatVal(rat("-7/2")),
		RatVal(rat("22/7")),
		TextVal(""),
		TextVal("42"),
		TextVal("-7"),
		TextVal("3/4"),
		TextVal("hello"),
		TextVal("007"),
	}
	kinds := []Kind{KindInt, KindRational, KindText}
	for _, v := range values {
		for _, to := range kinds {
			w, err := Convert(v, to)
			if err != nil {
				continue // rejected narrowing is fine
			}
			back, err := Convert(w, v.Kind)
			if err != nil {
				t.Errorf("Convert(%s, %s) = %s, but converting back failed: %v", v, to, w, err)
				continue
			}
			if !Equal(back, v) {
				t.Errorf("round trip %s -> %s -> %s gave %s, want %s", v.Kind, to, v.Kind, back, v)
			}
		}
	}
}

// Type narrowing: only lossless narrowings are allowed.
func TestConvertNarrowing(t *testing.T) {
	ok := []struct {
		v    Value
		to   Kind
		want Value
	}{
		{IntVal(7), KindRational, RatVal(rat("7/1"))},
		{IntVal(7), KindText, TextVal("7")},
		{RatVal(rat("3/1")), KindInt, IntVal(3)},
		{RatVal(rat("22/7")), KindText, TextVal("22/7")},
		{TextVal("-42"), KindInt, IntVal(-42)},
		{TextVal("3/4"), KindRational, RatVal(rat("3/4"))},
	}
	for _, tc := range ok {
		got, err := Convert(tc.v, tc.to)
		if err != nil {
			t.Errorf("Convert(%s, %s) failed: %v", tc.v, tc.to, err)
			continue
		}
		if !Equal(got, tc.want) {
			t.Errorf("Convert(%s, %s) = %s, want %s", tc.v, tc.to, got, tc.want)
		}
	}

	rejected := []struct {
		v  Value
		to Kind
	}{
		{RatVal(rat("7/2")), KindInt},  // fractional
		{RatVal(rat("1/3")), KindInt},  // fractional
		{TextVal("4.5"), KindInt},      // not an integer
		{TextVal("007"), KindInt},      // non-canonical, would come back "7"
		{TextVal("1.5"), KindRational}, // would come back "3/2"
		{TextVal("hello"), KindInt},    // unparseable
		{Null(), KindInt},              // null never converts
		{IntVal(1), KindNull},          // cannot convert to null
	}
	for _, tc := range rejected {
		if w, err := Convert(tc.v, tc.to); err == nil {
			t.Errorf("Convert(%s, %s) = %s, want error", tc.v, tc.to, w)
		}
	}
}

func testSchema() *Schema {
	return &Schema{Fields: []Field{
		{Name: "id", Type: KindInt},
		{Name: "price", Type: KindRational, Nullable: true},
		{Name: "label", Type: KindText, HasDefault: true, Default: TextVal("n/a")},
	}}
}

func TestMigrateRowDefaultsAndNulls(t *testing.T) {
	s := testSchema()
	out, err := s.MigrateRow(Row{"id": IntVal(1), "price": Null()})
	if err != nil {
		t.Fatalf("MigrateRow failed: %v", err)
	}
	if !Equal(out["id"], IntVal(1)) {
		t.Errorf("id = %s, want 1", out["id"])
	}
	if !Equal(out["price"], Null()) {
		t.Errorf("price = %s, want null", out["price"])
	}
	if !Equal(out["label"], TextVal("n/a")) {
		t.Errorf("label = %s, want default %q", out["label"], "n/a")
	}
	if _, ok := out[ExtraColumn]; ok {
		t.Errorf("unexpected %s column", ExtraColumn)
	}
}

func TestMigrateRowMissingRequired(t *testing.T) {
	s := testSchema()
	_, err := s.MigrateRow(Row{"price": IntVal(3)})
	if err == nil {
		t.Fatal("expected error for missing id without default")
	}
	fe, ok := err.(*FieldError)
	if !ok || fe.Field != "id" {
		t.Fatalf("error = %v, want FieldError for id", err)
	}
}

func TestMigrateRowNullInNonNullable(t *testing.T) {
	s := testSchema()
	_, err := s.MigrateRow(Row{"id": Null()})
	fe, ok := err.(*FieldError)
	if !ok || fe.Field != "id" {
		t.Fatalf("error = %v, want FieldError for id", err)
	}
}

func TestMigrateRowUnknownFieldsPreserved(t *testing.T) {
	s := testSchema()
	out, err := s.MigrateRow(Row{
		"id":      IntVal(9),
		"legacy":  TextVal("keep me"),
		"oldflag": IntVal(1),
	})
	if err != nil {
		t.Fatalf("MigrateRow failed: %v", err)
	}
	extra, ok := out[ExtraColumn]
	if !ok || extra.Kind != KindRecord {
		t.Fatalf("missing %s record column", ExtraColumn)
	}
	if !Equal(extra.Record["legacy"], TextVal("keep me")) {
		t.Errorf("extra.legacy = %v", extra.Record["legacy"])
	}
	if !Equal(extra.Record["oldflag"], IntVal(1)) {
		t.Errorf("extra.oldflag = %v", extra.Record["oldflag"])
	}
}

func TestMigrateBatchAtomic(t *testing.T) {
	s := testSchema()
	rows := []Row{
		{"id": IntVal(1)},
		{"id": TextVal("not-a-number")},
		{"id": IntVal(3)},
	}
	out, err := s.MigrateBatch(rows)
	if out != nil {
		t.Errorf("got %d published rows, want none on failure", len(out))
	}
	be, ok := err.(*BatchError)
	if !ok {
		t.Fatalf("error = %v, want BatchError", err)
	}
	if len(be.Rows) != 1 {
		t.Fatalf("got %d row errors, want 1", len(be.Rows))
	}
	re := be.Rows[0]
	if re.Index != 1 || re.Path != "rows[1].id" {
		t.Errorf("row error = index %d path %q, want rows[1].id", re.Index, re.Path)
	}
	if !strings.Contains(re.Error(), "rows[1].id") {
		t.Errorf("error text %q lacks path", re.Error())
	}
}

func TestMigrateBatchAllValid(t *testing.T) {
	s := testSchema()
	out, err := s.MigrateBatch([]Row{
		{"id": IntVal(1), "price": RatVal(rat("5/2"))},
		{"id": TextVal("2"), "label": TextVal("x")},
	})
	if err != nil {
		t.Fatalf("MigrateBatch failed: %v", err)
	}
	if len(out) != 2 {
		t.Fatalf("got %d rows, want 2", len(out))
	}
	if !Equal(out[1]["id"], IntVal(2)) {
		t.Errorf("row 1 id = %s, want 2 (text narrowed to int)", out[1]["id"])
	}
	if !Equal(out[0]["price"], RatVal(rat("5/2"))) {
		t.Errorf("row 0 price = %s, want 5/2", out[0]["price"])
	}
}

func TestSchemaValidate(t *testing.T) {
	bad := &Schema{Fields: []Field{
		{Name: "x", Type: KindInt, HasDefault: true, Default: Null()},
	}}
	if err := bad.Validate(); err == nil {
		t.Error("expected error for null default on non-nullable field")
	}
	dup := &Schema{Fields: []Field{{Name: "a", Type: KindInt}, {Name: "a", Type: KindText}}}
	if err := dup.Validate(); err == nil {
		t.Error("expected error for duplicate field")
	}
	if err := testSchema().Validate(); err != nil {
		t.Errorf("testSchema should validate: %v", err)
	}
}
