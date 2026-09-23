package sparseidx

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"
	"math/rand"
	"os"
	"path/filepath"
	"testing"
)

func openTest(t *testing.T, dir string, opts *Options) *Store {
	t.Helper()
	st, err := Open(dir, opts)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	return st
}

func mustFlush(t *testing.T, st *Store) {
	t.Helper()
	if err := st.Flush(); err != nil {
		t.Fatalf("Flush: %v", err)
	}
}

func recordsEqual(a, b Record) bool {
	return a.Flags == b.Flags && bytes.Equal(a.Key, b.Key) && bytes.Equal(a.Value, b.Value)
}

// 随机第 k 条定位对照全解码，且每次定位解码条数不超过跨度。
func TestKthMatchesFullDecodeAndBoundedSpan(t *testing.T) {
	dir := t.TempDir()
	const stride = 8
	st := openTest(t, dir, &Options{Stride: stride})
	rng := rand.New(rand.NewSource(42))
	total := 0
	for total < 200 {
		batch := 1 + rng.Intn(13) // irregular block sizes
		for i := 0; i < batch; i++ {
			key := fmt.Sprintf("key-%04d", total)
			val := make([]byte, rng.Intn(50))
			rng.Read(val)
			if err := st.Put([]byte(key), val); err != nil {
				t.Fatal(err)
			}
			total++
		}
		mustFlush(t, st)
	}
	if st.NumRecords() != uint64(total) {
		t.Fatalf("NumRecords=%d want %d", st.NumRecords(), total)
	}
	for trial := 0; trial < 500; trial++ {
		k := uint64(rng.Intn(total))
		got, err := st.Kth(k)
		if err != nil {
			t.Fatalf("Kth(%d): %v", k, err)
		}
		want, err := st.KthSlow(k)
		if err != nil {
			t.Fatalf("KthSlow(%d): %v", k, err)
		}
		if !recordsEqual(got, want) {
			t.Fatalf("Kth(%d) mismatch vs full decode", k)
		}
		if d := st.LastSeekDecoded(); d > stride {
			t.Fatalf("Kth(%d) decoded %d records, span bound is %d", k, d, stride)
		}
	}
}

// 跨块第 k 条定位：索引项在块尾附近，目标记录在下一块。
func TestCrossBlockKth(t *testing.T) {
	dir := t.TempDir()
	st := openTest(t, dir, &Options{Stride: 4})
	// 3 blocks x 3 records; entries at global 0, 4, 8.
	for b := 0; b < 3; b++ {
		for r := 0; r < 3; r++ {
			if err := st.Put([]byte(fmt.Sprintf("b%dr%d", b, r)), []byte{byte(b), byte(r)}); err != nil {
				t.Fatal(err)
			}
		}
		mustFlush(t, st)
	}
	// k=6: entry at global 4 (block 1), decode 4,5 then cross into block 2.
	rec, err := st.Kth(6)
	if err != nil {
		t.Fatal(err)
	}
	if string(rec.Key) != "b2r0" {
		t.Fatalf("Kth(6) key=%q want b2r0", rec.Key)
	}
	if d := st.LastSeekDecoded(); d != 3 {
		t.Fatalf("Kth(6) decoded %d records, want 3 (<= stride 4)", d)
	}
	for k := uint64(0); k < 9; k++ {
		got, err := st.Kth(k)
		if err != nil {
			t.Fatalf("Kth(%d): %v", k, err)
		}
		want, _ := st.KthSlow(k)
		if !recordsEqual(got, want) {
			t.Fatalf("Kth(%d) mismatch", k)
		}
	}
}

// 索引指向非记录边界必须报损坏，而不是盲信偏移。
func TestIndexNonBoundaryIsCorruption(t *testing.T) {
	dir := t.TempDir()
	st := openTest(t, dir, &Options{Stride: 2})
	for i := 0; i < 10; i++ {
		if err := st.Put([]byte(fmt.Sprintf("k%d", i)), []byte(fmt.Sprintf("v%d", i))); err != nil {
			t.Fatal(err)
		}
	}
	mustFlush(t, st)

	// Rewrite the index file with entry[1].offset shifted by +1 (mid-record),
	// keeping the file-level checksum valid so it loads cleanly.
	data, err := os.ReadFile(filepath.Join(dir, "index.idx"))
	if err != nil {
		t.Fatal(err)
	}
	idx, err := parseIndex(data)
	if err != nil {
		t.Fatal(err)
	}
	if len(idx.entries) < 2 {
		t.Fatal("need at least 2 index entries")
	}
	idx.entries[1].offset++
	target := idx.entries[1].recordIndex
	if err := os.WriteFile(filepath.Join(dir, "index.idx"), idx.marshal(), 0o644); err != nil {
		t.Fatal(err)
	}

	st2 := openTest(t, dir, &Options{Stride: 2})
	if _, err := st2.Kth(target); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("Kth(%d) err=%v, want ErrCorrupt", target, err)
	}
}

