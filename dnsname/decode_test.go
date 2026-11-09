package main

import (
	"encoding/hex"
	"encoding/json"
	"reflect"
	"testing"
)

func ptr(off int) [2]byte {
	return [2]byte{0xc0 | byte(off>>8), byte(off)}
}

func labelsHex(labels ...string) []string {
	out := make([]string, len(labels))
	for i, l := range labels {
		out[i] = hex.EncodeToString([]byte(l))
	}
	return out
}

func mustItem(t *testing.T, packet []byte, off int) *memoEntry {
	t.Helper()
	d := &decoder{packet: packet, memo: map[int]*memoEntry{}}
	got := d.decodeName(off)
	if !got.ok {
		t.Fatalf("offset %d: expected valid name, got null", off)
	}
	return got
}

func assertNull(t *testing.T, packet []byte, off int) {
	t.Helper()
	d := &decoder{packet: packet, memo: map[int]*memoEntry{}}
	got := d.decodeName(off)
	if got.ok {
		t.Fatalf("offset %d: expected null, got labels %v", off, got.labels)
	}
}

func assertItem(t *testing.T, packet []byte, off int, wantLabels []string, wantNext int) {
	t.Helper()
	got := mustItem(t, packet, off)
	labelHex := make([]string, len(got.labels))
	for i, l := range got.labels {
		labelHex[i] = hex.EncodeToString(l)
	}
	if !reflect.DeepEqual(labelHex, wantLabels) || got.nextField != wantNext {
		t.Fatalf("offset %d: got (%v, %d), want (%v, %d)", off, labelHex, got.nextField, wantLabels, wantNext)
	}
}

// Plain and single-hop names; the pointer's next-field must be the byte after
// the two-byte pointer rather than anything reached through the jump.
func TestPlainAndPointer(t *testing.T) {
	packet := []byte{
		0x03, 'a', 'b', 'c', 0x03, 'x', 'y', 'z', 0x00,
		0x02, 'p', 'q', 0xc0, 0x04,
		0x00, 0xff,
	}
	assertItem(t, packet, 0, labelsHex("abc", "xyz"), 9)
	assertItem(t, packet, 9, labelsHex("pq", "xyz"), 14)

	// Root name.
	assertItem(t, packet, 14, []string{}, 15)
}

func TestMultiHopAndForwardReference(t *testing.T) {
	// 0: pointer to 2; 2: pointer to 4; 4: forward target defined later.
	p0 := ptr(2)
	p2 := ptr(4)
	packet := []byte{p0[0], p0[1], p2[0], p2[1], 0, 0, 0, 0}
	packet[4] = 0x02
	packet[5] = 'h'
	packet[6] = 'o'
	packet[7] = 0x00

	d := &decoder{packet: packet, memo: map[int]*memoEntry{}}
	e0 := d.decodeName(0)
	if !e0.ok || e0.nextField != 2 || len(e0.labels) != 1 || string(e0.labels[0]) != "ho" {
		t.Fatalf("multi-hop at 0: got %+v", e0)
	}
	e2 := d.decodeName(2)
	if !e2.ok || e2.nextField != 4 || string(e2.labels[0]) != "ho" {
		t.Fatalf("multi-hop at 2: got %+v", e2)
	}
}

// Several starts share one suffix and one long pointer chain; the chain is
// walked a single time, and every start reports its own encoded end offset.
func TestSharedSuffixAndMemo(t *testing.T) {
	// Layout:
	//   0: label "a", then pointer to 10
	//   4: label "b", then pointer to 10
	//   8: pointer to 10
	//  10: plain name "xyz" ending in root at 15
	pa := ptr(10)
	pb := ptr(10)
	p8 := ptr(10)
	packet := []byte{
		0x01, 'a', pa[0], pa[1],
		0x01, 'b', pb[0], pb[1],
		p8[0], p8[1],
		0x03, 'x', 'y', 'z', 0x00,
	}

	d := &decoder{packet: packet, memo: map[int]*memoEntry{}}
	for _, start := range []int{0, 4, 8} {
		e := d.decodeName(start)
		if !e.ok {
			t.Fatalf("start %d unexpectedly null", start)
		}
	}
	// Every packet offset involved is resolved exactly once.
	for _, off := range []int{0, 4, 8, 10} {
		if !d.memo[off].ok {
			t.Fatalf("offset %d memo not resolved ok: %+v", off, d.memo[off])
		}
	}
	if d.memo[0].nextField != 4 || d.memo[4].nextField != 8 || d.memo[8].nextField != 10 {
		t.Fatalf("wrong encoded end offsets: %d %d %d",
			d.memo[0].nextField, d.memo[4].nextField, d.memo[8].nextField)
	}

	// Shared chain of 500 pointers; 100 different starts must stay linear.
	chain := make([]byte, 2*500+2)
	for i := 0; i < 500; i++ {
		p := ptr(2 * (i + 1))
		chain[2*i] = p[0]
		chain[2*i+1] = p[1]
	}
	chain[1000] = 0x00
	chain[1001] = 0x00

	starts := make([]int, 100)
	for i := range starts {
		starts[i] = 2 * i
	}
	results := decodePacket(chain, starts)
	for i, r := range results {
		tp, ok := r.(tuple)
		if !ok {
			t.Fatalf("shared chain start %d: got %+v", starts[i], r)
		}
		if len(tp.labels) != 0 || tp.nextField != starts[i]+2 {
			t.Fatalf("shared chain start %d: got %+v", starts[i], r)
		}
	}

	// Walking the same chain through a shared decoder must resolve each
	// offset exactly once (500 pointers + 1 root entry).
	d3 := &decoder{packet: chain, memo: map[int]*memoEntry{}}
	for _, s := range starts {
		d3.decodeName(s)
	}
	if len(d3.memo) != 501 {
		t.Fatalf("memo size %d shows repeated chain walks", len(d3.memo))
	}
}

