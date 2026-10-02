package versionset

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// flushL0 模拟 memtable 落盘：写一个 L0 SSTable 并提交 VersionEdit。
func flushL0(t *testing.T, vs *VersionSet, data string) *FileMeta {
	t.Helper()
	num := vs.NextFileNum()
	meta, err := vs.WriteSSTable(num, 0, []byte(data), []byte("a"), []byte("z"))
	if err != nil {
		t.Fatalf("WriteSSTable: %v", err)
	}
	edit := &VersionEdit{NewFiles: []FileMeta{*meta}}
	if err := vs.LogAndApply(edit); err != nil {
		t.Fatalf("LogAndApply: %v", err)
	}
	return meta
}

// fileExists 报告路径是否存在。
func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func sstNums(v *Version, level int) []uint64 {
	var nums []uint64
	for _, f := range v.Files[level] {
		nums = append(nums, f.FileNum)
	}
	return nums
}

// TestEditRoundTrip 校验 VersionEdit 编解码往返一致。
func TestEditRoundTrip(t *testing.T) {
	in := &VersionEdit{
		DeletedFiles:   []DeletedFile{{Level: 0, FileNum: 3}, {Level: 1, FileNum: 7}},
		NewFiles:       []FileMeta{{Level: 1, FileNum: 9, Size: 123, Checksum: 0xdeadbeef, Smallest: []byte("a"), Largest: []byte("k")}},
		NextFileNum:    42,
		HasNextFileNum: true,
	}
	var out VersionEdit
	if err := out.decode(in.encode()); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !reflect.DeepEqual(in, &out) {
		t.Fatalf("round trip mismatch:\n in=%+v\nout=%+v", in, &out)
	}
}

// TestReopenRestoresVersion 基本场景：提交后重开，版本完整恢复。
func TestReopenRestoresVersion(t *testing.T) {
	dir := t.TempDir()
	vs, err := Open(dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	m1 := flushL0(t, vs, "hello")
	m2 := flushL0(t, vs, "world")
	if err := vs.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	vs2, err := Open(dir)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer vs2.Close()
	got := sstNums(vs2.Current(), 0)
	want := []uint64{m1.FileNum, m2.FileNum}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("L0 files = %v, want %v", got, want)
	}
}

