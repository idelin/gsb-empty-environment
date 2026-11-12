package hpack

import (
	"encoding/hex"
	"testing"
)

func mustHex(t *testing.T, s string) []byte {
	t.Helper()
	b, err := hex.DecodeString(s)
	if err != nil {
		t.Fatalf("bad hex %q: %v", s, err)
	}
	return b
}

func fieldsEqual(got []Field, want []Field) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

// RFC 7541 appendix C.2.1: literal without indexing, Huffman off.
func TestDecodeFull_C21(t *testing.T) {
	block := mustHex(t, "040c2f73616d706c652f70617468")
	d := NewDecoder(4096, 4096)
	got, err := d.DecodeFull(block)
	if err != nil {
		t.Fatalf("DecodeFull: %v", err)
	}
	want := []Field{{Name: ":path", Value: "/sample/path"}}
	if !fieldsEqual(got, want) {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}

// RFC 7541 appendix C.2.2: literal never indexed.
func TestDecodeFull_C22(t *testing.T) {
	block := mustHex(t, "100870617373776f726406736563726574")
	d := NewDecoder(4096, 4096)
	got, err := d.DecodeFull(block)
	if err != nil {
		t.Fatalf("DecodeFull: %v", err)
	}
	want := []Field{{Name: "password", Value: "secret", Sensitive: true}}
	if !fieldsEqual(got, want) {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}

// RFC 7541 appendix C.2.4: indexed header field.
func TestDecodeFull_C24(t *testing.T) {
	block := mustHex(t, "82")
	d := NewDecoder(4096, 4096)
	got, err := d.DecodeFull(block)
	if err != nil {
		t.Fatalf("DecodeFull: %v", err)
	}
	want := []Field{{Name: ":method", Value: "GET"}}
	if !fieldsEqual(got, want) {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}

// RFC 7541 appendix C.3: a full request exercising the dynamic table.
func TestDecodeFull_C3Request(t *testing.T) {
	blocks := []string{
		"828684410f7777772e6578616d706c652e636f6d",
		"828684be58086e6f2d6361636865",
		"828785bf400a637573746f6d2d6b65790c637573746f6d2d76616c7565",
	}
	d := NewDecoder(4096, 4096)

	first, err := d.DecodeFull(mustHex(t, blocks[0]))
	if err != nil {
		t.Fatalf("block 1: %v", err)
	}
	wantFirst := []Field{
		{Name: ":method", Value: "GET"},
		{Name: ":scheme", Value: "http"},
		{Name: ":path", Value: "/"},
		{Name: ":authority", Value: "www.example.com"},
	}
	if !fieldsEqual(first, wantFirst) {
		t.Fatalf("block 1 got %+v", first)
	}
	if d.dynSize == 0 {
		t.Fatal("dynamic table should contain the authority entry")
	}

	second, err := d.DecodeFull(mustHex(t, blocks[1]))
	if err != nil {
		t.Fatalf("block 2: %v", err)
	}
	if len(second) != 5 || second[4].Name != "cache-control" || second[4].Value != "no-cache" {
		t.Fatalf("block 2 got %+v", second)
	}

	third, err := d.DecodeFull(mustHex(t, blocks[2]))
	if err != nil {
		t.Fatalf("block 3: %v", err)
	}
	last := third[len(third)-1]
	if last.Name != "custom-key" || last.Value != "custom-value" {
		t.Fatalf("block 3 tail got %+v", last)
	}
}

func TestDecodeFull_HuffmanRoundTrip(t *testing.T) {
	for _, useHuffman := range []bool{false, true} {
		enc := newEncoder()
		enc.huffman = useHuffman
		want := []Field{
			{Name: ":method", Value: "GET"},
			{Name: ":authority", Value: "api.example.org"},
			{Name: "x-token", Value: "abcDEF-0123_zzz"},
		}
		enc.encodeFields(want...)

		d := NewDecoder(4096, 4096)
		got, err := d.DecodeFull(enc.bytes())
		if err != nil {
			t.Fatalf("huffman=%v: %v", useHuffman, err)
		}
		if !fieldsEqual(got, want) {
			t.Fatalf("huffman=%v got %+v, want %+v", useHuffman, got, want)
		}
	}
}

func TestDecodeFull_DynamicIndexReferenced(t *testing.T) {
	enc := newEncoder()
	// First block adds custom-key/custom-value with incremental indexing.
	enc.encodeFields(Field{Name: "custom-key", Value: "custom-value"})
	d := NewDecoder(4096, 4096)
	if _, err := d.DecodeFull(enc.bytes()); err != nil {
		t.Fatalf("first block: %v", err)
	}

	// Dynamic index 62 (static table length + 1) now names custom-key.
	ref := newEncoder()
	ref.literalIndexedName(62, "second-value")
	got, err := d.DecodeFull(ref.bytes())
	if err != nil {
		t.Fatalf("second block: %v", err)
	}
	want := []Field{{Name: "custom-key", Value: "second-value"}}
	if !fieldsEqual(got, want) {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}

func TestDecodeFull_SizeUpdateShrinksTable(t *testing.T) {
	enc := newEncoder()
	enc.encodeFields(Field{Name: "big-name", Value: "big-value"})
	d := NewDecoder(4096, 4096)
	if _, err := d.DecodeFull(enc.bytes()); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if len(d.dyn) == 0 {
		t.Fatal("expected a dynamic entry")
	}

	upd := newEncoder()
	upd.sizeUpdate(0)
	if _, err := d.DecodeFull(upd.bytes()); err != nil {
		t.Fatalf("size update: %v", err)
	}
	if len(d.dyn) != 0 || d.dynSize != 0 {
		t.Fatalf("table not emptied: %+v", d.dyn)
	}

	bad := newEncoder()
	bad.sizeUpdate(4097)
	d2 := NewDecoder(4096, 4096)
	if _, err := d2.DecodeFull(bad.bytes()); err == nil {
		t.Fatal("expected error for oversized table update")
	}
}

func TestDecodeFull_Errors(t *testing.T) {
	d := NewDecoder(4096, 4096)
	cases := map[string][]byte{
		"indexed out of range": {0x85}, // index 5 is valid; use something larger
		"truncated varint":     {0x7f, 0x80},
		"truncated string":     {0x00, 0x05, 'a'},
		"bad huffman padding":  buildBadHuffman(),
	}
	// Replace the indexed case with a definitely-invalid index.
	cases["indexed out of range"] = appendVarint(nil, 1000, 7, 0x80)
	for name, block := range cases {
		if _, err := d.DecodeFull(block); err == nil {
			t.Errorf("%s: expected error, got nil", name)
		}
	}
}

func buildBadHuffman() []byte {
	// Huffman string of one byte with trailing zero padding, which violates
	// the "padding is an EOS prefix" rule.
	return []byte{0x88, 0x00}
}

func TestHuffmanEncodeDecodeRoundTrip(t *testing.T) {
	inputs := []string{"", "a", "hello world", "GET", "www.example.com", "0123456789abcdef"}
	for _, in := range inputs {
		encoded := huffmanEncode(nil, []byte(in))
		decoded, err := huffmanDecode(encoded)
		if err != nil {
			t.Fatalf("input %q: %v", in, err)
		}
		if string(decoded) != in {
			t.Fatalf("got %q, want %q", decoded, in)
		}
	}
}
