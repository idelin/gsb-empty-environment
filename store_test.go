package netsec

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestFileStoreAtomicPublishAndReopen(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nested", "mapping.json")

	store, err := NewFileStore(path)
	if err != nil {
		t.Fatalf("open new file store: %v", err)
	}

	batch := map[string]string{
		"pn1.old-token-a": "pn1.new-token-a",
		"pn1.old-token-b": "pn1.new-token-b",
	}
	if err := store.Commit(context.Background(), batch); err != nil {
		t.Fatalf("first commit: %v", err)
	}

	// 目录中不得遗留临时文件，说明发布走的是 rename 而非原地覆写。
	entries, err := os.ReadDir(filepath.Dir(path))
	if err != nil {
		t.Fatalf("read store dir: %v", err)
	}
	for _, entry := range entries {
		if filepath.Ext(entry.Name()) == ".tmp" {
			t.Fatalf("temporary staging file left behind: %s", entry.Name())
		}
	}

	assertStoreHas(t, store, "pn1.old-token-a", "pn1.new-token-a")

	// 第二次提交合并发布；冲突必须整体失败且磁盘内容不变。
	err = store.Commit(context.Background(), map[string]string{
		"pn1.old-token-a": "pn1.conflicting",
	})
	if !errors.Is(err, ErrMappingConflict) {
		t.Fatalf("want %v, got %v", ErrMappingConflict, err)
	}
	assertStoreHas(t, store, "pn1.old-token-a", "pn1.new-token-a")

	if err := store.Commit(context.Background(), map[string]string{
		"pn1.old-token-c": "pn1.new-token-c",
	}); err != nil {
		t.Fatalf("second commit: %v", err)
	}

	reopened, err := NewFileStore(path)
	if err != nil {
		t.Fatalf("reopen file store: %v", err)
	}
	assertStoreHas(t, reopened, "pn1.old-token-a", "pn1.new-token-a")
	assertStoreHas(t, reopened, "pn1.old-token-b", "pn1.new-token-b")
	assertStoreHas(t, reopened, "pn1.old-token-c", "pn1.new-token-c")
}

func TestFileStoreContextCancellationBeforeRename(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "mapping.json")
	store, err := NewFileStore(path)
	if err != nil {
		t.Fatalf("open file store: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err = store.Commit(ctx, map[string]string{"pn1.k": "pn1.v"})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("want context.Canceled, got %v", err)
	}
	if _, statErr := os.Stat(path); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("canceled commit must not publish a file, stat err=%v", statErr)
	}
}

func TestFileStoreRejectsCorruptPayload(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "mapping.json")
	if err := os.WriteFile(path, []byte("{not-json"), 0o600); err != nil {
		t.Fatalf("seed corrupt file: %v", err)
	}
	if _, err := NewFileStore(path); err == nil {
		t.Fatal("corrupt mapping file must be rejected instead of silently reset")
	}
}
