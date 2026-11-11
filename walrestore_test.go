package walrestore

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

// buildWAL writes batches into an in-memory WAL using the given block size and
// returns its raw bytes together with the assigned sequence numbers.
func buildWAL(t *testing.T, blockSize int, batches [][]Entry) ([]byte, []uint64) {
	t.Helper()

	var buf bytes.Buffer
	w, err := NewWriter(&buf, WithBlockSize(blockSize))
	if err != nil {
		t.Fatalf("NewWriter: %v", err)
	}

	seqs := make([]uint64, 0, len(batches))
	for _, entries := range batches {
		seq, err := w.WriteBatch(entries)
		if err != nil {
			t.Fatalf("WriteBatch: %v", err)
		}
		seqs = append(seqs, seq)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	return buf.Bytes(), seqs
}

func put(key, value string) Entry {
	return Entry{Op: OpPut, Key: []byte(key), Value: []byte(value)}
}

func del(key string) Entry {
	return Entry{Op: OpDelete, Key: []byte(key)}
}

func collectAll(view *View) map[string]string {
	out := make(map[string]string)
	for key, value := range view.All() {
		out[string(key)] = string(value)
	}
	return out
}

func mustGet(t *testing.T, view *View, key string) string {
	t.Helper()
	value, err := view.Get([]byte(key))
	if err != nil {
		t.Fatalf("Get(%q): %v", key, err)
	}
	return string(value)
}

func equalU64(a, b []uint64) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestRoundTripBatchesViewsAndTombstones(t *testing.T) {
	batches := [][]Entry{
		{put("a", "1"), put("b", "2")},
		{put("c", "3"), del("a")},
		{put("a", "10"), put("d", "4")},
		{del("b")},
	}
	data, seqs := buildWAL(t, 64, batches)

	store, err := Recover(context.Background(), bytes.NewReader(data), int64(len(data)), WithBlockSize(64))
	if err != nil {
		t.Fatalf("Recover: %v", err)
	}
	if store.MaxSeq() != 4 {
		t.Fatalf("MaxSeq = %d, want 4", store.MaxSeq())
	}
	if got := store.Sequences(); !equalU64(got, seqs) {
		t.Fatalf("Sequences = %v, want %v", got, seqs)
	}
	if store.Truncated() {
		t.Fatal("clean log must not be reported truncated")
	}

	v1, err := store.View(1)
	if err != nil {
		t.Fatalf("View(1): %v", err)
	}
	if got := collectAll(v1); len(got) != 2 || got["a"] != "1" || got["b"] != "2" {
		t.Fatalf("view@1 = %v", got)
	}

	v2, err := store.ViewAt(2)
	if err != nil {
		t.Fatalf("ViewAt(2): %v", err)
	}
	if v2.Has([]byte("a")) {
		t.Fatal("deleted key a must be hidden at seq 2")
	}
	if _, err := v2.Get([]byte("a")); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Get deleted key: err = %v, want ErrNotFound", err)
	}
	if mustGet(t, v2, "c") != "3" || mustGet(t, v2, "b") != "2" {
		t.Fatalf("view@2 contents wrong: %v", collectAll(v2))
	}

	v3, err := store.ViewAt(3)
	if err != nil {
		t.Fatalf("ViewAt(3): %v", err)
	}
	if mustGet(t, v3, "a") != "10" {
		t.Fatal("delete marker must allow later resurrection")
	}
	if v3.Count() != 4 {
		t.Fatalf("view@3 count = %d, want 4", v3.Count())
	}

	latest := store.Latest()
	if latest.Seq() != 4 {
		t.Fatalf("latest seq = %d", latest.Seq())
	}
	if latest.Has([]byte("b")) {
		t.Fatal("b must stay deleted in latest view")
	}
	if latest.Count() != 3 {
		t.Fatalf("latest count = %d, want 3", latest.Count())
	}

	// Historical views stay immutable after later batches are published.
	if mustGet(t, v1, "a") != "1" {
		t.Fatal("historical view changed after newer batches")
	}

	if _, err := store.View(99); !errors.Is(err, ErrSequenceNotFound) {
		t.Fatalf("View(99): err = %v, want ErrSequenceNotFound", err)
	}
	if _, err := store.ViewAt(0); !errors.Is(err, ErrSequenceNotFound) {
		t.Fatalf("ViewAt(0): err = %v, want ErrSequenceNotFound", err)
	}

	var prev string
	for key := range latest.All() {
		if prev != "" && string(key) <= prev {
			t.Fatalf("All not sorted: %q after %q", key, prev)
		}
		prev = string(key)
	}
}

