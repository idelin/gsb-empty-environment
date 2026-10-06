package csvq

import (
	"strings"
	"testing"
)

func importAll(t *testing.T, f *File, chunkSize int) *Store {
	t.Helper()
	store := NewStore(f.Width())
	imp := NewImporter(store, f.Width())
	for _, c := range Chunks(f, chunkSize) {
		if err := imp.ImportBatch(f.Records[c.FirstRecord:c.LastRecord]); err != nil {
			t.Fatalf("ImportBatch chunk %d: %v", c.Index, err)
		}
	}
	return store
}

func TestTypeInferenceAndWideningKeepsOldBatches(t *testing.T) {
	// Batch 1: column "code" looks like integers. Batch 2: "007" and
	// "abc" force the column to widen to text.
	src := []byte("id,code\n1,100\n2,200\n3,007\n4,abc\n")
	f := Parse(src)
	store := NewStore(f.Width())
	imp := NewImporter(store, f.Width())

	if err := imp.ImportBatch(f.Records[0:2]); err != nil {
		t.Fatal(err)
	}
	if got := store.SchemaVersion(); got != 1 {
		t.Fatalf("schema version = %d, want 1", got)
	}
	types, _ := store.Types(1)
	if types[1] != TInt {
		t.Fatalf("batch 1 code type = %s, want int", types[1])
	}

	if err := imp.ImportBatch(f.Records[2:4]); err != nil {
		t.Fatal(err)
	}
	if got := store.SchemaVersion(); got != 2 {
		t.Fatalf("schema version = %d, want 2 after widening", got)
	}
	types, _ = store.Types(2)
	if types[1] != TText {
		t.Fatalf("batch 2 code type = %s, want text", types[1])
	}

	// Versioned view: old batch keeps its original integer values.
	v1, err := store.View(1)
	if err != nil {
		t.Fatal(err)
	}
	if len(v1) != 2 || v1[0].Values[1] != int64(100) || v1[1].Values[1] != int64(200) {
		t.Errorf("view v1 = %+v", v1)
	}
	v2, err := store.View(2)
	if err != nil {
		t.Fatal(err)
	}
	if len(v2) != 2 || v2[0].Values[1] != "007" || v2[1].Values[1] != "abc" {
		t.Errorf("view v2 = %+v", v2)
	}

	// Latest view renders everything as text from raw source values;
	// leading zeros survive widening.
	all := store.ViewAll()
	if len(all) != 4 {
		t.Fatalf("ViewAll rows = %d", len(all))
	}
	if all[2].Values[1] != "007" {
		t.Errorf("widened value = %v, leading zeros lost", all[2].Values[1])
	}
	if all[0].Values[1] != "100" {
		t.Errorf("old value under latest schema = %v, want original text", all[0].Values[1])
	}
}

func TestLeadingZerosAreTextFromTheStart(t *testing.T) {
	src := []byte("code\n007\n008\n")
	f := Parse(src)
	store := importAll(t, f, 10)
	types, _ := store.Types(store.SchemaVersion())
	if types[0] != TText {
		t.Fatalf("type = %s, want text (leading zeros must not parse as int)", types[0])
	}
	if got := store.ViewAll()[0].Values[0]; got != "007" {
		t.Errorf("value = %v, want %q", got, "007")
	}
}

func TestAtomicBatchCommit(t *testing.T) {
	src := []byte("a,b\n1,2\n3,4\n")
	f := Parse(src)
	store := NewStore(f.Width())
	imp := NewImporter(store, f.Width())
	if err := imp.ImportBatch(f.Records[0:1]); err != nil {
		t.Fatal(err)
	}
	before := store.RowCount()

	// A batch containing one malformed record must commit nothing.
	good := f.Records[1]
	bad := Record{Index: 99, Fields: []Field{{Value: "x"}}} // wrong width
	if err := imp.ImportBatch([]Record{good, bad}); err == nil {
		t.Fatal("expected error for malformed batch")
	}
	if got := store.RowCount(); got != before {
		t.Errorf("row count = %d after failed batch, want %d (atomic)", got, before)
	}
	if got := store.SchemaVersion(); got != 1 {
		t.Errorf("schema version = %d after failed batch, want 1", got)
	}
}

func TestMissingVsEmptyInView(t *testing.T) {
	src := []byte("a,b\n1,\n2\n")
	f := Parse(src)
	store := importAll(t, f, 10)
	rows := store.ViewAll()
	if rows[0].Values[1] != "" {
		t.Errorf("empty field renders as %v, want \"\"", rows[0].Values[1])
	}
	if rows[1].Values[1] != nil {
		t.Errorf("missing field renders as %v, want nil", rows[1].Values[1])
	}
}

func TestManifestVerifyFullImport(t *testing.T) {
	src := []byte("id,code,note\n1,100,\"a,b\"\n2,200,\"x\ny\"\n3,007,z\n4,5,6,7\n5,300,w\n")
	f := Parse(src)
	if len(f.Quarantine) != 1 {
		t.Fatalf("quarantine = %d, want 1", len(f.Quarantine))
	}
	store := importAll(t, f, 2)
	m := BuildManifest(src, f, store, 2)

	if m.QuarantineCount != 1 || m.RecordCount != 4 {
		t.Fatalf("manifest counts = %d/%d", m.RecordCount, m.QuarantineCount)
	}
	if m.SchemaVersion != store.SchemaVersion() {
		t.Fatalf("manifest schema version = %d, store %d", m.SchemaVersion, store.SchemaVersion())
	}
	if err := m.Verify(src, 2); err != nil {
		t.Fatalf("Verify: %v", err)
	}

	// Tampered source must fail the digest binding.
	tampered := []byte(strings.Replace(string(src), "007", "008", 1))
	if err := m.Verify(tampered, 2); err == nil {
		t.Error("Verify accepted tampered source")
	}

	// Tampered schema version must fail the full re-import check.
	bad := m
	bad.SchemaVersion++
	if err := bad.Verify(src, 2); err == nil {
		t.Error("Verify accepted wrong schema version")
	}

	// Tampered quarantine list must fail.
	bad = m
	bad.Quarantine = append([]QuarantineEntry(nil), m.Quarantine...)
	bad.Quarantine[0].Offset++
	if err := bad.Verify(src, 2); err == nil {
		t.Error("Verify accepted wrong quarantine range")
	}
}
