package csvq

import (
	"reflect"
	"testing"
)

func TestQuotedCommasAndEmbeddedNewlines(t *testing.T) {
	src := []byte("name,note\n\"a,b\",\"line1\nline2\"\n\"x\"\"y\",plain\n")
	f := Parse(src)
	if got, want := len(f.Records), 2; got != want {
		t.Fatalf("records = %d, want %d", got, want)
	}
	if len(f.Quarantine) != 0 {
		t.Fatalf("quoted newline triggered repair: %+v", f.Quarantine)
	}
	r0 := f.Records[0]
	if r0.Fields[0].Value != "a,b" || !r0.Fields[0].Quoted {
		t.Errorf("field 0 = %+v, want quoted \"a,b\"", r0.Fields[0])
	}
	if r0.Fields[1].Value != "line1\nline2" {
		t.Errorf("field 1 = %q, want embedded newline preserved", r0.Fields[1].Value)
	}
	if r1 := f.Records[1]; r1.Fields[0].Value != `x"y` || r1.Fields[1].Value != "plain" {
		t.Errorf("record 1 fields = %q, %q", r1.Fields[0].Value, r1.Fields[1].Value)
	}
}

func TestDuplicateHeaderNamesPositionIdentity(t *testing.T) {
	src := []byte("a,a,b\n1,2,3\n")
	f := Parse(src)
	if got, want := f.Header, []string{"a", "a", "b"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("header = %v, want %v", got, want)
	}
	if got := f.ColumnIndex("a"); got != 0 {
		t.Errorf("ColumnIndex(a) = %d, want 0 (first match)", got)
	}
	vals := f.Records[0].Values()
	if !reflect.DeepEqual(vals, []string{"1", "2", "3"}) {
		t.Errorf("values = %v", vals)
	}
	// Position, not name, is identity: the two "a" columns stay distinct.
	if f.Records[0].Fields[0].Value == f.Records[0].Fields[1].Value {
		t.Error("positional identity collapsed duplicate columns")
	}
}

func TestShortRowPaddingAndEmptyVsMissing(t *testing.T) {
	src := []byte("x,y,z\n1,,3\n1,2\n")
	f := Parse(src)
	if got, want := len(f.Records), 2; got != want {
		t.Fatalf("records = %d, want %d", got, want)
	}
	empty := f.Records[0].Fields[1]
	if empty.Missing || empty.Value != "" {
		t.Errorf("empty field = %+v, want present empty", empty)
	}
	padded := f.Records[1].Fields[2]
	if !padded.Missing {
		t.Errorf("padded field = %+v, want Missing", padded)
	}
	if len(f.Records[1].Fields) != 3 {
		t.Errorf("short row not padded to header width: %d fields", len(f.Records[1].Fields))
	}
	if len(f.Quarantine) != 0 {
		t.Errorf("short rows must be repaired, not quarantined: %+v", f.Quarantine)
	}
}

func TestLongRowQuarantineKeepsLaterPositions(t *testing.T) {
	src := []byte("x,y\n1,2\n9,9,9\n3,4\n")
	f := Parse(src)
	if got, want := len(f.Quarantine), 1; got != want {
		t.Fatalf("quarantine = %d, want %d", got, want)
	}
	q := f.Quarantine[0]
	if q.Index != 1 || q.Raw != "9,9,9\n" {
		t.Errorf("quarantine = %+v", q)
	}
	if got, want := len(f.Records), 2; got != want {
		t.Fatalf("records = %d, want %d", got, want)
	}
	// The row after the quarantined one keeps its ordinal and positions.
	r := f.Records[1]
	if r.Index != 2 || r.Fields[0].Value != "3" || r.Fields[1].Value != "4" {
		t.Errorf("post-quarantine record = index %d values %v", r.Index, r.Values())
	}
	// The quarantined row's byte range must slice back its raw text.
	if got := string(src[q.Offset : q.Offset+q.Length]); got != q.Raw {
		t.Errorf("range slice = %q, want %q", got, q.Raw)
	}
}

func TestChunksAndIndependentRecordSamples(t *testing.T) {
	src := []byte("a,b\n1,2\n3,4\n5,6\n7,8\n9,10\n")
	f := Parse(src)
	chunks := Chunks(f, 2)
	if got, want := len(chunks), 3; got != want {
		t.Fatalf("chunks = %d, want %d", got, want)
	}
	// Enumerated chunks tile the record range without gaps or overlaps.
	for i, c := range chunks {
		if c.Index != i {
			t.Errorf("chunk %d has index %d", i, c.Index)
		}
		if i > 0 && c.FirstRecord != chunks[i-1].LastRecord {
			t.Errorf("gap/overlap between chunk %d and %d", i-1, i)
		}
	}
	if chunks[len(chunks)-1].LastRecord != len(f.Records) {
		t.Errorf("last chunk ends at %d, want %d", chunks[len(chunks)-1].LastRecord, len(f.Records))
	}
	// Independent samples: re-parse records straight from the raw source
	// at their recorded offsets and compare field by field.
	for _, i := range []int{0, 2, 4} {
		want := f.Records[i]
		got, err := ParseRecordAt(src, want.Offset)
		if err != nil {
			t.Fatalf("ParseRecordAt: %v", err)
		}
		if got.Length != want.Length {
			t.Errorf("record %d length = %d, want %d", i, got.Length, want.Length)
		}
		if !reflect.DeepEqual(got.Values(), want.Values()) {
			t.Errorf("record %d values = %v, want %v", i, got.Values(), want.Values())
		}
		if string(src[want.Offset:want.Offset+want.Length]) != string(src[got.Offset:got.Offset+got.Length]) {
			t.Errorf("record %d raw range mismatch", i)
		}
	}
}

func TestCRLFAndBlankLines(t *testing.T) {
	src := []byte("a,b\r\n1,2\r\n\r\n3,4\r\n")
	f := Parse(src)
	if got, want := len(f.Records), 2; got != want {
		t.Fatalf("records = %d, want %d", got, want)
	}
	if f.Records[1].Fields[0].Value != "3" {
		t.Errorf("record 1 = %v", f.Records[1].Values())
	}
}
