package csvq

import "testing"

func mustParse(t *testing.T, src string) *Table {
	t.Helper()
	tab, err := Parse([]byte(src))
	if err != nil {
		t.Fatal(err)
	}
	return tab
}

func TestTypeInferenceInt(t *testing.T) {
	s := NewStore()
	if _, err := s.Import(mustParse(t, "id,code\n1,007\n2,042\n")); err != nil {
		t.Fatal(err)
	}
	schemas := s.Schemas()
	if len(schemas) != 1 {
		t.Fatalf("schemas = %d, want 1", len(schemas))
	}
	if schemas[0].Columns[1].Type != Int {
		t.Fatalf("code column type = %v, want int", schemas[0].Columns[1].Type)
	}
}

func TestWideningKeepsLeadingZerosViaVersionedViews(t *testing.T) {
	s := NewStore()
	if _, err := s.Import(mustParse(t, "id,code\n1,007\n2,042\n")); err != nil {
		t.Fatal(err)
	}
	meta, err := s.Import(mustParse(t, "id,code\n3,abc\n"))
	if err != nil {
		t.Fatal(err)
	}
	if meta.Schema != 2 {
		t.Fatalf("widening batch committed under schema %d, want 2", meta.Schema)
	}
	schemas := s.Schemas()
	if len(schemas) != 2 || schemas[1].Columns[1].Type != Text {
		t.Fatalf("schema history = %+v", schemas)
	}

	// Old batch under the original version renders integers.
	v1, err := s.View(1)
	if err != nil {
		t.Fatal(err)
	}
	got := v1.Rows[0].Values[1]
	if !got.IsInt || got.Int != 7 {
		t.Fatalf("v1 view of old batch = %+v, want int 7", got)
	}

	// Old batch under the widened version keeps the original text:
	// leading zeros are not lost.
	v2, err := s.View(2)
	if err != nil {
		t.Fatal(err)
	}
	got = v2.Rows[0].Values[1]
	if got.IsInt || got.Text != "007" {
		t.Fatalf("v2 view of old batch = %+v, want text 007", got)
	}
	if got = v2.Rows[1].Values[1]; got.Text != "042" {
		t.Fatalf("v2 view row 1 = %+v, want text 042", got)
	}
	// New batch text value is visible under both views as raw text.
	if got = v2.Rows[2].Values[1]; got.Text != "abc" {
		t.Fatalf("v2 view row 2 = %+v, want text abc", got)
	}
}

func TestAtomicBatchWriteOnError(t *testing.T) {
	s := NewStore()
	if _, err := s.Import(mustParse(t, "id,code\n1,007\n")); err != nil {
		t.Fatal(err)
	}
	rowsBefore := s.Rows()
	batchesBefore := len(s.Batches())
	schemasBefore := len(s.Schemas())

	// Width mismatch must fail and leave the store untouched.
	if _, err := s.Import(mustParse(t, "id,code,extra\n9,9,9\n")); err == nil {
		t.Fatal("expected width mismatch error")
	}
	if s.Rows() != rowsBefore || len(s.Batches()) != batchesBefore || len(s.Schemas()) != schemasBefore {
		t.Fatalf("store changed after failed import: rows %d->%d batches %d->%d schemas %d->%d",
			rowsBefore, s.Rows(), batchesBefore, len(s.Batches()), schemasBefore, len(s.Schemas()))
	}
}

func TestEmptyVsMissingInStore(t *testing.T) {
	s := NewStore()
	if _, err := s.Import(mustParse(t, "a,b,c\n1,,3\n4,5\n")); err != nil {
		t.Fatal(err)
	}
	v, err := s.View(1)
	if err != nil {
		t.Fatal(err)
	}
	empty := v.Rows[0].Values[1]
	if !empty.Present || empty.Text != "" {
		t.Fatalf("empty cell = %+v, want present empty text", empty)
	}
	missing := v.Rows[1].Values[2]
	if missing.Present {
		t.Fatalf("missing cell = %+v, want not present", missing)
	}
}

func TestQuarantineDoesNotShiftColumnPositions(t *testing.T) {
	s := NewStore()
	meta, err := s.Import(mustParse(t, "a,b,c\n1,2,3,4\n7,8,9\n"))
	if err != nil {
		t.Fatal(err)
	}
	if len(meta.Quarantined) != 1 || meta.Quarantined[0] != 0 {
		t.Fatalf("batch quarantine = %+v", meta)
	}
	if len(s.Quarantine()) != 1 {
		t.Fatalf("store quarantine = %d, want 1", len(s.Quarantine()))
	}
	v, err := s.View(1)
	if err != nil {
		t.Fatal(err)
	}
	if len(v.Rows) != 1 {
		t.Fatalf("view rows = %d, want 1", len(v.Rows))
	}
	vals := v.Rows[0].Values
	if vals[0].Text != "7" || vals[1].Text != "8" || vals[2].Text != "9" {
		t.Fatalf("row after quarantine = %+v, positions shifted", vals)
	}
}

func TestDuplicateHeaderNamesInStore(t *testing.T) {
	s := NewStore()
	if _, err := s.Import(mustParse(t, "name,name\n1,2\n")); err != nil {
		t.Fatal(err)
	}
	cols := s.Columns()
	if len(cols) != 2 || cols[0] != "name" || cols[1] != "name" {
		t.Fatalf("columns = %v, want [name name]", cols)
	}
	v, err := s.View(1)
	if err != nil {
		t.Fatal(err)
	}
	// Positional identity: both "name" columns addressable by index.
	if v.Rows[0].Values[0].Int != 1 || v.Rows[0].Values[1].Int != 2 {
		t.Fatalf("positional values = %+v", v.Rows[0].Values)
	}
}
