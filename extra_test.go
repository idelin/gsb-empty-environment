package parquetcol

import (
	"bytes"
	"testing"
)

func TestReaderAtPath(t *testing.T) {
	schema, err := Compile(Group("root", Required, Leaf("n", Required, TypeInt64)))
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	w, err := NewWriter(&buf, schema, nil)
	if err != nil {
		t.Fatal(err)
	}
	want := []Value{
		Struct(map[string]Value{"n": Int64(10)}),
		Struct(map[string]Value{"n": Int64(20)}),
	}
	for _, row := range want {
		if err := w.WriteRow(row); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	r, err := NewReader(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	if err != nil {
		t.Fatal(err)
	}
	got, err := r.ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	assertRowsEqual(t, want, got)
}

func TestMultiPageRoundTrip(t *testing.T) {
	schema, err := Compile(Group("root", Required,
		Leaf("n", Required, TypeInt64),
		Leaf("b", Required, TypeBoolean),
	))
	if err != nil {
		t.Fatal(err)
	}
	const rows = 5000
	want := make([]Value, rows)
	for i := 0; i < rows; i++ {
		want[i] = Struct(map[string]Value{
			"n": Int64(int64(i)),
			"b": Bool(i%2 == 0),
		})
	}
	opts := &Options{PageSize: 512}
	got := roundTrip(t, schema, want, opts)
	assertRowsEqual(t, want, got)

	// Confirm more than one data page was produced for at least one column.
	var raw bytes.Buffer
	w, err := NewWriter(&raw, schema, opts)
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range want {
		if err := w.WriteRow(row); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	r, err := NewReaderFromBytes(raw.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	meta := r.meta.rowGroups[0]
	dataPages := 0
	for _, cc := range meta.columns {
		m := cc.metaData
		pos := m.dataPageOffset
		if m.dictionaryOffset >= 0 {
			pos = m.dictionaryOffset
		}
		end := pos + m.totalCompressed
		for pos < end {
			hdr, hdrLen, err := r.readPageHeaderAt(pos)
			if err != nil {
				t.Fatal(err)
			}
			if hdr.type_ == pageDataPage {
				dataPages++
			}
			pos += int64(hdrLen) + int64(hdr.compressedSize)
		}
	}
	if dataPages <= len(meta.columns) {
		t.Fatalf("expected multiple data pages, got %d across %d columns", dataPages, len(meta.columns))
	}
}

func TestBooleanStats(t *testing.T) {
	schema, err := Compile(Group("root", Required, Leaf("flag", Optional, TypeBoolean)))
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	w, err := NewWriter(&buf, schema, nil)
	if err != nil {
		t.Fatal(err)
	}
	rows := []Value{
		Struct(map[string]Value{"flag": Bool(true)}),
		Struct(map[string]Value{"flag": Bool(false)}),
		Struct(map[string]Value{"flag": Null()}),
		Struct(map[string]Value{"flag": Bool(true)}),
	}
	for _, row := range rows {
		if err := w.WriteRow(row); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	r, err := NewReaderFromBytes(buf.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	st := r.meta.rowGroups[0].columns[0].metaData.stats
	if st.nullCount != 1 || st.distinctCnt != 2 {
		t.Fatalf("stats=%+v", st)
	}
	if len(st.min) != 1 || st.min[0] != 0 || len(st.max) != 1 || st.max[0] != 1 {
		t.Fatalf("boolean min/max wrong: %v %v", st.min, st.max)
	}
}

func TestCorruptMagicRejected(t *testing.T) {
	if _, err := NewReaderFromBytes([]byte("NOTPARQUET!!!!")); err == nil {
		t.Fatal("expected error for bad magic")
	}
}

func TestAllNullColumn(t *testing.T) {
	schema, err := Compile(Group("root", Required,
		Leaf("missing", Optional, TypeInt32),
		Leaf("present", Required, TypeInt64),
	))
	if err != nil {
		t.Fatal(err)
	}
	rows := []Value{
		Struct(map[string]Value{"present": Int64(1)}),
		Struct(map[string]Value{"present": Int64(2), "missing": Null()}),
		Struct(map[string]Value{"present": Int64(3)}),
	}
	got := roundTrip(t, schema, rows, nil)
	assertRowsEqual(t, rows, got)

	var raw bytes.Buffer
	w, err := NewWriter(&raw, schema, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range rows {
		if err := w.WriteRow(row); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	r, err := NewReaderFromBytes(raw.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	stats := r.meta.rowGroups[0].columns[0].metaData.stats
	if stats.nullCount != 3 || stats.distinctCnt != 0 || stats.min != nil || stats.max != nil {
		t.Fatalf("all-null stats wrong: %+v", stats)
	}
}
