package main

import "testing"

func TestEdgeDupAndEmpty(t *testing.T) {
	// duplicate offsets share memo; empty at yields empty array
	p := []byte{0x03, 'a', 'b', 'c', 0x00}
	r := decodePacket(p, []int{0, 0, 0})
	if len(r) != 3 {
		t.Fatal("dup count")
	}
	r2 := decodePacket(p, nil)
	if len(r2) != 0 {
		t.Fatal("empty at")
	}
	// label length 63 is max; 64-length label still parses structurally (DNS
	// wire only forbids >63 at protocol level, not at compression decoding).
	long := append([]byte{63}, make([]byte, 63)...)
	long = append(long, 0)
	d := &decoder{packet: long, memo: map[int]*memoEntry{}}
	if !d.decodeName(0).ok {
		t.Fatal("63-byte label should decode")
	}
}