// TestCrashDuringCompaction 是验收边界场景：
// L0->L1 Compaction 已把新文件写盘并 fsync，但 VersionEdit 尚未提交时崩溃。
// 重启后必须恢复到 Compaction 前的版本，且孤儿 SSTable 被清除。
func TestCrashDuringCompaction(t *testing.T) {
	dir := t.TempDir()
	vs, err := Open(dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	m1 := flushL0(t, vs, "aaa")
	m2 := flushL0(t, vs, "bbb")

	// 开始 L0->L1 Compaction：分配文件号，写出合并后的新 SSTable 并 fsync。
	outNum := vs.NextFileNum()
	out, err := vs.WriteSSTable(outNum, 1, []byte("aaabbb"), []byte("a"), []byte("b"))
	if err != nil {
		t.Fatalf("WriteSSTable: %v", err)
	}
	outPath := filepath.Join(dir, sstName(out.FileNum))
	if !fileExists(outPath) {
		t.Fatalf("compaction output %s not on disk", outPath)
	}

	// *** 模拟崩溃：进程直接消失，LogAndApply 从未执行，Close 也不会调用。 ***
	// （vs 被直接遗弃，等价于断电。）

	vs2, err := Open(dir) // 重启恢复
	if err != nil {
		t.Fatalf("reopen after crash: %v", err)
	}
	defer vs2.Close()

	// 版本必须回到 Compaction 之前：L0 两个文件，L1 为空。
	if got := sstNums(vs2.Current(), 0); !reflect.DeepEqual(got, []uint64{m1.FileNum, m2.FileNum}) {
		t.Fatalf("L0 files after recovery = %v, want [%d %d]", got, m1.FileNum, m2.FileNum)
	}
	if got := sstNums(vs2.Current(), 1); len(got) != 0 {
		t.Fatalf("L1 should be empty after recovery, got %v", got)
	}
	// 未提交的 Compaction 输出是孤儿文件，必须被清理。
	if fileExists(outPath) {
		t.Fatalf("orphan sstable %s was not removed during recovery", outPath)
	}
	// 崩溃前分配但未提交的文件号应被回收复用。
	if n := vs2.NextFileNum(); n != outNum {
		t.Fatalf("file number not reused: got %d, want %d", n, outNum)
	}
}

// TestCommittedCompactionSurvivesRestart 对照场景：
// Compaction 提交后崩溃，新版本必须完整恢复，旧输入文件被清除。
func TestCommittedCompactionSurvivesRestart(t *testing.T) {
	dir := t.TempDir()
	vs, err := Open(dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	m1 := flushL0(t, vs, "aaa")
	m2 := flushL0(t, vs, "bbb")

	outNum := vs.NextFileNum()
	out, err := vs.WriteSSTable(outNum, 1, []byte("aaabbb"), []byte("a"), []byte("b"))
	if err != nil {
		t.Fatalf("WriteSSTable: %v", err)
	}
	edit := &VersionEdit{
		DeletedFiles: []DeletedFile{{Level: 0, FileNum: m1.FileNum}, {Level: 0, FileNum: m2.FileNum}},
		NewFiles:     []FileMeta{*out},
	}
	if err := vs.LogAndApply(edit); err != nil { // 提交点
		t.Fatalf("LogAndApply: %v", err)
	}
	// *** 崩溃：不调用 Close。 ***

	vs2, err := Open(dir)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer vs2.Close()
	if got := sstNums(vs2.Current(), 0); len(got) != 0 {
		t.Fatalf("L0 should be empty, got %v", got)
	}
	if got := sstNums(vs2.Current(), 1); !reflect.DeepEqual(got, []uint64{outNum}) {
		t.Fatalf("L1 = %v, want [%d]", got, outNum)
	}
	for _, m := range []*FileMeta{m1, m2} {
		if fileExists(filepath.Join(dir, sstName(m.FileNum))) {
			t.Fatalf("obsolete input %d still on disk", m.FileNum)
		}
	}
}

// TestCrashBetweenCommitAndObsoleteDelete 边界：
// VersionEdit 已 fsync 提交，但旧文件还没来得及删就崩溃。
// 恢复时这些文件已不被任何版本引用，必须作为孤儿清理。
func TestCrashBetweenCommitAndObsoleteDelete(t *testing.T) {
	dir := t.TempDir()
	vs, err := Open(dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	m1 := flushL0(t, vs, "aaa")
	outNum := vs.NextFileNum()
	out, err := vs.WriteSSTable(outNum, 1, []byte("aaa"), []byte("a"), []byte("a"))
	if err != nil {
		t.Fatalf("WriteSSTable: %v", err)
	}
	edit := &VersionEdit{
		DeletedFiles: []DeletedFile{{Level: 0, FileNum: m1.FileNum}},
		NewFiles:     []FileMeta{*out},
	}
	if err := vs.LogAndApply(edit); err != nil {
		t.Fatalf("LogAndApply: %v", err)
	}
	// 模拟"已删除文件仍残留磁盘"（崩溃发生在 LogAndApply 内部清理之前）：
	// 把旧文件内容写回去。
	stale := filepath.Join(dir, sstName(m1.FileNum))
	if err := os.WriteFile(stale, []byte("aaa"), 0o644); err != nil {
		t.Fatalf("recreate stale file: %v", err)
	}

	vs2, err := Open(dir)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer vs2.Close()
	if fileExists(stale) {
		t.Fatalf("stale file %s not removed during recovery", stale)
	}
	if got := sstNums(vs2.Current(), 1); !reflect.DeepEqual(got, []uint64{outNum}) {
		t.Fatalf("L1 = %v, want [%d]", got, outNum)
	}
}

// TestTornManifestTail 崩溃在 MANIFEST 写了一半时：
// 恢复必须截断撕裂的尾部记录，保留已提交版本。
func TestTornManifestTail(t *testing.T) {
	dir := t.TempDir()
	vs, err := Open(dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	m1 := flushL0(t, vs, "aaa")
	manifest := manifestPathOf(dir)
	info, err := os.Stat(manifest)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	goodLen := info.Size()

	// 模拟撕裂写：追加半条记录（头部完整、payload 残缺）。
	f, err := os.OpenFile(manifest, os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		t.Fatalf("open manifest: %v", err)
	}
	if _, err := f.Write([]byte{0xde, 0xad, 0xbe, 0xef, 0x10, 0, 0, 0, 0x01, 0x02}); err != nil {
		t.Fatalf("write torn record: %v", err)
	}
	f.Close()

	vs2, err := Open(dir)
	if err != nil {
		t.Fatalf("reopen with torn tail: %v", err)
	}
	defer vs2.Close()
	if got := sstNums(vs2.Current(), 0); !reflect.DeepEqual(got, []uint64{m1.FileNum}) {
		t.Fatalf("L0 = %v, want [%d]", got, m1.FileNum)
	}
	// 撕裂部分应被截断。
	info2, err := os.Stat(manifest)
	if err != nil {
		t.Fatalf("stat after recovery: %v", err)
	}
	if info2.Size() != goodLen {
		t.Fatalf("manifest size = %d, want truncated to %d", info2.Size(), goodLen)
	}
	// 截断后仍可继续提交新版本。
	m2 := flushL0(t, vs2, "bbb")
	if got := sstNums(vs2.Current(), 0); !reflect.DeepEqual(got, []uint64{m1.FileNum, m2.FileNum}) {
		t.Fatalf("L0 after new flush = %v", got)
	}
}

// TestLiveFileCorruptionDetected 存活文件内容被破坏时，恢复必须报错而不是静默接受。
func TestLiveFileCorruptionDetected(t *testing.T) {
	dir := t.TempDir()
	vs, err := Open(dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	m1 := flushL0(t, vs, "aaa")
	vs.Close()

	if err := os.WriteFile(filepath.Join(dir, sstName(m1.FileNum)), []byte("corrupted"), 0o644); err != nil {
		t.Fatalf("corrupt file: %v", err)
	}
	if _, err := Open(dir); err == nil {
		t.Fatalf("expected checksum mismatch error, got nil")
	}
}

func sstName(num uint64) string {
	return fmt.Sprintf("%06d%s", num, sstSuffix)
}

func manifestPathOf(dir string) string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return ""
	}
	for _, e := range entries {
		if len(e.Name()) >= len(manifestPrefix) && e.Name()[:len(manifestPrefix)] == manifestPrefix {
			return filepath.Join(dir, e.Name())
		}
	}
	return ""
}
