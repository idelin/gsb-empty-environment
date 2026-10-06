package migrate

import (
	"strings"
	"testing"
)

func testSchema(t *testing.T) *Schema {
	t.Helper()
	s, err := NewSchema("_extra",
		Field{Name: "id", Type: TInt},
		Field{Name: "score", Type: TRat, Nullable: true},
		Field{Name: "name", Type: TText, HasDefault: true, Default: TextVal("anon")},
		Field{Name: "nick", Type: TText, Nullable: true, HasDefault: true, Default: NullVal()},
	)
	if err != nil {
		t.Fatalf("NewSchema: %v", err)
	}
	return s
}

func TestMissingFieldUsesExplicitDefault(t *testing.T) {
	s := testSchema(t)
	out, errs := s.MigrateRow(Row{"id": IntVal(1), "score": rat(3, 2)})
	if len(errs) > 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}
	if got := out["name"]; !got.Equal(TextVal("anon")) {
		t.Errorf("name = %v, want default %q", got, "anon")
	}
	if got := out["nick"]; !got.IsNull() {
		t.Errorf("nick = %v, want null default", got)
	}
}

func TestMissingFieldWithoutDefaultFails(t *testing.T) {
	s := testSchema(t)
	_, errs := s.MigrateRow(Row{"score": rat(1, 1)}) // id missing, no default
	if len(errs) != 1 || errs[0].Field != "id" {
		t.Fatalf("errors = %v, want one error on id", errs)
	}
}

func TestNullability(t *testing.T) {
	s := testSchema(t)
	// Null into nullable field passes.
	out, errs := s.MigrateRow(Row{"id": IntVal(1), "score": NullVal()})
	if len(errs) > 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}
	if !out["score"].IsNull() {
		t.Errorf("score = %v, want null", out["score"])
	}
	// Null into non-nullable field fails.
	_, errs = s.MigrateRow(Row{"id": NullVal(), "score": rat(1, 1)})
	if len(errs) != 1 || errs[0].Field != "id" {
		t.Fatalf("errors = %v, want one error on id", errs)
	}
}

func TestUnknownFieldsPreservedInSingleColumn(t *testing.T) {
	s := testSchema(t)
	out, errs := s.MigrateRow(Row{
		"id": IntVal(1), "score": rat(1, 1),
		"legacy": TextVal("x"), "old_n": IntVal(9), "gone": NullVal(),
	})
	if len(errs) > 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}
	got := out["_extra"].Text()
	want := `{"gone":null,"legacy":"x","old_n":9}`
	if got != want {
		t.Errorf("_extra = %s, want %s", got, want)
	}
}

func TestUnknownFieldsRejectedWithoutColumn(t *testing.T) {
	s, err := NewSchema("", Field{Name: "id", Type: TInt})
	if err != nil {
		t.Fatalf("NewSchema: %v", err)
	}
	_, errs := s.MigrateRow(Row{"id": IntVal(1), "stray": IntVal(2)})
	if len(errs) != 1 || !strings.Contains(errs[0].Msg, "unknown fields") {
		t.Fatalf("errors = %v, want unknown-field rejection", errs)
	}
}

// TestBatchAtomicity: one bad row rejects the whole batch, nothing is
// published, and every failure is reported with its per-row path.
func TestBatchAtomicity(t *testing.T) {
	s := testSchema(t)
	rows := []Row{
		{"id": IntVal(1), "score": rat(1, 2)},
		{"id": rat(3, 2), "score": rat(1, 1)},                 // narrowing fails
		{"id": IntVal(3)},                                     // score missing, no default
		{"id": TextVal("007"), "score": NullVal()},            // not round-trip safe
		{"id": IntVal(5), "score": rat(7, 1), "x": IntVal(1)}, // ok, unknown kept
	}
	out, err := s.MigrateBatch(rows)
	if err == nil {
		t.Fatalf("batch succeeded with %d rows, want rejection", len(out))
	}
	if out != nil {
		t.Errorf("out = %v, want nil (no results published)", out)
	}
	be, ok := err.(*BatchError)
	if !ok {
		t.Fatalf("err type = %T, want *BatchError", err)
	}
	var paths []string
	for _, re := range be.Errors {
		paths = append(paths, re.Path())
	}
	want := []string{"rows[1].id", "rows[2].score", "rows[3].id"}
	if strings.Join(paths, ",") != strings.Join(want, ",") {
		t.Errorf("paths = %v, want %v", paths, want)
	}
}

// TestBatchSuccessRoundTrip migrates a clean batch and converts each output
// row back, verifying values survive unchanged.
func TestBatchSuccessRoundTrip(t *testing.T) {
	s := testSchema(t)
	rows := []Row{
		{"id": IntVal(1), "score": rat(1, 2), "name": TextVal("ada")},
		{"id": TextVal("2"), "score": IntVal(3)}, // safe widenings
		{"id": IntVal(3), "score": NullVal(), "nick": TextVal("g")},
	}
	out, err := s.MigrateBatch(rows)
	if err != nil {
		t.Fatalf("MigrateBatch: %v", err)
	}
	if len(out) != len(rows) {
		t.Fatalf("got %d rows, want %d", len(out), len(rows))
	}
	// Row 1: text "2" narrowed to int; int 3 widened to rational.
	if got := out[1]["id"]; !got.Equal(IntVal(2)) {
		t.Errorf("out[1].id = %v, want 2", got)
	}
	if got := out[1]["score"]; !got.Equal(rat(3, 1)) {
		t.Errorf("out[1].score = %v, want 3/1", got)
	}
	if got := out[1]["name"]; !got.Equal(TextVal("anon")) {
		t.Errorf("out[1].name = %v, want default", got)
	}
	// Row 2: nulls preserved through nullable fields.
	if !out[2]["score"].IsNull() {
		t.Errorf("out[2].score = %v, want null", out[2]["score"])
	}
}

func TestSchemaValidation(t *testing.T) {
	if _, err := NewSchema("", Field{Name: "a"}, Field{Name: "a"}); err == nil {
		t.Error("duplicate field names accepted")
	}
	if _, err := NewSchema("", Field{Name: "a", Type: TInt, HasDefault: true, Default: NullVal()}); err == nil {
		t.Error("null default on non-nullable field accepted")
	}
	if _, err := NewSchema("", Field{Name: "a", Type: TInt, HasDefault: true, Default: TextVal("x")}); err == nil {
		t.Error("mismatched default kind accepted")
	}
	if _, err := NewSchema("a", Field{Name: "a", Type: TInt}); err == nil {
		t.Error("unknown column colliding with field accepted")
	}
}
