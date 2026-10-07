package importer

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeRecords 将记录以换行分隔写入源文件，返回文件路径。
func writeRecords(t *testing.T, dir string, records []string) string {
	t.Helper()
	path := filepath.Join(dir, "source.log")
	var b strings.Builder
	for _, r := range records {
		b.WriteString(r)
		b.WriteByte('\n')
	}
	if err := os.WriteFile(path, []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func appendRecords(t *testing.T, path string, records []string) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	for _, r := range records {
		if _, err := f.WriteString(r + "\n"); err != nil {
			t.Fatal(err)
		}
	}
}

func recordStrings(records [][]byte) []string {
	out := make([]string, len(records))
	for i, r := range records {
		out[i] = string(r)
	}
	return out
}

// assertNoLossNoDup 断言目标端记录与期望完全一致：不漏、不重、顺序一致。
func assertNoLossNoDup(t *testing.T, sink *MemSink, want []string) {
	t.Helper()
	got := recordStrings(sink.Records())
	if len(got) != len(want) {
		t.Fatalf("record count mismatch: got %d, want %d\ngot:  %v\nwant: %v", len(got), len(want), got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("record %d mismatch: got %q, want %q", i, got[i], want[i])
		}
	}
	for k, v := range CountRecords(sink.Records()) {
		if v != 1 {
			t.Fatalf("duplicate record %q x%d", k, v)
		}
	}
}

func genRecords(prefix string, n int) []string {
	out := make([]string, n)
	for i := range out {
		out[i] = fmt.Sprintf("%s-%04d", prefix, i)
	}
	return out
}

// TestTailAppendResume：源尾追加后续传，不漏不重。
func TestTailAppendResume(t *testing.T) {
	dir := t.TempDir()
	first := genRecords("rec", 7)
	src := writeRecords(t, dir, first)
	sink := NewMemSink()
	store := NewCheckpointStore(filepath.Join(dir, "checkpoint.json"))

	imp := NewImporter(src, sink, store, 3) // 7 条 -> 批 1,2,3
	if err := imp.Run(); err != nil {
		t.Fatalf("first run: %v", err)
	}
	assertNoLossNoDup(t, sink, first)

	cp, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if cp.Batch != 3 {
		t.Fatalf("checkpoint batch = %d, want 3", cp.Batch)
	}
	info, _ := os.Stat(src)
	if cp.Offset != info.Size() {
		t.Fatalf("checkpoint offset = %d, want EOF %d", cp.Offset, info.Size())
	}

	// 源尾追加新记录，再次运行应只导入新增部分。
	more := genRecords("rec", 7)
	for i := range more {
		more[i] = fmt.Sprintf("rec-%04d", i+7)
	}
	appendRecords(t, src, more)

	if err := NewImporter(src, sink, store, 3).Run(); err != nil {
		t.Fatalf("second run: %v", err)
	}
	assertNoLossNoDup(t, sink, append(first, more...))

	cp, err = store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if cp.Batch != 6 {
		t.Fatalf("checkpoint batch after append = %d, want 6", cp.Batch)
	}
}

// TestSourceMiddleModified：源中段被修改后，恢复核对失败并明确停止，目标端不受污染。
func TestSourceMiddleModified(t *testing.T) {
	dir := t.TempDir()
	records := genRecords("rec", 10)
	src := writeRecords(t, dir, records)
	sink := NewMemSink()
	store := NewCheckpointStore(filepath.Join(dir, "checkpoint.json"))

	if err := NewImporter(src, sink, store, 4).Run(); err != nil {
		t.Fatalf("first run: %v", err)
	}
	assertNoLossNoDup(t, sink, records)

	// 等长修改源文件中段一个字节（保持长度，排除“只是偏移失效”的干扰）。
	data, err := os.ReadFile(src)
	if err != nil {
		t.Fatal(err)
	}
	mid := len(data) / 2
	if data[mid] == 'X' {
		data[mid] = 'Y'
	} else {
		data[mid] = 'X'
	}
	if err := os.WriteFile(src, data, 0o644); err != nil {
		t.Fatal(err)
	}
	appendRecords(t, src, []string{"rec-0010"})

	err = NewImporter(src, sink, store, 4).Run()
	if !errors.Is(err, ErrSourceModified) {
		t.Fatalf("expected ErrSourceModified, got %v", err)
	}
	// 明确停止：目标端保持第一批运行的结果，未导入任何新数据。
	assertNoLossNoDup(t, sink, records)
}

// TestReceiptLost：目标已提交但回执丢失，恢复时对账快进，不漏不重。
func TestReceiptLost(t *testing.T) {
	dir := t.TempDir()
	records := genRecords("rec", 9) // batchSize 2 -> 批 1..5
	src := writeRecords(t, dir, records)
	sink := NewMemSink()
	store := NewCheckpointStore(filepath.Join(dir, "checkpoint.json"))

	// 批 3 提交成功但回执丢失：导入器必须停止且不保存该批检查点。
	sink.LoseReceiptFor[3] = true
	err := NewImporter(src, sink, store, 2).Run()
	if err == nil || !errors.Is(err, ErrReceiptLost) {
		t.Fatalf("expected receipt-lost commit error, got %v", err)
	}

	// 目标端实际已有批 1..3（前 6 条），检查点仍停在批 2。
	if last, _ := sink.LastCommittedBatch(); last != 3 {
		t.Fatalf("sink last batch = %d, want 3", last)
	}
	cp, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if cp.Batch != 2 {
		t.Fatalf("checkpoint batch = %d, want 2", cp.Batch)
	}

	// 故障恢复后重跑：恢复对账应识别批 3 已在目标端，快进到其结束偏移继续。
	delete(sink.LoseReceiptFor, 3)
	if err := NewImporter(src, sink, store, 2).Run(); err != nil {
		t.Fatalf("resume run: %v", err)
	}
	assertNoLossNoDup(t, sink, records)

	cp, err = store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if cp.Batch != 5 {
		t.Fatalf("final checkpoint batch = %d, want 5", cp.Batch)
	}
}

// TestOffsetOnlyCheckpointRejected：只保存偏移的检查点不是有效恢复，必须明确拒绝。
func TestOffsetOnlyCheckpointRejected(t *testing.T) {
	dir := t.TempDir()
	records := genRecords("rec", 4)
	src := writeRecords(t, dir, records)
	sink := NewMemSink()
	store := NewCheckpointStore(filepath.Join(dir, "checkpoint.json"))

	// 手写一个只有偏移的检查点。
	if err := os.WriteFile(store.path, []byte(`{"offset":12}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Load(); !errors.Is(err, ErrInvalidCheckpoint) {
		t.Fatalf("expected ErrInvalidCheckpoint from Load, got %v", err)
	}
	err := NewImporter(src, sink, store, 2).Run()
	if !errors.Is(err, ErrInvalidCheckpoint) {
		t.Fatalf("expected ErrInvalidCheckpoint from Run, got %v", err)
	}
	if got := len(sink.Records()); got != 0 {
		t.Fatalf("sink received %d records despite invalid checkpoint", got)
	}
}

// TestTargetBehindCheckpoint：目标端落后于检查点，属于不一致状态，必须明确停止。
func TestTargetBehindCheckpoint(t *testing.T) {
	dir := t.TempDir()
	records := genRecords("rec", 6)
	src := writeRecords(t, dir, records)
	sink := NewMemSink()
	store := NewCheckpointStore(filepath.Join(dir, "checkpoint.json"))

	if err := NewImporter(src, sink, store, 2).Run(); err != nil {
		t.Fatalf("first run: %v", err)
	}

	// 模拟目标端数据丢失：换一个新的空目标端，但检查点还在。
	freshSink := NewMemSink()
	err := NewImporter(src, freshSink, store, 2).Run()
	if !errors.Is(err, ErrTargetBehind) {
		t.Fatalf("expected ErrTargetBehind, got %v", err)
	}
	if got := len(freshSink.Records()); got != 0 {
		t.Fatalf("fresh sink received %d records despite inconsistent state", got)
	}
}
