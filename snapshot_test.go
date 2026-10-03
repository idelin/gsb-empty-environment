package bitmap

import (
	"bytes"
	"io/ioutil"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

var tempDirs []string

func tempDir(t *testing.T) string {
	t.Helper()
	dir, err := ioutil.TempDir("", "bitmap-test-")
	if err != nil {
		t.Fatal(err)
	}
	tempDirs = append(tempDirs, dir)
	return dir
}

func TestMain(m *testing.M) {
	code := m.Run()
	for _, d := range tempDirs {
		os.RemoveAll(d)
	}
	os.Exit(code)
}

// ---------- 快照隔离与 COW ----------

func TestSnapshotIsolation(t *testing.T) {
	bm := New()
	bm.AddRange(0, 100000)
	for i := 0; i < 3000; i++ {
		bm.Add(uint32(i * 7))
	}
	snap := bm.Snapshot()
	cardBefore := snap.Cardinality()

	// 快照后大量变更：新增、删除、新增容器、删除容器。
	bm.AddRange(200000, 500000)
	for i := 0; i < 3000; i++ {
		bm.Remove(uint32(i * 7))
	}
	for i := uint32(0); i < 100000; i++ {
		bm.Remove(i)
	}

	if snap.Cardinality() != cardBefore {
		t.Fatalf("snapshot cardinality changed: %d -> %d", cardBefore, snap.Cardinality())
	}
	for _, v := range []uint32{0, 7, 14000, 99999} {
		if !snap.Contains(v) {
			t.Fatalf("snapshot lost %d", v)
		}
	}
	if snap.Contains(300000) {
		t.Fatal("snapshot sees post-snapshot write")
	}
	snap.Release()
}

func TestSnapshotReleaseRestoresInPlaceWrites(t *testing.T) {
	bm := New()
	bm.AddRange(0, 10000)
	s1 := bm.Snapshot()
	s2 := bm.Snapshot()
	if len(bm.snaps) != 2 || bm.maxSnap == 0 {
		t.Fatal("snapshots not tracked")
	}
	s1.Release()
	if len(bm.snaps) != 1 || bm.maxSnap == 0 {
		t.Fatal("one snapshot should remain")
	}
	s2.Release()
	s2.Release() // 幂等
	if len(bm.snaps) != 0 || bm.maxSnap != 0 {
		t.Fatalf("COW state leaked: snaps=%d maxSnap=%d", len(bm.snaps), bm.maxSnap)
	}
}

func TestSnapshotIteratorConsistent(t *testing.T) {
	bm := New()
	bm.AddRange(0, 65536)
	snap := bm.Snapshot()
	defer snap.Release()
	bm.AddRange(65536, 1<<20) // 快照后继续写
	n := 0
	it := snap.Iterator()
	for _, ok := it.Next(); ok; _, ok = it.Next() {
		n++
	}
	if n != 65536 {
		t.Fatalf("snapshot iterator yielded %d, want 65536", n)
	}
}

// ---------- 序列化往返 ----------

func TestSaveLoadRoundTrip(t *testing.T) {
	bm := New()
	bm.AddRange(0, 100000)
	for i := 0; i < 2000; i++ {
		bm.Add(uint32(1<<20 + i*13))
	}
	var buf bytes.Buffer
	if err := bm.Save(&buf); err != nil {
		t.Fatal(err)
	}
	back, err := ReadBitmap(&buf)
	if err != nil {
		t.Fatal(err)
	}
	if d := Xor(bm, back); d.Cardinality() != 0 {
		t.Fatalf("round trip differs by %d", d.Cardinality())
	}
}

func TestReadBitmapRejectsGarbage(t *testing.T) {
	if _, err := ReadBitmap(bytes.NewReader([]byte("garbage-data"))); err == nil {
		t.Fatal("expect error on garbage")
	}
}

// ---------- 检查点与增量归档 ----------

func buildTestBitmap() *Bitmap {
	bm := New()
	bm.AddRange(0, 50000)       // RLE
	for i := 0; i < 6000; i++ { // 密集 -> bitmap 容器
		bm.Add(uint32(10<<16 + i))
	}
	for i := 0; i < 100; i++ { // 稀疏 -> array 容器
		bm.Add(uint32(20<<16 + i*100))
	}
	return bm
}

func TestCheckpointRestoreRoundTrip(t *testing.T) {
	dir := tempDir(t)
	bm := buildTestBitmap()
	cp, err := NewCheckpointer(bm, dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := cp.Checkpoint(); err != nil {
		t.Fatal(err)
	}
	restored, err := Restore(dir)
	if err != nil {
		t.Fatal(err)
	}
	if d := Xor(bm, restored); d.Cardinality() != 0 {
		t.Fatalf("restore differs by %d", d.Cardinality())
	}
	// 恢复后的位图与原位图互不影响（无共享状态泄漏）。
	restored.Add(1 << 30)
	if bm.Contains(1 << 30) {
		t.Fatal("restored bitmap shares state with original")
	}
}

func TestIncrementalChain(t *testing.T) {
	dir := tempDir(t)
	bm := buildTestBitmap()
	cp, _ := NewCheckpointer(bm, dir)
	if _, err := cp.Checkpoint(); err != nil {
		t.Fatal(err)
	}

	// 变更 1：新增容器 + 修改既有容器。
	bm.AddRange(30<<16, 30<<16+90000)
	bm.Add(uint32(20<<16 + 5))
	p1, err := cp.Incremental()
	if err != nil || p1 == "" {
		t.Fatalf("incremental 1: %v %q", err, p1)
	}

	// 变更 2：删除整个容器（触发墓碑）+ 大范围删除。
	for i := 0; i < 100; i++ {
		bm.Remove(uint32(20<<16 + i*100))
	}
	bm.Remove(uint32(20<<16 + 5))
	bm.Add(0xFFFFFFFF)
	p2, err := cp.Incremental()
	if err != nil || p2 == "" {
		t.Fatalf("incremental 2: %v %q", err, p2)
	}

	restored, err := Restore(dir)
	if err != nil {
		t.Fatal(err)
	}
	if d := Xor(bm, restored); d.Cardinality() != 0 {
		t.Fatalf("incremental restore differs by %d", d.Cardinality())
	}
	if !restored.Contains(0xFFFFFFFF) {
		t.Fatal("max uint32 lost")
	}
	if restored.Contains(uint32(20 << 16)) {
		t.Fatal("tombstoned container resurrected")
	}
}

func TestIncrementalNoChanges(t *testing.T) {
	dir := tempDir(t)
	bm := buildTestBitmap()
	cp, _ := NewCheckpointer(bm, dir)
	if _, err := cp.Checkpoint(); err != nil {
		t.Fatal(err)
	}
	p, err := cp.Incremental()
	if err != nil {
		t.Fatal(err)
	}
	if p != "" {
		t.Fatalf("expect no file, got %q", p)
	}
	restored, err := Restore(dir)
	if err != nil {
		t.Fatal(err)
	}
	if d := Xor(bm, restored); d.Cardinality() != 0 {
		t.Fatal("restore mismatch")
	}
}

func TestIncrementalOnlyWritesChangedContainers(t *testing.T) {
	dir := tempDir(t)
	bm := New()
	bm.AddRange(0, 100000) // 2 个容器
	cp, _ := NewCheckpointer(bm, dir)
	if _, err := cp.Checkpoint(); err != nil {
		t.Fatal(err)
	}
	bm.Add(1 << 20) // 新增 1 个容器
	p, err := cp.Incremental()
	if err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	// 增量文件只含 1 个小容器，必须远小于全量。
	if fi.Size() > 256 {
		t.Fatalf("incremental too large: %d bytes", fi.Size())
	}
	chk, _ := filepath.Glob(filepath.Join(dir, "chk-*.bin"))
	ci, err := os.Stat(chk[0])
	if err != nil {
		t.Fatal(err)
	}
	if fi.Size() >= ci.Size() {
		t.Fatalf("incremental (%d) should be smaller than checkpoint (%d)", fi.Size(), ci.Size())
	}
}

func TestRestorePicksLatestCheckpoint(t *testing.T) {
	dir := tempDir(t)
	bm := New()
	cp, _ := NewCheckpointer(bm, dir)
	bm.Add(1)
	if _, err := cp.Checkpoint(); err != nil {
		t.Fatal(err)
	}
	bm.Add(2)
	if _, err := cp.Checkpoint(); err != nil {
		t.Fatal(err)
	}
	bm.Add(3)
	if _, err := cp.Incremental(); err != nil {
		t.Fatal(err)
	}
	restored, err := Restore(dir)
	if err != nil {
		t.Fatal(err)
	}
	if restored.Cardinality() != 3 {
		t.Fatalf("cardinality = %d, want 3", restored.Cardinality())
	}
}

func TestRestoreMissingCheckpoint(t *testing.T) {
	dir := tempDir(t)
	if _, err := Restore(dir); err == nil {
		t.Fatal("expect error on empty dir")
	}
	if _, err := Restore(filepath.Join(dir, "nonexistent")); err == nil {
		t.Fatal("expect error on missing dir")
	}
}

func TestRestoreCorruptFile(t *testing.T) {
	dir := tempDir(t)
	if err := ioutil.WriteFile(filepath.Join(dir, "chk-00000000000000000001.bin"), []byte("corrupt"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := Restore(dir); err == nil {
		t.Fatal("expect error on corrupt checkpoint")
	}
}

func TestIncrementalBaseMismatch(t *testing.T) {
	dir := tempDir(t)
	bm := New()
	bm.Add(1)
	cp, _ := NewCheckpointer(bm, dir)
	if _, err := cp.Checkpoint(); err != nil {
		t.Fatal(err)
	}
	// 手工构造 base 不匹配的增量文件。
	bm.Add(2)
	p, err := cp.Incremental()
	if err != nil {
		t.Fatal(err)
	}
	data, err := ioutil.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	// base 字段位于 magic(4)+version(1)+type(1)+gen(8) = 偏移 14。
	for i := 14; i < 22; i++ {
		data[i] = 0xFF
	}
	if err := ioutil.WriteFile(p, data, 0644); err != nil {
		t.Fatal(err)
	}
	// base 被篡改后与链上代际不匹配，恢复必须报错而非静默应用。
	if _, err := Restore(dir); err == nil {
		t.Fatal("expect error on base mismatch")
	}
}

func TestCheckpointAtomicNoTempLeft(t *testing.T) {
	dir := tempDir(t)
	bm := buildTestBitmap()
	cp, _ := NewCheckpointer(bm, dir)
	if _, err := cp.Checkpoint(); err != nil {
		t.Fatal(err)
	}
	if _, err := cp.Incremental(); err != nil {
		t.Fatal(err)
	}
	entries, err := ioutil.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if filepath.Ext(e.Name()) == ".tmp" {
			t.Fatalf("temp file left behind: %s", e.Name())
		}
	}
}

// ---------- 无泄漏断言 ----------

func TestNoLeaks(t *testing.T) {
	before := runtime.NumGoroutine()
	for round := 0; round < 50; round++ {
		bm := New()
		bm.AddRange(0, 10000)
		snaps := make([]*Snapshot, 0, 8)
		for i := 0; i < 8; i++ {
			snaps = append(snaps, bm.Snapshot())
			bm.Add(uint32(1<<20 + i))
		}
		for _, s := range snaps {
			s.Release()
		}
		if len(bm.snaps) != 0 || bm.maxSnap != 0 {
			t.Fatalf("round %d: snapshot bookkeeping leaked", round)
		}
	}
	runtime.GC()
	time.Sleep(20 * time.Millisecond)
	if after := runtime.NumGoroutine(); after > before+1 {
		t.Fatalf("goroutine leak: before=%d after=%d", before, after)
	}
}

// 快照释放后，原位图不再为写入付出 COW 克隆（内部状态断言）。
func TestCOWDisabledAfterRelease(t *testing.T) {
	bm := New()
	bm.AddRange(0, 10000)
	s := bm.Snapshot()
	bm.Add(1 << 20) // 触发一次 COW
	s.Release()
	if bm.maxSnap != 0 {
		t.Fatal("COW still armed after release")
	}
	// 直接验证：就地修改路径不再克隆（gens 全部晚于 maxSnap=0）。
	bm.Add(2 << 20)
	for i, g := range bm.gens {
		if g == 0 {
			t.Fatalf("container %d has zero gen", i)
		}
	}
}
