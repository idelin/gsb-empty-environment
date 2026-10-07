package importer

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func writeSource(t *testing.T, dir string, lines ...string) string {
	t.Helper()
	path := filepath.Join(dir, "source.log")
	content := ""
	for _, l := range lines {
		content += l + "\n"
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func appendSource(t *testing.T, path string, data string) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(data); err != nil {
		f.Close()
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
}

func newImporter(src, dir string, store *MemStore, batchSize int) *Importer {
	return &Importer{
		SourcePath:     src,
		CheckpointPath: filepath.Join(dir, "checkpoint.json"),
		Store:          store,
		BatchSize:      batchSize,
	}
}

func wantRecords(n int) [][]byte {
	var out [][]byte
	for i := 1; i <= n; i++ {
		out = append(out, []byte(fmt.Sprintf("rec-%d", i)))
	}
	return out
}

func lines(n int) []string {
	var out []string
	for i := 1; i <= n; i++ {
		out = append(out, fmt.Sprintf("rec-%d", i))
	}
	return out
}

// 源尾追加：中断后在源文件尾部追加记录，恢复导入必须不漏不重。
func TestTailAppend(t *testing.T) {
	dir := t.TempDir()
	src := writeSource(t, dir, lines(4)...)
	store := &MemStore{}

	if err := newImporter(src, dir, store, 2).Run(); err != nil {
		t.Fatalf("first run: %v", err)
	}
	if got := store.Records(); !reflect.DeepEqual(got, wantRecords(4)) {
		t.Fatalf("after first run got %q", got)
	}

	appendSource(t, src, "rec-5\nrec-6\n")

	if err := newImporter(src, dir, store, 2).Run(); err != nil {
		t.Fatalf("resume run: %v", err)
	}
	if got, want := store.Records(), wantRecords(6); !reflect.DeepEqual(got, want) {
		t.Fatalf("after resume got %q, want %q (must not lose or duplicate)", got, want)
	}
	last, _ := store.LastBatch()
	if last != 3 {
		t.Fatalf("target last batch = %d, want 3", last)
	}
	cp, found, err := loadCheckpoint(filepath.Join(dir, "checkpoint.json"))
	if err != nil || !found {
		t.Fatalf("load checkpoint: found=%v err=%v", found, err)
	}
	info, _ := os.Stat(src)
	if cp.Offset != info.Size() || cp.Batch != 3 || cp.PrefixDigest == "" {
		t.Fatalf("checkpoint = %+v, want offset=%d batch=3 with digest", cp, info.Size())
	}
}

// 源中段修改：已导入区域被改动后，前缀摘要不匹配，恢复必须明确报错停止。
func TestSourceMidModification(t *testing.T) {
	dir := t.TempDir()
	src := writeSource(t, dir, lines(4)...)
	store := &MemStore{}

	if err := newImporter(src, dir, store, 2).Run(); err != nil {
		t.Fatalf("first run: %v", err)
	}

	// Modify a byte inside the already-imported region, keeping the file
	// length identical so only the digest can catch it.
	data, err := os.ReadFile(src)
	if err != nil {
		t.Fatal(err)
	}
	data[4] = 'X' // "rec-1" -> "rec-X"
	if err := os.WriteFile(src, data, 0o644); err != nil {
		t.Fatal(err)
	}
	appendSource(t, src, "rec-5\n")

	err = newImporter(src, dir, store, 2).Run()
	if !errors.Is(err, ErrSourcePrefixMismatch) {
		t.Fatalf("resume err = %v, want ErrSourcePrefixMismatch", err)
	}
	if got := store.Records(); !reflect.DeepEqual(got, wantRecords(4)) {
		t.Fatalf("store changed after failed resume: %q", got)
	}
}

// 回执丢失：批次已提交但回执丢失、检查点未保存，恢复时必须靠目标批号
// 核对 + 幂等重交去重，不能重复导入。
func TestReceiptLoss(t *testing.T) {
	dir := t.TempDir()
	src := writeSource(t, dir, lines(6)...)
	store := &MemStore{DropReceipts: map[uint64]bool{2: true}}

	err := newImporter(src, dir, store, 2).Run()
	if !errors.Is(err, ErrReceiptLost) {
		t.Fatalf("first run err = %v, want ErrReceiptLost", err)
	}
	// Batch 2 was committed on the target, but the checkpoint still says 1.
	cp, _, err := loadCheckpoint(filepath.Join(dir, "checkpoint.json"))
	if err != nil {
		t.Fatal(err)
	}
	if cp.Batch != 1 {
		t.Fatalf("checkpoint batch = %d, want 1 (receipt lost before save)", cp.Batch)
	}
	if last, _ := store.LastBatch(); last != 2 {
		t.Fatalf("target last batch = %d, want 2", last)
	}

	// Resume: target is one batch ahead of the checkpoint; the importer
	// must recommit batch 2 idempotently instead of duplicating it.
	if err := newImporter(src, dir, store, 2).Run(); err != nil {
		t.Fatalf("resume run: %v", err)
	}
	if got, want := store.Records(), wantRecords(6); !reflect.DeepEqual(got, want) {
		t.Fatalf("after resume got %q, want %q (no loss, no duplication)", got, want)
	}
	if last, _ := store.LastBatch(); last != 3 {
		t.Fatalf("target last batch = %d, want 3", last)
	}
}

// 只保存偏移不算有效恢复：缺少前缀摘要/批号的检查点必须被拒绝。
func TestOffsetOnlyCheckpointRejected(t *testing.T) {
	dir := t.TempDir()
	src := writeSource(t, dir, lines(4)...)
	cpPath := filepath.Join(dir, "checkpoint.json")
	if err := os.WriteFile(cpPath, []byte(`{"offset":12}`), 0o644); err != nil {
		t.Fatal(err)
	}
	store := &MemStore{}
	err := newImporter(src, dir, store, 2).Run()
	if !errors.Is(err, ErrInvalidCheckpoint) {
		t.Fatalf("run err = %v, want ErrInvalidCheckpoint", err)
	}
	if got := store.Records(); len(got) != 0 {
		t.Fatalf("store received records despite invalid checkpoint: %q", got)
	}
}

// 目标批号不一致（目标落后于检查点）必须明确停止。
func TestTargetBatchMismatch(t *testing.T) {
	dir := t.TempDir()
	src := writeSource(t, dir, lines(4)...)
	if err := newImporter(src, dir, &MemStore{}, 2).Run(); err != nil {
		t.Fatalf("first run: %v", err)
	}
	// Resume against a fresh (regressed) target.
	err := newImporter(src, dir, &MemStore{}, 2).Run()
	if !errors.Is(err, ErrTargetBatchMismatch) {
		t.Fatalf("resume err = %v, want ErrTargetBatchMismatch", err)
	}
}

// 记录边界：文件末尾未以换行结尾的半行不算记录，偏移停在记录边界；
// 追加补全后再恢复才导入。
func TestPartialLineStaysOnRecordBoundary(t *testing.T) {
	dir := t.TempDir()
	src := writeSource(t, dir, lines(2)...)
	appendSource(t, src, "rec-3") // no trailing newline yet
	store := &MemStore{}

	if err := newImporter(src, dir, store, 10).Run(); err != nil {
		t.Fatalf("first run: %v", err)
	}
	if got := store.Records(); !reflect.DeepEqual(got, wantRecords(2)) {
		t.Fatalf("partial line imported too early: %q", got)
	}

	appendSource(t, src, "\nrec-4\n")
	if err := newImporter(src, dir, store, 10).Run(); err != nil {
		t.Fatalf("resume run: %v", err)
	}
	if got, want := store.Records(), wantRecords(4); !reflect.DeepEqual(got, want) {
		t.Fatalf("after resume got %q, want %q", got, want)
	}
	if strings.Count(fmt.Sprintf("%q", store.Records()), "rec-3") != 1 {
		t.Fatalf("rec-3 imported more than once: %q", store.Records())
	}
}
