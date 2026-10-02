package backup

import (
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"snapshotlsm/lsm"
)

func tempDir(t *testing.T) string {
	dir, err := ioutil.TempDir("", "backup-test-")
	if err != nil {
		t.Fatal(err)
	}
	return dir
}

// writeBatch 写入 n 条键值，key 使用 keyPrefix、value 使用 valPrefix，
// 允许用相同 keyPrefix 不同 valPrefix 实现覆盖写。
func writeBatch(t *testing.T, db *lsm.DB, keyPrefix, valPrefix string, start, n int) {
	t.Helper()
	for i := start; i < start+n; i++ {
		key := []byte(fmt.Sprintf("%s-key-%05d", keyPrefix, i))
		val := []byte(fmt.Sprintf("%s-value-%05d-%s", valPrefix, i, strings.Repeat("d", 32)))
		if err := db.Put(key, val); err != nil {
			t.Fatal(err)
		}
	}
}

func readAll(t *testing.T, path string) []lsm.Entry {
	t.Helper()
	entries, err := lsm.ReadSSTable(path)
	if err != nil {
		t.Fatal(err)
	}
	return entries
}

// TestSnapshotSurvivesCompaction 是核心验收测试：
// 快照导出全部 SSTable 硬链接后，前台继续密集写入并触发 Compaction
// 删除全部旧文件，快照目录的数据必须完好无损。
func TestSnapshotSurvivesCompaction(t *testing.T) {
	dbDir := tempDir(t)
	snapRoot := tempDir(t)
	defer os.RemoveAll(dbDir)
	defer os.RemoveAll(snapRoot)

	db, err := lsm.Open(dbDir, lsm.Options{FlushThresholdBytes: 4096, CompactThreshold: 1 << 30})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	// 第一批写入：产生多个 SSTable。
	writeBatch(t, db, "batch1", "batch1", 0, 2000)
	sstCount, _ := db.Stats()
	if sstCount < 2 {
		t.Fatalf("expected multiple sstables, got %d", sstCount)
	}

	// 生成快照。
	sn, err := NewSnapshotter(db, snapRoot)
	if err != nil {
		t.Fatal(err)
	}
	m, err := sn.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	snapDir := sn.SnapshotDir(m.Seq)
	if len(m.Files) != sstCount {
		t.Fatalf("manifest has %d files, db has %d sstables", len(m.Files), sstCount)
	}

	// 验收点 1：快照目录包含当时全部 SSTable 的硬链接（同 inode，零拷贝）。
	for _, fe := range m.Files {
		same, err := SameFile(filepath.Join(dbDir, fe.Name), filepath.Join(snapDir, fe.Name))
		if err != nil {
			t.Fatal(err)
		}
		if !same {
			t.Fatalf("%s is not a hard link of the source sstable", fe.Name)
		}
	}

	// 记录快照时刻每个文件的内容，供事后比对。
	wantEntries := make(map[string][]lsm.Entry)
	for _, fe := range m.Files {
		wantEntries[fe.Name] = readAll(t, filepath.Join(snapDir, fe.Name))
	}

	// 前台继续密集写入（含对第一批 key 的覆盖写），然后强制 Compaction。
	writeBatch(t, db, "batch1", "batch3", 0, 2000) // 覆盖 batch1 的全部 key
	writeBatch(t, db, "batch2", "batch2", 0, 2000) // 全新 key
	if err := db.Compact(); err != nil {
		t.Fatal(err)
	}
	_, compacted := db.Stats()
	if compacted == 0 {
		t.Fatal("expected compaction to delete old sstables")
	}

	// 验收点 2：Compaction 已删除快照所对应的全部源文件。
	for _, fe := range m.Files {
		if _, err := os.Stat(filepath.Join(dbDir, fe.Name)); !os.IsNotExist(err) {
			t.Fatalf("source file %s should have been deleted by compaction", fe.Name)
		}
	}

	// 验收点 3：快照目录不受任何影响——清单校验通过。
	if err := Verify(snapDir); err != nil {
		t.Fatalf("snapshot verify failed after compaction: %v", err)
	}

	// 验收点 4：快照数据逐字节完整，且保留的是快照时刻的旧值。
	for _, fe := range m.Files {
		got := readAll(t, filepath.Join(snapDir, fe.Name))
		want := wantEntries[fe.Name]
		if len(got) != len(want) {
			t.Fatalf("%s: got %d entries, want %d", fe.Name, len(got), len(want))
		}
		for i := range want {
			if string(got[i].Key) != string(want[i].Key) || string(got[i].Value) != string(want[i].Value) {
				t.Fatalf("%s entry %d corrupted", fe.Name, i)
			}
		}
	}

	// 被覆盖的 key：快照中是 batch1 旧值，DB 中是 batch3 新值。
	var snapVal string
	for _, fe := range m.Files {
		for _, e := range wantEntries[fe.Name] {
			if string(e.Key) == "batch1-key-00000" {
				snapVal = string(e.Value)
			}
		}
	}
	if !strings.HasPrefix(snapVal, "batch1-value-") {
		t.Fatalf("snapshot should keep old batch1 value, got %q", snapVal)
	}
	dbVal, ok, err := db.Get([]byte("batch1-key-00000"))
	if err != nil || !ok {
		t.Fatalf("db get: ok=%v err=%v", ok, err)
	}
	if !strings.HasPrefix(string(dbVal), "batch3-value-") {
		t.Fatalf("db should have latest batch3 value, got %q", dbVal)
	}
}

