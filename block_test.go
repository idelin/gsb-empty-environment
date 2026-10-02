package blockindex

import (
	"bytes"
	"testing"
)

// TestBlockLayoutExact pins the exact wire layout of a block.
func TestBlockLayoutExact(t *testing.T) {
	entries := []Entry{
		{Key: 5, Value: []byte("ab")},
		{Key: 9, Value: []byte{}},
		{Key: 300, Value: []byte("x")},
	}
	raw := EncodeBlock(1, entries)
	want := []byte{
		'B', 'L', 'K', '1',
		0x01,                 // version
		0x03,                 // count
		0x05,                 // firstKey
		0x00, 0x02, 'a', 'b', // delta 0, len 2, "ab"
		0x04, 0x00, // delta 4, len 0
		0xa3, 0x02, 0x01, 'x', // delta 291 (varint 0xa3 0x02), len 1, "x"
	}
	if !bytes.Equal(raw, want) {
		t.Fatalf("layout mismatch:\n got %x\nwant %x", raw, want)
	}
	if len(raw) != 17 {
		t.Fatalf("block occupies %d bytes, want 17", len(raw))
	}
	ver, got, err := DecodeBlock(raw)
	if err != nil {
		t.Fatal(err)
	}
	if ver != 1 || len(got) != 3 || got[2].Key != 300 || string(got[0].Value) != "ab" {
		t.Fatalf("roundtrip mismatch: ver=%d entries=%v", ver, got)
	}
}

func TestBlockDecodeErrors(t *testing.T) {
	good := EncodeBlock(2, []Entry{{Key: 1, Value: []byte("v")}, {Key: 4, Value: nil}})
	cases := map[string][]byte{
		"empty":          {},
		"bad magic":      append([]byte("XXXX"), good[4:]...),
		"truncated":      good[:len(good)-1],
		"trailing bytes": append(append([]byte{}, good...), 0x00),
		"bad varint":     {'B', 'L', 'K', '1', 0xff, 0xff, 0xff},
	}
	for name, raw := range cases {
		if _, _, err := DecodeBlock(raw); err == nil {
			t.Errorf("%s: expected decode error", name)
		}
	}
	// Non-increasing keys: hand-encode delta 0 for the second entry.
	bad := []byte{'B', 'L', 'K', '1', 0x01, 0x02, 0x05, 0x00, 0x00, 0x00, 0x00}
	if _, _, err := DecodeBlock(bad); err == nil {
		t.Error("non-increasing keys: expected decode error")
	}
	// First delta must be zero.
	bad2 := []byte{'B', 'L', 'K', '1', 0x01, 0x01, 0x05, 0x01, 0x00}
	if _, _, err := DecodeBlock(bad2); err == nil {
		t.Error("nonzero first delta: expected decode error")
	}
}

// TestNodeLayoutExact pins the exact wire layout of a summary node.
func TestNodeLayoutExact(t *testing.T) {
	var dgA, dgB [DigestLen]byte
	dgA[0] = 0xAA
	dgB[0] = 0xBB
	children := []ChildInfo{
		{MinKey: 10, MaxKey: 15, Entries: 3, Blocks: 1, Digest: dgA},
		{MinKey: 20, MaxKey: 25, Entries: 4, Blocks: 2, Digest: dgB},
	}
	raw := EncodeNode(2, 1, children)
	want := []byte{
		'S', 'U', 'M', '1',
		0x02,                   // version
		0x01,                   // level
		0x02,                   // nChild
		0x0a, 0x05, 0x03, 0x01, // child0: min 10, maxdelta 5, entries 3, blocks 1
	}
	want = append(want, dgA[:]...)
	want = append(want,
		0x0a, 0x05, 0x04, 0x02, // child1: mindelta 10, maxdelta 5, entries 4, blocks 2
	)
	want = append(want, dgB[:]...)
	if !bytes.Equal(raw, want) {
		t.Fatalf("layout mismatch:\n got %x\nwant %x", raw, want)
	}
	if len(raw) != 79 {
		t.Fatalf("node occupies %d bytes, want 79", len(raw))
	}
	ver, lvl, got, err := DecodeNode(raw)
	if err != nil {
		t.Fatal(err)
	}
	if ver != 2 || lvl != 1 || len(got) != 2 || got[1] != children[1] {
		t.Fatalf("roundtrip mismatch: ver=%d lvl=%d children=%v", ver, lvl, got)
	}
}

func TestNodeDecodeErrors(t *testing.T) {
	var dg [DigestLen]byte
	good := EncodeNode(1, 1, []ChildInfo{{MinKey: 1, MaxKey: 2, Entries: 1, Blocks: 1, Digest: dg}})
	if _, _, _, err := DecodeNode(good[:len(good)-5]); err == nil {
		t.Error("truncated node: expected error")
	}
	// Overlapping child ranges.
	overlap := EncodeNode(1, 1, []ChildInfo{
		{MinKey: 1, MaxKey: 10, Entries: 1, Blocks: 1, Digest: dg},
		{MinKey: 10, MaxKey: 20, Entries: 1, Blocks: 1, Digest: dg},
	})
	// EncodeNode would emit a mindelta of 9 which decodes to MinKey 10; the
	// decoder must reject the overlap with the previous MaxKey.
	if _, _, _, err := DecodeNode(overlap); err == nil {
		t.Error("overlapping ranges: expected error")
	}
}
