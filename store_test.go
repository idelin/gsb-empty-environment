package netsec

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func TestFileStoreAtomicReplaceAndContext(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "nested", "store.json")
	store, err := NewFileStore(path)
	if err != nil {
		t.Fatal(err)
	}

	if data, err := store.Load(ctx); err != nil || len(data) != 0 {
		t.Fatalf("empty load = %q, %v", data, err)
	}
	if err := store.Save(ctx, []byte("first")); err != nil {
		t.Fatal(err)
	}
	if err := store.Save(ctx, []byte("second")); err != nil {
		t.Fatal(err)
	}
	data, err := store.Load(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "second" {
		t.Fatalf("load = %q, want second", data)
	}

	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if err := store.Save(canceled, []byte("third")); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled save error = %v", err)
	}
	data, _ = store.Load(ctx)
	if string(data) != "second" {
		t.Fatalf("canceled save changed data to %q", data)
	}
}

func TestFileStoreConcurrentSavesLeaveCompleteSnapshot(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "store.json")
	store, err := NewFileStore(path)
	if err != nil {
		t.Fatal(err)
	}

	const workers = 16
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			// Each payload is self-contained; a torn file would not match any
			// one payload.
			payload := make([]byte, 4096)
			for j := range payload {
				payload[j] = byte(n)
			}
			if err := store.Save(ctx, payload); err != nil {
				t.Errorf("save %d: %v", n, err)
			}
		}(i)
	}
	wg.Wait()

	data, err := store.Load(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(data) != 4096 {
		t.Fatalf("snapshot length = %d, want 4096", len(data))
	}
	for i := 1; i < len(data); i++ {
		if data[i] != data[0] {
			t.Fatalf("snapshot is torn at byte %d", i)
		}
	}

	matches, err := filepath.Glob(filepath.Join(filepath.Dir(path), ".store.json.tmp-*"))
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 0 {
		entries, _ := os.ReadDir(filepath.Dir(path))
		names := make([]string, 0, len(entries))
		for _, entry := range entries {
			names = append(names, entry.Name())
		}
		t.Fatalf("temporary files left behind: %v", names)
	}
}

func TestStoreRejectsEmptyPath(t *testing.T) {
	if _, err := NewFileStore("  "); !errors.Is(err, ErrInvalidOptions) {
		t.Fatalf("empty path error = %v, want ErrInvalidOptions", err)
	}
}