// TestIncrementalSnapshots 验证增量清单：每次快照的 DELTA.json
// 只包含相对上一快照新增的 SSTable，支持差异化归档。
func TestIncrementalSnapshots(t *testing.T) {
	dbDir := tempDir(t)
	snapRoot := tempDir(t)
	defer os.RemoveAll(dbDir)
	defer os.RemoveAll(snapRoot)

	db, err := lsm.Open(dbDir, lsm.Options{FlushThresholdBytes: 2048, CompactThreshold: 1 << 30})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	sn, err := NewSnapshotter(db, snapRoot)
	if err != nil {
		t.Fatal(err)
	}

	// 快照 1：全量，delta == 全部文件。
	writeBatch(t, db, "a", "a", 0, 500)
	m1, err := sn.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	d1, err := LoadManifest(filepath.Join(sn.SnapshotDir(m1.Seq), DeltaFile))
	if err != nil {
		t.Fatal(err)
	}
	if len(d1.Files) != len(m1.Files) || len(m1.Files) == 0 {
		t.Fatalf("first delta should equal full set: delta=%d full=%d", len(d1.Files), len(m1.Files))
	}

	// 快照 2：delta 只含新增 SSTable。
	writeBatch(t, db, "b", "b", 0, 500)
	m2, err := sn.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	d2, err := LoadManifest(filepath.Join(sn.SnapshotDir(m2.Seq), DeltaFile))
	if err != nil {
		t.Fatal(err)
	}
	prevNames := make(map[string]bool)
	for _, fe := range m1.Files {
		prevNames[fe.Name] = true
	}
	if len(d2.Files) == 0 || len(d2.Files) >= len(m2.Files) {
		t.Fatalf("delta2 should be a proper subset: delta=%d full=%d", len(d2.Files), len(m2.Files))
	}
	for _, fe := range d2.Files {
		if prevNames[fe.Name] {
			t.Fatalf("delta2 contains old file %s", fe.Name)
		}
	}
	if len(m1.Files)+len(d2.Files) != len(m2.Files) {
		t.Fatalf("full2 (%d) != full1 (%d) + delta2 (%d)", len(m2.Files), len(m1.Files), len(d2.Files))
	}

	// Compaction 后快照 3：全量清单只剩合并后的新文件。
	if err := db.Compact(); err != nil {
		t.Fatal(err)
	}
	m3, err := sn.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	if len(m3.Files) != 1 {
		t.Fatalf("after compact expect 1 sstable in snapshot, got %d", len(m3.Files))
	}
	d3, err := LoadManifest(filepath.Join(sn.SnapshotDir(m3.Seq), DeltaFile))
	if err != nil {
		t.Fatal(err)
	}
	if len(d3.Files) != 1 || d3.Files[0].Name != m3.Files[0].Name {
		t.Fatalf("delta3 should be the merged file: %+v", d3.Files)
	}

	// 三个快照都必须通过完整性校验（即使源文件已被删除）。
	for _, m := range []*Manifest{m1, m2, m3} {
		if err := Verify(sn.SnapshotDir(m.Seq)); err != nil {
			t.Fatalf("verify snap %d: %v", m.Seq, err)
		}
	}
}

// TestSnapshotterResume 验证快照器重启后延续序号与增量链。
func TestSnapshotterResume(t *testing.T) {
	dbDir := tempDir(t)
	snapRoot := tempDir(t)
	defer os.RemoveAll(dbDir)
	defer os.RemoveAll(snapRoot)

	db, err := lsm.Open(dbDir, lsm.Options{FlushThresholdBytes: 2048, CompactThreshold: 1 << 30})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	writeBatch(t, db, "a", "a", 0, 300)
	sn1, err := NewSnapshotter(db, snapRoot)
	if err != nil {
		t.Fatal(err)
	}
	m1, err := sn1.Snapshot()
	if err != nil {
		t.Fatal(err)
	}

	// 模拟进程重启：新建 Snapshotter，应接着上次序号继续。
	writeBatch(t, db, "b", "b", 0, 300)
	sn2, err := NewSnapshotter(db, snapRoot)
	if err != nil {
		t.Fatal(err)
	}
	m2, err := sn2.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	if m2.Seq != m1.Seq+1 {
		t.Fatalf("expected seq %d, got %d", m1.Seq+1, m2.Seq)
	}
	d2, err := LoadManifest(filepath.Join(sn2.SnapshotDir(m2.Seq), DeltaFile))
	if err != nil {
		t.Fatal(err)
	}
	for _, fe := range d2.Files {
		for _, old := range m1.Files {
			if fe.Name == old.Name {
				t.Fatalf("resumed delta contains old file %s", fe.Name)
			}
		}
	}
}

// TestVerifyDetectsCorruption 验证清单校验能发现快照数据损坏。
func TestVerifyDetectsCorruption(t *testing.T) {
	dbDir := tempDir(t)
	snapRoot := tempDir(t)
	defer os.RemoveAll(dbDir)
	defer os.RemoveAll(snapRoot)

	db, err := lsm.Open(dbDir, lsm.Options{FlushThresholdBytes: 1024, CompactThreshold: 1 << 30})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	writeBatch(t, db, "a", "a", 0, 200)

	sn, err := NewSnapshotter(db, snapRoot)
	if err != nil {
		t.Fatal(err)
	}
	m, err := sn.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	snapDir := sn.SnapshotDir(m.Seq)
	if err := Verify(snapDir); err != nil {
		t.Fatalf("fresh snapshot should verify: %v", err)
	}

	// 篡改快照文件的一个字节。
	victim := filepath.Join(snapDir, m.Files[0].Name)
	f, err := os.OpenFile(victim, os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteAt([]byte{0xAA}, 10); err != nil {
		t.Fatal(err)
	}
	f.Close()
	if err := Verify(snapDir); err == nil {
		t.Fatal("expected verify to detect corruption")
	}
}