func TestCrossBlockFragmentsReassemble(t *testing.T) {
	const blockSize = 40
	large := bytes.Repeat([]byte("Z"), 300)
	batches := [][]Entry{
		{put("k1", "small")},
		{put("big", string(large)), put("k2", "tail")},
	}
	data, _ := buildWAL(t, blockSize, batches)

	if len(data) < 3*blockSize {
		t.Fatalf("test setup: log only %d bytes, expected multiple blocks", len(data))
	}

	store, err := Recover(context.Background(), bytes.NewReader(data), int64(len(data)), WithBlockSize(blockSize))
	if err != nil {
		t.Fatalf("Recover: %v", err)
	}
	view := store.Latest()
	if got := mustGet(t, view, "big"); !bytes.Equal([]byte(got), large) {
		t.Fatalf("large value reassembled incorrectly (len %d)", len(got))
	}
	if mustGet(t, view, "k1") != "small" || mustGet(t, view, "k2") != "tail" {
		t.Fatalf("neighbor batches lost: %v", collectAll(view))
	}
}

func manyBatches(n int) [][]Entry {
	batches := make([][]Entry, 0, n)
	for i := 0; i < n; i++ {
		batches = append(batches, []Entry{
			put(fmt.Sprintf("key-%02d", i), fmt.Sprintf("value-%02d", i)),
		})
	}
	return batches
}

func TestMidLogCRCMismatchIsCorruption(t *testing.T) {
	const blockSize = 48
	data, _ := buildWAL(t, blockSize, manyBatches(12))
	if len(data) < 3*blockSize {
		t.Fatalf("test setup: only %d bytes", len(data))
	}

	damaged := append([]byte(nil), data...)
	// Flip a payload byte inside the first block, which is never the tail.
	damaged[fragmentHeaderSize+2] ^= 0xFF

	_, err := Recover(context.Background(), bytes.NewReader(damaged), int64(len(damaged)), WithBlockSize(blockSize))
	if !errors.Is(err, ErrCorruption) {
		t.Fatalf("err = %v, want ErrCorruption", err)
	}
}

// assertPrefix verifies the store only exposes a strict 1..k sequence prefix
// and that every published batch still contains complete data.
func assertPrefix(t *testing.T, store *Store, total int) {
	t.Helper()
	seqs := store.Sequences()
	if len(seqs) == 0 || len(seqs) >= total {
		t.Fatalf("kept %d of %d batches; expected a non-empty strict prefix", len(seqs), total)
	}
	for i, seq := range seqs {
		if seq != uint64(i+1) {
			t.Fatalf("seqs = %v, want strict prefix", seqs)
		}
		view, err := store.View(seq)
		if err != nil {
			t.Fatalf("View(%d): %v", seq, err)
		}
		if view.Count() == 0 {
			t.Fatalf("batch %d published without entries", seq)
		}
	}
}