func TestCycles(t *testing.T) {
	// Self loop.
	self := ptr(0)
	assertNull(t, []byte{self[0], self[1]}, 0)

	// Two-node cycle 0 -> 4 -> 0.
	p0 := ptr(4)
	p4 := ptr(0)
	assertNull(t, []byte{p0[0], p0[1], 0, 0, p4[0], p4[1]}, 0)

	// A start that leads into a cycle after one literal label.
	// 0: "a" -> ptr 4 ; 4 -> ptr 2 ; 2 -> ptr 4.
	pa := ptr(4)
	p4b := ptr(2)
	p2 := ptr(4)
	packet := []byte{
		0x01, 'a', pa[0], pa[1],
		p4b[0], p4b[1],
		p2[0], p2[1],
	}
	assertNull(t, packet, 0)
}

func TestReservedPrefixes(t *testing.T) {
	// 01 prefix: extended label type, not allowed.
	assertNull(t, []byte{0x41, 'a', 0x00}, 0)
	// 10 prefix: reserved.
	assertNull(t, []byte{0x80, 0x00}, 0)
	assertNull(t, []byte{0xff, 0x00}, 0)
	// Reserved byte reached through a pointer.
	p := ptr(2)
	assertNull(t, []byte{p[0], p[1], 0x80, 0x00}, 0)
}

func TestOutOfBounds(t *testing.T) {
	// Start at / past packet end.
	assertNull(t, []byte{0x00}, 1)
	assertNull(t, []byte{0x00}, 5)

	// Truncated pointer (second byte missing).
	assertNull(t, []byte{0xc0}, 0)

	// Pointer target outside the packet.
	p := ptr(0x0100)
	assertNull(t, []byte{p[0], p[1]}, 0)

	// Label body runs past the packet.
	assertNull(t, []byte{0x03, 'a', 'b'}, 0)
}

func TestNameLongerThan255(t *testing.T) {
	build := func(labelLens ...int) []byte {
		var out []byte
		for _, n := range labelLens {
			out = append(out, byte(n))
			for i := 0; i < n; i++ {
				out = append(out, 'x')
			}
		}
		out = append(out, 0x00)
		return out
	}

	// 63+63+63+61 = 250 label bytes + 4 length octets + root = 255: valid.
	edge := build(63, 63, 63, 61)
	if len(edge) != 255 {
		t.Fatalf("test setup: edge packet length %d", len(edge))
	}
	mustItem(t, edge, 0)

	// One byte more: 256 total wire length is invalid.
	tooLong := build(63, 63, 63, 62)
	if len(tooLong) != 256 {
		t.Fatalf("test setup: long packet length %d", len(tooLong))
	}
	assertNull(t, tooLong, 0)

	// Expanded name exceeds 255 only after a suffix is joined via pointer.
	// 0: 63-byte label -> ptr 65 ; 65: 63+63+62 labels + root = 192 wire bytes.
	var packet []byte
	packet = append(packet, 63)
	for i := 0; i < 63; i++ {
		packet = append(packet, 'a')
	}
	if len(packet) != 64 {
		t.Fatalf("test setup: label prefix length %d", len(packet))
	}
	p := ptr(66)
	packet = append(packet, p[0], p[1])
	if len(packet) != 66 {
		t.Fatalf("test setup: prefix length %d", len(packet))
	}
	suffix := build(63, 63, 62)
	packet = append(packet, suffix...)
	assertNull(t, packet, 0)
}

func TestDecodePacketAndJSONShape(t *testing.T) {
	packet := []byte{
		0x03, 'a', 'b', 'c', 0x00,
		0xc0, 0x00,
		0x80, 0x00,
		0xc0, 0x7f,
	}
	got := decodePacket(packet, []int{5, 7, 9, 0})
	// offsets 7 (reserved 0x80) and 9 (pointer out of bounds) are invalid.
	if got[1] != nil || got[2] != nil {
		t.Fatalf("invalid offsets must be null: %+v", got)
	}
	assertTuple(t, got[0], labelsHex("abc"), 7)
	assertTuple(t, got[3], labelsHex("abc"), 5)

	b, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	want := `[[["616263"],7],null,null,[["616263"],5]]`
	if string(b) != want {
		t.Fatalf("JSON shape:\n got %s\nwant %s", b, want)
	}
}

func assertTuple(t *testing.T, item any, wantLabels []string, wantNext int) {
	t.Helper()
	tp, ok := item.(tuple)
	if !ok {
		t.Fatalf("expected 2-element tuple, got %#v", item)
	}
	if !reflect.DeepEqual(tp.labels, wantLabels) || tp.nextField != wantNext {
		t.Fatalf("tuple: got (%v,%d), want (%v,%d)", tp.labels, tp.nextField, wantLabels, wantNext)
	}
}
