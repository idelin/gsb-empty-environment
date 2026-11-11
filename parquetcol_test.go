package parquetcol

import (
	"bytes"
	"strconv"
	"testing"
)

func roundTrip(t *testing.T, schema *Schema, rows []Value, opts *Options) []Value {
	t.Helper()
	var buf bytes.Buffer
	w, err := NewWriter(&buf, schema, opts)
	if err != nil {
		t.Fatalf("NewWriter: %v", err)
	}
	for i, row := range rows {
		if err := w.WriteRow(row); err != nil {
			t.Fatalf("WriteRow %d: %v", i, err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	r, err := NewReaderFromBytes(buf.Bytes())
	if err != nil {
		t.Fatalf("NewReader: %v", err)
	}
	got, err := r.ReadAll()
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	return got
}

func assertRowsEqual(t *testing.T, want, got []Value) {
	t.Helper()
	if len(want) != len(got) {
		t.Fatalf("row count: want %d got %d", len(want), len(got))
	}
	for i := range want {
		if !want[i].Equal(got[i]) {
			t.Errorf("row %d mismatch:\n want %s\n got  %s", i, want[i].GoString(), got[i].GoString())
		}
	}
}

func TestPrimitiveRoundTrip(t *testing.T) {
	schema, err := Compile(Group("root", Required,
		Leaf("b", Required, TypeBoolean),
		Leaf("i32", Optional, TypeInt32),
		Leaf("i64", Optional, TypeInt64),
		Leaf("f32", Optional, TypeFloat),
		Leaf("f64", Optional, TypeDouble),
		StringLeaf("s", Optional),
		Leaf("data", Optional, TypeByteArray),
	))
	if err != nil {
		t.Fatal(err)
	}
	rows := []Value{
		Struct(map[string]Value{
			"b":    Bool(true),
			"i32":  Int32(-7),
			"i64":  Int64(1 << 40),
			"f32":  Float32(3.5),
			"f64":  Float64(-2.25),
			"s":    String("héllo"),
			"data": Bytes([]byte{0, 1, 2, 255}),
		}),
		Struct(map[string]Value{"b": Bool(false)}),
		Struct(map[string]Value{
			"b":    Bool(true),
			"i32":  Int32(0),
			"i64":  Int64(-99),
			"f32":  Float32(0),
			"f64":  Float64(0),
			"s":    String(""),
			"data": Bytes([]byte{}),
		}),
	}
	assertRowsEqual(t, rows, roundTrip(t, schema, rows, nil))
}

func int32FromPlain(b []byte) int32 {
	return int32(uint32(b[0]) | uint32(b[1])<<8 | uint32(b[2])<<16 | uint32(b[3])<<24)
}

func itoa(i int) string { return strconv.Itoa(i) }

func TestEmptyAndNullLists(t *testing.T) {
	schema, err := Compile(Group("root", Required,
		ListOf("tags", StringLeaf("element", Optional)),
		ListOf("counts", Leaf("element", Required, TypeInt64)),
	))
	if err != nil {
		t.Fatal(err)
	}
	rows := []Value{
		Struct(map[string]Value{
			"tags":   List([]Value{String("a"), String("b"), String("c")}),
			"counts": List([]Value{Int64(1), Int64(2)}),
		}),
		Struct(map[string]Value{
			"tags":   List([]Value{}),
			"counts": Null(),
		}),
		Struct(map[string]Value{
			"tags":   Null(),
			"counts": List([]Value{}),
		}),
		Struct(map[string]Value{
			"tags":   List([]Value{Null(), String("x"), Null()}),
			"counts": List([]Value{Int64(7)}),
		}),
	}
	assertRowsEqual(t, rows, roundTrip(t, schema, rows, nil))
}

func TestEmptyAndNullStructs(t *testing.T) {
	schema, err := Compile(Group("root", Required,
		Group("empty", Optional),
		Group("meta", Optional,
			Leaf("id", Required, TypeInt64),
			Group("emptyInner", Optional),
		),
	))
	if err != nil {
		t.Fatal(err)
	}
	rows := []Value{
		Struct(map[string]Value{
			"empty": Struct(map[string]Value{}),
			"meta": Struct(map[string]Value{
				"id":         Int64(1),
				"emptyInner": Struct(map[string]Value{}),
			}),
		}),
		Struct(map[string]Value{
			"empty": Null(),
			"meta": Struct(map[string]Value{
				"id":         Int64(2),
				"emptyInner": Null(),
			}),
		}),
		Struct(map[string]Value{"empty": Null(), "meta": Null()}),
	}
	assertRowsEqual(t, rows, roundTrip(t, schema, rows, nil))
}

func TestNestedListsAndGroups(t *testing.T) {
	schema, err := Compile(Group("root", Required,
		Leaf("top", Required, TypeInt32),
		Group("order", Required,
			Leaf("ids", Repeated, TypeInt32),
			ListOf("lines", Group("line", Required,
				Leaf("qty", Required, TypeInt64),
				ListOf("notes", StringLeaf("element", Optional)),
			)),
		),
	))
	if err != nil {
		t.Fatal(err)
	}
	rows := []Value{
		Struct(map[string]Value{
			"top": Int32(10),
			"order": Struct(map[string]Value{
				"ids": List([]Value{Int32(3), Int32(4), Int32(5)}),
				"lines": List([]Value{
					Struct(map[string]Value{
						"qty":   Int64(2),
						"notes": List([]Value{String("n1")}),
					}),
					Struct(map[string]Value{
						"qty":   Int64(0),
						"notes": List([]Value{}),
					}),
				}),
			}),
		}),
		Struct(map[string]Value{
			"top": Int32(11),
			"order": Struct(map[string]Value{
				"ids":   List([]Value{}),
				"lines": List([]Value{}),
			}),
		}),
		Struct(map[string]Value{
			"top": Int32(12),
			"order": Struct(map[string]Value{
				"ids":   List([]Value{Int32(9)}),
				"lines": Null(),
			}),
		}),
	}
	assertRowsEqual(t, rows, roundTrip(t, schema, rows, nil))
}

func TestDictionaryFallbackAndMultiPage(t *testing.T) {
	schema, err := Compile(Group("root", Required,
		StringLeaf("code", Optional),
		Leaf("n", Required, TypeInt32),
	))
	if err != nil {
		t.Fatal(err)
	}
	rows := make([]Value, 300)
	for i := range rows {
		code := Null()
		if i%3 != 0 {
			code = String(string(rune('a'+i%26)) + "-" + itoa(i))
		}
		rows[i] = Struct(map[string]Value{"code": code, "n": Int32(int32(i))})
	}
	opts := &Options{PageSize: 256, MaxDictionaryBytes: 64}
	assertRowsEqual(t, rows, roundTrip(t, schema, rows, opts))
}

func TestMultipleRowGroups(t *testing.T) {
	schema, err := Compile(Group("root", Required, Leaf("n", Optional, TypeInt64)))
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	w, err := NewWriter(&buf, schema, nil)
	if err != nil {
		t.Fatal(err)
	}
	all := []Value{}
	for g := 0; g < 3; g++ {
		for i := 0; i < 4; i++ {
			row := Struct(map[string]Value{"n": Int64(int64(g*10 + i))})
			if err := w.WriteRow(row); err != nil {
				t.Fatal(err)
			}
			all = append(all, row)
		}
		if err := w.Flush(); err != nil {
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
	if r.NumRowGroups() != 3 || r.NumRows() != 12 {
		t.Fatalf("groups=%d rows=%d", r.NumRowGroups(), r.NumRows())
	}
	got, err := r.ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	assertRowsEqual(t, all, got)
}

func TestHybridRoundTrip(t *testing.T) {
	cases := [][]int{
		{1, 1, 1, 1, 1, 1, 1, 1, 1, 2, 3, 4},
		{0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0},
		{7, 3, 7, 3, 7, 3, 7, 3, 7, 3, 1, 1, 1, 1, 1, 1, 1, 1},
		{5},
		{},
	}
	for _, vals := range cases {
		width := 0
		for _, v := range vals {
			if w := bitWidth(v); w > width {
				width = w
			}
		}
		encoded := encodeRLEBitPackedHybrid(vals, width)
		dec := newHybridDecoder(encoded, width)
		got, err := dec.decodeAll(len(vals))
		if err != nil {
			t.Fatalf("decode: %v", err)
		}
		for i := range vals {
			if got[i] != vals[i] {
				t.Fatalf("at %d: %d != %d (width=%d)", i, got[i], vals[i], width)
			}
		}
	}
}

func TestBitPackingRoundTrip(t *testing.T) {
	vals := []int{0, 1, 2, 3, 4, 5, 6, 7, 7, 6, 5, 4, 3, 2, 1, 0, 1, 3}
	for width := 1; width <= 5; width++ {
		mask := (1 << width) - 1
		limited := make([]int, len(vals))
		for i, v := range vals {
			limited[i] = v & mask
		}
		packed := packBitsLSB(limited, width)
		got := unpackBitsLSB(packed, width, len(limited))
		for i, v := range limited {
			if got[i] != v {
				t.Fatalf("width %d at %d: %d != %d", width, i, got[i], v)
			}
		}
	}
	if got := packBitsLSB([]int{0, 0, 0}, 0); got != nil {
		t.Fatalf("zero width should encode to nil, got %v", got)
	}
}

func TestStatisticsAndMetadata(t *testing.T) {
	schema, err := Compile(Group("root", Required,
		Leaf("n", Optional, TypeInt32),
		StringLeaf("s", Optional),
	))
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	w, err := NewWriter(&buf, schema, &Options{MaxDictionaryBytes: 1 << 20})
	if err != nil {
		t.Fatal(err)
	}
	rows := []Value{
		Struct(map[string]Value{"n": Int32(5), "s": String("b")}),
		Struct(map[string]Value{"n": Int32(-2), "s": String("a")}),
		Struct(map[string]Value{"n": Null(), "s": String("a")}),
		Struct(map[string]Value{"n": Int32(9), "s": Null()}),
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
	rg := r.meta.rowGroups[0]
	if len(rg.columns) != 2 {
		t.Fatalf("columns=%d", len(rg.columns))
	}
	statsN := rg.columns[0].metaData.stats
	if statsN.nullCount != 1 || statsN.distinctCnt != 3 {
		t.Fatalf("n stats: null=%d distinct=%d", statsN.nullCount, statsN.distinctCnt)
	}
	if int32FromPlain(statsN.min) != -2 || int32FromPlain(statsN.max) != 9 {
		t.Fatalf("n min/max wrong: %v %v", statsN.min, statsN.max)
	}
	statsS := rg.columns[1].metaData.stats
	if statsS.nullCount != 1 || string(statsS.min) != "a" || string(statsS.max) != "b" {
		t.Fatalf("string stats wrong: %+v", statsS)
	}
	got, err := r.ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	assertRowsEqual(t, rows, got)

	// Low-cardinality string column must have taken the dictionary path.
	encs := map[int32]bool{}
	for _, e := range rg.columns[1].metaData.encodings {
		encs[e] = true
	}
	if !encs[encRLEDictionary] || rg.columns[1].metaData.dictionaryOffset < 0 {
		t.Fatalf("expected dictionary encoding, got enc=%v dictOff=%d",
			rg.columns[1].metaData.encodings, rg.columns[1].metaData.dictionaryOffset)
	}
}

func TestRequiredFieldMissingRejected(t *testing.T) {
	schema, err := Compile(Group("root", Required, Leaf("n", Required, TypeInt32)))
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	w, err := NewWriter(&buf, schema, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := w.WriteRow(Struct(map[string]Value{})); err == nil {
		t.Fatal("expected error for missing required field")
	}
}