func TestTornTailTruncatesAndKeepsCommittedPrefix(t *testing.T) {
	const blockSize = 48
	batches := manyBatches(16)
	data, _ := buildWAL(t, blockSize, batches)

	totalBlocks := (len(data) + blockSize - 1) / blockSize
	tailStart := int64(totalBlocks-1) * blockSize
	if totalBlocks < 2 {
		t.Fatal("test setup: expected more than one block")
	}

	t.Run("chopped_tail", func(t *testing.T) {
		chopped := append([]byte(nil), data[:tailStart+10]...)
		store, err := Recover(context.Background(), bytes.NewReader(chopped), int64(len(chopped)), WithBlockSize(blockSize))
		if err != nil {
			t.Fatalf("Recover: %v", err)
		}
		if !store.Truncated() {
			t.Fatal("expected truncated store")
		}
		if !errors.Is(store.TruncationError(), ErrTruncated) {
			t.Fatalf("truncation err = %v, want ErrTruncated", store.TruncationError())
		}
		if store.TruncatedOffset() != tailStart {
			t.Fatalf("truncated at %d, want %d", store.TruncatedOffset(), tailStart)
		}
		assertPrefix(t, store, len(batches))
	})

	t.Run("crc_error_in_tail_block", func(t *testing.T) {
		damaged := append([]byte(nil), data...)
		damaged[tailStart+fragmentHeaderSize+1] ^= 0x01

		store, err := Recover(context.Background(), bytes.NewReader(damaged), int64(len(damaged)), WithBlockSize(blockSize))
		if err != nil {
			t.Fatalf("Recover: %v", err)
		}
		if !store.Truncated() || store.TruncatedOffset() != tailStart {
			t.Fatalf("truncation state wrong: truncated=%v off=%d", store.Truncated(), store.TruncatedOffset())
		}
		assertPrefix(t, store, len(batches))

		// Scanner surface: ErrTruncated once, then io.EOF.
		scanner, err := Scan(bytes.NewReader(damaged), int64(len(damaged)), WithBlockSize(blockSize))
		if err != nil {
			t.Fatalf("Scan: %v", err)
		}
		for {
			_, err := scanner.Next()
			if errors.Is(err, ErrTruncated) {
				break
			}
			if err != nil {
				t.Fatalf("unexpected scanner err: %v", err)
			}
		}
		if _, err := scanner.Next(); !errors.Is(err, io.EOF) {
			t.Fatalf("after truncation: err = %v, want io.EOF", err)
		}
	})
}

type cancelAfterReads struct {
	*bytes.Reader
	reads  int
	after  int
	cancel context.CancelFunc
}

func (c *cancelAfterReads) ReadAt(p []byte, off int64) (int, error) {
	n, err := c.Reader.ReadAt(p, off)
	c.reads++
	if c.reads == c.after {
		c.cancel()
	}
	return n, err
}

func TestCancellationPublishesNoPartialStore(t *testing.T) {
	const blockSize = 32
	data, total := buildWAL(t, blockSize, manyBatches(40))

	// An already-cancelled context must fail without producing a store.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if store, err := Recover(ctx, bytes.NewReader(data), int64(len(data)), WithBlockSize(blockSize)); err == nil || store != nil {
		t.Fatalf("pre-cancelled: store=%v err=%v", store, err)
	}

	// Cancel mid-stream: only an error comes back, never a partial store.
	ctx2, cancel2 := context.WithCancel(context.Background())
	reader := &cancelAfterReads{Reader: bytes.NewReader(data), after: 3, cancel: cancel2}
	store, err := Recover(ctx2, reader, int64(len(data)), WithBlockSize(blockSize))
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if store != nil {
		t.Fatalf("cancelled recovery published a store: %+v", store)
	}
	if len(total) == 0 {
		t.Fatal("test setup produced no batches")
	}
}

func TestRepeatedOpenNeverPublishesPartialBatches(t *testing.T) {
	const blockSize = 48
	batches := manyBatches(16)
	data, _ := buildWAL(t, blockSize, batches)

	tailStart := int64((len(data)+blockSize-1)/blockSize-1) * blockSize
	damaged := append([]byte(nil), data...)
	damaged[tailStart+fragmentHeaderSize+1] ^= 0x01

	dir := t.TempDir()
	path := filepath.Join(dir, "sync.wal")
	if err := os.WriteFile(path, damaged, 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	const goroutines = 8
	stores := make([]*Store, goroutines)
	var wg sync.WaitGroup
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			store, err := Open(context.Background(), path, WithBlockSize(blockSize))
			if err != nil {
				t.Errorf("open %d: %v", idx, err)
				return
			}
			stores[idx] = store
		}(i)
	}
	wg.Wait()

	for i, store := range stores {
		if store == nil {
			t.Fatalf("store %d missing", i)
		}
		if !store.Truncated() {
			t.Fatalf("store %d should report torn tail", i)
		}
		assertPrefix(t, store, len(batches))
	}

	// Reopening a second time yields the same committed prefix.
	again, err := Open(context.Background(), path, WithBlockSize(blockSize))
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	if !equalU64(again.Sequences(), stores[0].Sequences()) {
		t.Fatal("reopen published a different sequence prefix")
	}
}

