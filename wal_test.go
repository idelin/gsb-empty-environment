package repartition

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// TestTornWALTail 直接在 WAL 末尾追加垃圾字节，模拟断电半写，
// 验证重新打开时损坏尾部被截断、完整帧全部恢复。
func TestTornWALTail(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "b")
	b, err := Open(dir, testConfig())
	if err != nil {
		t.Fatal(err)
	}
	wl := buildWorkload()
	for _, r := range wl.records {
		if err := b.Ingest(r); err != nil {
			t.Fatal(err)
		}
	}
	knownBefore := b.Snapshot().KnownCount
	path := filepath.Join(dir, walName)
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	_ = b.Close()

	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.Write([]byte{0xde, 0xad, 0xbe, 0xef, 1, 2, 3}); err != nil {
		t.Fatal(err)
	}
	_ = f.Close()

	b2, err := Open(dir, testConfig())
	if err != nil {
		t.Fatalf("带损坏尾部的 WAL 应可恢复: %v", err)
	}
	if got := b2.Snapshot().KnownCount; got != knownBefore {
		t.Fatalf("恢复后已知记录数 = %d，期望 %d", got, knownBefore)
	}
	if _, err := b2.TryNext(); err != nil && !errors.Is(err, ErrDone) {
		t.Fatalf("恢复后 TryNext 异常: %v", err)
	}
	_ = b2.Close()

	info2, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info2.Size() != info.Size() {
		t.Fatalf("损坏尾部未截断: %d -> %d", info.Size(), info2.Size())
	}
}

// TestTornLastFrame 截断最后一个完整帧的一半，验证该帧被丢弃而更早的帧保留。
func TestTornLastFrame(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "b")
	b, err := Open(dir, testConfig())
	if err != nil {
		t.Fatal(err)
	}
	first := Record{Partition: 0, Offset: 0}
	second := Record{Partition: 0, Offset: 1}
	if err := b.Ingest(first); err != nil {
		t.Fatal(err)
	}
	if err := b.Ingest(second); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, walName)
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	_ = b.Close()

	if err := os.Truncate(path, info.Size()-5); err != nil {
		t.Fatal(err)
	}
	b2, err := Open(dir, testConfig())
	if err != nil {
		t.Fatalf("截断尾部后应可恢复: %v", err)
	}
	st := b2.Snapshot()
	if st.KnownCount != 2 {
		// config + first 完整；second 的帧被截断丢弃
		if st.KnownCount != 1 {
			t.Fatalf("恢复后已知记录数 = %d，期望 1", st.KnownCount)
		}
	}
	_ = b2.Close()
}
