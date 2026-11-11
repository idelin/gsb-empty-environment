package walrestore

import (
	"bytes"
	"context"
	"errors"
	"io"
	"testing"
)

func TestNonZeroPaddingInNonTailBlockIsCorruption(t *testing.T) {
	const blockSize = 48
	data, _ := buildWAL(t, blockSize, manyBatches(12))

	// Find the first block that ends in zero padding and dirty one padding byte.
	damaged := append([]byte(nil), data...)
	blockCount := (len(damaged) + blockSize - 1) / blockSize
	found := false
	for block := 0; block < blockCount-1 && !found; block++ {
		start := block * blockSize
		end := start + blockSize
		for pos := end - 1; pos >= start; pos-- {
			if damaged[pos] == 0 {
				damaged[pos] = 0x7F
				found = true
				break
			}
		}
	}
	if !found {
		t.Fatal("test setup: no zero padding found in an early block")
	}

	_, err := Recover(context.Background(), bytes.NewReader(damaged), int64(len(damaged)), WithBlockSize(blockSize))
	if !errors.Is(err, ErrCorruption) {
		t.Fatalf("err = %v, want ErrCorruption", err)
	}
}

func TestMultiFragmentTornTailIsTruncated(t *testing.T) {
	const blockSize = 40
	large := bytes.Repeat([]byte("L"), 500)
	data, _ := buildWAL(t, blockSize, [][]Entry{
		{put("a", "1")},
		{put("big", string(large))},
	})

	// Chop the log mid-record, inside the final block.
	chopped := data[:len(data)-12]
	store, err := Recover(context.Background(), bytes.NewReader(chopped), int64(len(chopped)), WithBlockSize(blockSize))
	if err != nil {
		t.Fatalf("Recover: %v", err)
	}
	if !store.Truncated() {
		t.Fatal("expected truncation for incomplete multi-fragment record")
	}
	if store.MaxSeq() != 1 {
		t.Fatalf("MaxSeq = %d, only the first batch must be published", store.MaxSeq())
	}

	scanner, err := Scan(bytes.NewReader(chopped), int64(len(chopped)), WithBlockSize(blockSize))
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	for {
		_, err := scanner.Next()
		if errors.Is(err, ErrTruncated) {
			break
		}
		if err != nil {
			t.Fatalf("unexpected scanner error: %v", err)
		}
	}
	if !scanner.Truncated() {
		t.Fatal("scanner must report truncation")
	}
	if scanner.TruncatedOffset() < 0 || scanner.TruncationError() == nil {
		t.Fatal("scanner truncation accessors not populated")
	}
}

func TestInvalidBlockSizeRejected(t *testing.T) {
	if _, err := Scan(bytes.NewReader(nil), 0, WithBlockSize(fragmentHeaderSize)); err == nil {
		t.Fatal("expected error for block size <= header size")
	}
	var buf bytes.Buffer
	if _, err := NewWriter(&buf, WithBlockSize(2)); err == nil {
		t.Fatal("expected error for tiny writer block size")
	}
	if _, err := Recover(context.Background(), bytes.NewReader(nil), -1); err == nil {
		t.Fatal("expected error for negative log size")
	}
}

func TestWriterRejectsBadBatchesAndReuse(t *testing.T) {
	var buf bytes.Buffer
	w, err := NewWriter(&buf, WithBlockSize(64))
	if err != nil {
		t.Fatalf("NewWriter: %v", err)
	}
	if w.NextSeq() != 1 {
		t.Fatalf("NextSeq = %d, want 1", w.NextSeq())
	}
	if _, err := w.WriteBatch(nil); err == nil {
		t.Fatal("empty batch must be rejected")
	}
	if seq, err := w.WriteBatch([]Entry{put("k", "v")}); err != nil || seq != 1 {
		t.Fatalf("first batch: seq=%d err=%v", seq, err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if _, err := w.WriteBatch([]Entry{put("k", "v2")}); err == nil {
		t.Fatal("write after Close must fail")
	}
	if err := w.Close(); err != nil {
		t.Fatalf("double Close must be idempotent: %v", err)
	}
}

func TestDecodeBatchRejectsMalformedRecords(t *testing.T) {
	cases := map[string][]byte{
		"short header":      make([]byte, 4),
		"zero entries":      encodeBatch(1, nil),
		"truncated entry":   encodeBatch(1, []Entry{put("k", "v")})[:14],
		"unknown operation": malformedOpRecord(),
		"trailing bytes":    append(encodeBatch(1, []Entry{put("k", "v")}), 9, 9, 9),
	}
	for name, record := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := decodeBatch(record); err == nil {
				t.Fatalf("%s: expected decode error", name)
			}
		})
	}
}

func malformedOpRecord() []byte {
	record := encodeBatch(1, []Entry{put("k", "v")})
	// Entry op is the first byte after the 12-byte batch header.
	record[12] = 99
	return record
}

func TestIteratorCloseEndsScan(t *testing.T) {
	data, _ := buildWAL(t, 64, manyBatches(4))
	scanner, err := Scan(bytes.NewReader(data), int64(len(data)), WithBlockSize(64))
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if _, err := scanner.Next(); err != nil {
		t.Fatalf("Next: %v", err)
	}
	if err := scanner.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if _, err := scanner.Next(); !errors.Is(err, io.EOF) {
		t.Fatalf("after Close: err = %v, want io.EOF", err)
	}
}

func TestOpenMissingFile(t *testing.T) {
	if _, err := Open(context.Background(), "does-not-exist.wal"); err == nil {
		t.Fatal("expected error opening a missing WAL file")
	}
}

func TestShortFinalBlockWithPaddingIsTornTail(t *testing.T) {
	const blockSize = 48
	data, _ := buildWAL(t, blockSize, manyBatches(10))

	// Keep a complete multi-block prefix plus a partial final block whose bytes
	// are mostly missing. The reader must truncate, not index out of range.
	tailStart := int64((len(data)+blockSize-1)/blockSize-1) * blockSize
	chopped := data[:tailStart+6]

	store, err := Recover(context.Background(), bytes.NewReader(chopped), int64(len(chopped)), WithBlockSize(blockSize))
	if err != nil {
		t.Fatalf("Recover: %v", err)
	}
	if !store.Truncated() || store.TruncatedOffset() != tailStart {
		t.Fatalf("unexpected truncation state: %+v off=%d", store.Truncated(), store.TruncatedOffset())
	}
	assertPrefix(t, store, 10)
}