func TestEmptyLog(t *testing.T) {
	store, err := Recover(context.Background(), bytes.NewReader(nil), 0)
	if err != nil {
		t.Fatalf("Recover empty: %v", err)
	}
	if store.MaxSeq() != 0 || store.Truncated() {
		t.Fatalf("empty store unexpected state: %+v", store)
	}
	if _, err := store.ViewAt(1); !errors.Is(err, ErrSequenceNotFound) {
		t.Fatalf("empty ViewAt: err = %v", err)
	}

	scanner, err := Scan(bytes.NewReader(nil), 0)
	if err != nil {
		t.Fatalf("Scan empty: %v", err)
	}
	defer scanner.Close()
	if _, err := scanner.Next(); !errors.Is(err, io.EOF) {
		t.Fatalf("empty scanner: err = %v, want io.EOF", err)
	}
}

func TestReturnedSlicesAreImmutableCopies(t *testing.T) {
	data, _ := buildWAL(t, 64, [][]Entry{
		{put("k", "original")},
		{put("k", "second")},
	})
	store, err := Recover(context.Background(), bytes.NewReader(data), int64(len(data)), WithBlockSize(64))
	if err != nil {
		t.Fatalf("Recover: %v", err)
	}

	view1, err := store.View(1)
	if err != nil {
		t.Fatalf("View: %v", err)
	}
	value, err := view1.Get([]byte("k"))
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	value[0] = 'X'

	if got := mustGet(t, view1, "k"); got != "original" {
		t.Fatalf("store value mutated through returned slice: %q", got)
	}

	seqs := store.Sequences()
	seqs[0] = 999
	if store.Sequences()[0] != 1 {
		t.Fatal("Sequences returned a mutable backing array")
	}
}

func TestSequenceGapInNonTailBlockIsCorruption(t *testing.T) {
	const blockSize = 48
	data, _ := buildWAL(t, blockSize, manyBatches(12))

	// Re-encode the second batch's record with an out-of-order sequence number.
	// Its CRC stays valid, but strict batch sequencing must reject it.
	scanner, err := Scan(bytes.NewReader(data), int64(len(data)), WithBlockSize(blockSize))
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	first, err := scanner.Next()
	if err != nil {
		t.Fatalf("first batch: %v", err)
	}
	second, err := scanner.Next()
	if err != nil {
		t.Fatalf("second batch: %v", err)
	}

	var buf bytes.Buffer
	w, err := NewWriter(&buf, WithBlockSize(blockSize))
	if err != nil {
		t.Fatalf("NewWriter: %v", err)
	}
	if _, err := w.WriteBatch(first.Entries); err != nil {
		t.Fatalf("write first: %v", err)
	}
	badRecord := encodeBatch(second.Seq+5, second.Entries)
	if err := w.writeRecord(badRecord); err != nil {
		t.Fatalf("write bad record: %v", err)
	}

	// Pad out so the bad record sits in a non-tail block.
	if err := w.Sync(); err != nil {
		t.Fatalf("sync: %v", err)
	}
	if _, err := w.WriteBatch(manyBatches(12)[0]); err != nil {
		t.Fatalf("write tail batch: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	_, err = Recover(context.Background(), bytes.NewReader(buf.Bytes()), int64(buf.Len()), WithBlockSize(blockSize))
	if !errors.Is(err, ErrCorruption) {
		t.Fatalf("err = %v, want ErrCorruption", err)
	}
}

func TestMidLogLengthErrorIsCorruption(t *testing.T) {
	const blockSize = 48
	data, _ := buildWAL(t, blockSize, manyBatches(12))

	damaged := append([]byte(nil), data...)
	// Impossible declared length in the first, non-tail block.
	damaged[4] = 0xFF
	damaged[5] = 0xFF

	_, err := Recover(context.Background(), bytes.NewReader(damaged), int64(len(damaged)), WithBlockSize(blockSize))
	if !errors.Is(err, ErrCorruption) {
		t.Fatalf("err = %v, want ErrCorruption", err)
	}
}