// 空记录不与缺记录混淆。
func TestEmptyRecordVsMissing(t *testing.T) {
	dir := t.TempDir()
	st := openTest(t, dir, nil)
	if err := st.Put([]byte(""), []byte("")); err != nil { // empty key AND value
		t.Fatal(err)
	}
	if err := st.Put([]byte("k"), []byte{}); err != nil { // empty value
		t.Fatal(err)
	}
	mustFlush(t, st)

	rec, err := st.Kth(0)
	if err != nil {
		t.Fatalf("Kth(0): %v", err)
	}
	if len(rec.Key) != 0 || len(rec.Value) != 0 {
		t.Fatalf("Kth(0) = %q/%q, want empty/empty", rec.Key, rec.Value)
	}
	if _, err := st.Kth(st.NumRecords()); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Kth(total) err=%v, want ErrNotFound", err)
	}
	v, found := st.Get([]byte(""))
	if !found || len(v) != 0 {
		t.Fatalf("Get(empty) = %q,%v want empty,true", v, found)
	}
	if _, found := st.Get([]byte("never-written")); found {
		t.Fatal("Get(never-written) found=true, want false")
	}
}

// 墓碑与局部替换语义；旧快照仍看到旧版本。
func TestTombstoneAndPatch(t *testing.T) {
	dir := t.TempDir()
	st := openTest(t, dir, nil)
	if err := st.Put([]byte("k"), []byte("hello world")); err != nil {
		t.Fatal(err)
	}
	mustFlush(t, st)
	if err := st.Patch([]byte("k"), 6, []byte("Gophers")); err != nil {
		t.Fatal(err)
	}
	mustFlush(t, st)
	v, found := st.Get([]byte("k"))
	if !found || string(v) != "hello Gophers" {
		t.Fatalf("after patch Get(k)=%q,%v want hello Gophers,true", v, found)
	}

	snap := st.Snapshot()
	defer snap.Close()
	if err := st.Delete([]byte("k")); err != nil {
		t.Fatal(err)
	}
	mustFlush(t, st)
	if _, found := st.Get([]byte("k")); found {
		t.Fatal("Get(k) after tombstone found=true, want false")
	}
	sv, sfound, err := snap.Get([]byte("k"))
	if err != nil || !sfound || string(sv) != "hello Gophers" {
		t.Fatalf("snapshot Get(k)=%q,%v,%v want hello Gophers,true,nil", sv, sfound, err)
	}
}

// 崩溃恢复：块尾半条记录被丢弃，绝不被接受。
func TestRecoveryDropsHalfRecord(t *testing.T) {
	dir := t.TempDir()
	st := openTest(t, dir, nil)
	for i := 0; i < 3; i++ {
		if err := st.Put([]byte(fmt.Sprintf("k%d", i)), []byte(fmt.Sprintf("v%d", i))); err != nil {
			t.Fatal(err)
		}
	}
	mustFlush(t, st)

	// Simulate a torn write: append half of a 4th record after the footer,
	// then remove manifest+index so recovery rescans the block directory.
	path := blockPath(dir, 1)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	half := appendRecord(nil, Record{Flags: FlagPut, Key: []byte("k3"), Value: []byte("v3")})
	data = append(data, half[:len(half)/2]...)
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	os.Remove(filepath.Join(dir, "MANIFEST"))
	os.Remove(filepath.Join(dir, "index.idx"))

	st2 := openTest(t, dir, nil)
	if st2.RecoveredBlocks != 1 {
		t.Fatalf("RecoveredBlocks=%d want 1", st2.RecoveredBlocks)
	}
	if st2.NumRecords() != 3 {
		t.Fatalf("NumRecords=%d want 3: half record must not be accepted", st2.NumRecords())
	}
	if _, err := st2.Kth(3); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Kth(3) err=%v, want ErrNotFound", err)
	}
}

// 索引丢失或损坏时可在恢复中重建。
func TestIndexRebuiltOnRecovery(t *testing.T) {
	dir := t.TempDir()
	st := openTest(t, dir, &Options{Stride: 2})
	for i := 0; i < 20; i++ {
		if err := st.Put([]byte(fmt.Sprintf("k%02d", i)), []byte{byte(i)}); err != nil {
			t.Fatal(err)
		}
	}
	mustFlush(t, st)

	os.Remove(filepath.Join(dir, "index.idx"))
	st2 := openTest(t, dir, &Options{Stride: 2})
	for k := uint64(0); k < 20; k++ {
		got, err := st2.Kth(k)
		if err != nil {
			t.Fatalf("after rebuild Kth(%d): %v", k, err)
		}
		if string(got.Key) != fmt.Sprintf("k%02d", k) {
			t.Fatalf("after rebuild Kth(%d)=%q", k, got.Key)
		}
	}

	// Structurally corrupt index (bad checksum) must also trigger a rebuild.
	if err := os.WriteFile(filepath.Join(dir, "index.idx"), []byte("garbage"), 0o644); err != nil {
		t.Fatal(err)
	}
	st3 := openTest(t, dir, &Options{Stride: 2})
	rec, err := st3.Kth(7)
	if err != nil || string(rec.Key) != "k07" {
		t.Fatalf("after corrupt index Kth(7)=%q,%v", rec.Key, err)
	}
}

