package main

import (
	"bytes"
	"testing"
)

// TestCursorAcrossBlockBoundary exercises reads that span multiple paged
// blocks, including a discard length much larger than one block.
func TestCursorAcrossBlockBoundary(t *testing.T) {
	size := blockSize*3 + 12345
	data := make([]byte, size)
	for i := range data {
		data[i] = byte(i * 7)
	}
	pg := newPager(bytes.NewReader(data))
	c := pg.newCursor(0)

	first := blockSize + 10
	if err := c.discard(int64(first)); err != nil {
		t.Fatalf("discard first span: %v", err)
	}
	if c.Offset() != int64(first) {
		t.Fatalf("offset = %d, want %d", c.Offset(), first)
	}

	b, err := c.readByte()
	if err != nil {
		t.Fatalf("readByte: %v", err)
	}
	if b != data[first] {
		t.Fatalf("byte = %d, want %d", b, data[first])
	}

	rest := int64(size - first - 1)
	if err := c.discard(rest); err != nil {
		t.Fatalf("discard remainder: %v", err)
	}
	if c.Offset() != int64(size) {
		t.Fatalf("final offset = %d, want %d", c.Offset(), size)
	}
	if _, err := c.readByte(); err == nil {
		t.Fatal("expected EOF past the frontier")
	}
}