// 原子发布：已写盘但未提交进 manifest 的块在恢复时被忽略并可回收。
func TestAtomicPublishOrphanIgnored(t *testing.T) {
	dir := t.TempDir()
	st := openTest(t, dir, nil)
	if err := st.Put([]byte("a"), []byte("1")); err != nil {
		t.Fatal(err)
	}
	mustFlush(t, st)

	// Crash after writing block+tmp but before committing the manifest.
	if _, err := writeBlockFile(dir, 99, []Record{{Flags: FlagPut, Key: []byte("orphan"), Value: []byte("x")}}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "blocks", "stray.tmp"), []byte("junk"), 0o644); err != nil {
		t.Fatal(err)
	}

	st2 := openTest(t, dir, nil)
	if st2.NumRecords() != 1 {
		t.Fatalf("NumRecords=%d want 1: orphan block must be ignored", st2.NumRecords())
	}
	if _, found := st2.Get([]byte("orphan")); found {
		t.Fatal("orphan record visible after recovery")
	}
	if err := st2.CollectGarbage(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(blockPath(dir, 99)); !os.IsNotExist(err) {
		t.Fatal("orphan block not reclaimed")
	}
	if _, err := os.Stat(filepath.Join(dir, "blocks", "stray.tmp")); !os.IsNotExist(err) {
		t.Fatal("stray tmp not reclaimed")
	}
}

// 旧快照只访问自己钉住的块，绝不访问已回收块。
func TestSnapshotNeverTouchesReclaimedBlocks(t *testing.T) {
	dir := t.TempDir()
	var mu []uint64
	hook := func(id uint64) { mu = append(mu, id) }
	st := openTest(t, dir, &Options{AccessHook: hook})
	if err := st.Put([]byte("a"), []byte("v1")); err != nil {
		t.Fatal(err)
	}
	mustFlush(t, st) // block 1
	if err := st.Put([]byte("b"), []byte("v2")); err != nil {
		t.Fatal(err)
	}
	mustFlush(t, st) // block 2

	snap := st.Snapshot()                // pins blocks 1,2
	if err := st.Compact(); err != nil { // block 3 replaces 1,2 in manifest
		t.Fatal(err)
	}
	if err := st.CollectGarbage(); err != nil {
		t.Fatal(err)
	}
	// Pinned blocks must survive GC.
	for _, id := range []uint64{1, 2} {
		if _, err := os.Stat(blockPath(dir, id)); err != nil {
			t.Fatalf("pinned block %d reclaimed while snapshot open", id)
		}
	}

	mu = nil
	v, found, err := snap.Get([]byte("a"))
	if err != nil || !found || string(v) != "v1" {
		t.Fatalf("snapshot Get(a)=%q,%v,%v", v, found, err)
	}
	for _, id := range mu {
		if id != 1 && id != 2 {
			t.Fatalf("snapshot accessed non-pinned block %d", id)
		}
	}

	snap.Close()
	if err := st.CollectGarbage(); err != nil {
		t.Fatal(err)
	}
	for _, id := range []uint64{1, 2} {
		if _, err := os.Stat(blockPath(dir, id)); !os.IsNotExist(err) {
			t.Fatalf("block %d not reclaimed after snapshot close", id)
		}
	}

	// After reclaim, live reads must touch only the compacted block 3.
	mu = nil
	if _, err := st.Kth(0); err != nil {
		t.Fatal(err)
	}
	if _, found := st.Get([]byte("b")); !found {
		t.Fatal("Get(b) lost after compact")
	}
	for _, id := range mu {
		if id == 1 || id == 2 {
			t.Fatalf("read touched reclaimed block %d", id)
		}
	}
	if len(mu) == 0 {
		t.Fatal("expected Kth to access the compacted block")
	}
}

// 解码输出额度有界：超限记录在写入与解码两侧都被拒绝。
func TestBoundedDecodeBudget(t *testing.T) {
	dir := t.TempDir()
	st := openTest(t, dir, &Options{MaxRecordBytes: 64})
	if err := st.Put([]byte("k"), make([]byte, 100)); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("Put oversized err=%v, want ErrTooLarge", err)
	}

	// A hostile header claiming a 1 GiB value must be rejected by the budget
	// check before any allocation happens.
	var hdr [recordHeaderSize]byte
	binary.BigEndian.PutUint32(hdr[7:], 1<<30)
	binary.BigEndian.PutUint32(hdr[0:], crc32.ChecksumIEEE(hdr[4:]))
	if _, _, err := decodeRecordAt(hdr[:], 0, 1<<20); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("decodeRecordAt err=%v, want ErrTooLarge", err)
	}
	// And a truncated giant must report truncation, not allocate.
	if _, _, err := decodeRecordAt(hdr[:], 0, 1<<31); !errors.Is(err, ErrTruncated) {
		t.Fatalf("decodeRecordAt err=%v, want ErrTruncated", err)
	}
}
