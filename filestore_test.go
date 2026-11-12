package netsec

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func newTempFileStore(t *testing.T) *FileStore {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "store")
	store, err := NewFileStore(dir)
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}
	return store
}

func sampleRecord() Record {
	return Record{
		DeviceID:     "device-1",
		Field:        "browsing_history",
		ParamVersion: "v1",
		Fingerprint:  "fp-xyz",
		Truth:        0,
		Permanent:    1,
	}
}

func TestFileStoreRoundTrip(t *testing.T) {
	store := newTempFileStore(t)
	ctx := context.Background()
	rec := sampleRecord()
	key := Key(rec.DeviceID, rec.Field, rec.ParamVersion)

	if _, err := store.Load(ctx, key); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Load before commit = %v, want ErrNotFound", err)
	}
	if err := store.Commit(ctx, key, rec); err != nil {
		t.Fatalf("Commit: %v", err)
	}
	got, err := store.Load(ctx, key)
	if err != nil {
		t.Fatalf("Load after commit: %v", err)
	}
	if got != rec {
		t.Fatalf("loaded record = %+v, want %+v", got, rec)
	}

	// 重复提交保持幂等，不覆盖既有永久记忆。
	other := rec
	other.Permanent = 0
	if err := store.Commit(ctx, key, other); err != nil {
		t.Fatalf("idempotent Commit: %v", err)
	}
	got, _ = store.Load(ctx, key)
	if got.Permanent != rec.Permanent {
		t.Fatalf("record overwritten: permanent = %d, want %d", got.Permanent, rec.Permanent)
	}
}

func TestFileStoreInterruptStages(t *testing.T) {
	stages := []string{
		StageTempCreate,
		StageTempWrite,
		StageTempSync,
		StageRename,
		StageDirSync,
	}
	for _, stage := range stages {
		t.Run(stage, func(t *testing.T) {
			store := newTempFileStore(t)
			store.Hook = func(_ context.Context, gotStage, _ string) error {
				if gotStage == stage {
					return errors.New("injected interrupt at " + gotStage)
				}
				return nil
			}
			ctx := context.Background()
			rec := sampleRecord()
			key := Key(rec.DeviceID, rec.Field, rec.ParamVersion)

			err := store.Commit(ctx, key, rec)
			if err == nil {
				t.Fatalf("interrupt at %s did not fail Commit", stage)
			}

			// DirSync 之前的阶段失败：最终文件不存在，Load 返回 NotFound。
			// DirSync 阶段失败：rename 已完成，记录对 Load 已可见。
			_, loadErr := store.Load(ctx, key)
			if stage == StageDirSync {
				if loadErr != nil {
					t.Fatalf("after rename record should be readable, got %v", loadErr)
				}
			} else if !errors.Is(loadErr, ErrNotFound) {
				t.Fatalf("Load after interrupt at %s = %v, want ErrNotFound", stage, loadErr)
			}

			// 任何中断都不得残留可被 Load 观察到的半成品；重试一次必然成功。
			store.Hook = nil
			if err := store.Commit(ctx, key, rec); err != nil {
				t.Fatalf("retry Commit: %v", err)
			}
			got, err := store.Load(ctx, key)
			if err != nil {
				t.Fatalf("Load after retry: %v", err)
			}
			if got != rec {
				t.Fatalf("retried record = %+v, want %+v", got, rec)
			}
		})
	}
}

func TestFileStoreCancelledContext(t *testing.T) {
	store := newTempFileStore(t)
	store.Hook = func(ctx context.Context, stage, _ string) error {
		if stage == StageTempWrite {
			return ctx.Err()
		}
		return nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	rec := sampleRecord()
	key := Key(rec.DeviceID, rec.Field, rec.ParamVersion)
	if err := store.Commit(ctx, key, rec); !errors.Is(err, context.Canceled) {
		t.Fatalf("Commit error = %v, want context.Canceled", err)
	}
	if _, err := store.Load(context.Background(), key); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cancelled commit left readable record: %v", err)
	}
}

func TestFileStoreIgnoresTempFiles(t *testing.T) {
	store := newTempFileStore(t)
	ctx := context.Background()
	rec := sampleRecord()
	key := Key(rec.DeviceID, rec.Field, rec.ParamVersion)

	// 手工放置一个残留临时文件和一个无关文件，Load 必须忽略它们。
	if err := os.WriteFile(filepath.Join(store.dir, ".rec-"+key+".tmp-deadbeef"), []byte("{bad"), 0o600); err != nil {
		t.Fatalf("seed temp file: %v", err)
	}
	if err := os.WriteFile(filepath.Join(store.dir, "notes.txt"), []byte("ignore me"), 0o600); err != nil {
		t.Fatalf("seed unrelated file: %v", err)
	}
	if _, err := store.Load(ctx, key); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Load with leftovers = %v, want ErrNotFound", err)
	}

	if err := store.Commit(ctx, key, rec); err != nil {
		t.Fatalf("Commit alongside leftovers: %v", err)
	}
	if _, err := store.Load(ctx, key); err != nil {
		t.Fatalf("Load after commit: %v", err)
	}
}

func TestFileStoreCorruptionDetected(t *testing.T) {
	store := newTempFileStore(t)
	ctx := context.Background()
	rec := sampleRecord()
	key := Key(rec.DeviceID, rec.Field, rec.ParamVersion)

	if err := store.Commit(ctx, key, rec); err != nil {
		t.Fatalf("Commit: %v", err)
	}
	finalPath := filepath.Join(store.dir, "rec-"+key+".json")
	if err := os.WriteFile(finalPath, []byte("{not json"), 0o600); err != nil {
		t.Fatalf("corrupt record: %v", err)
	}
	if _, err := store.Load(ctx, key); err == nil || strings.Contains(err.Error(), "corrupt") == false {
		t.Fatalf("corrupt record Load error = %v, want corruption error", err)
	}

	// 内嵌身份与文件名不一致也必须拒绝。
	wrong := rec
	wrong.Field = "other_field"
	if err := os.WriteFile(finalPath, mustJSON(t, wrong), 0o600); err != nil {
		t.Fatalf("rewrite record: %v", err)
	}
	if _, err := store.Load(ctx, key); err == nil || !strings.Contains(err.Error(), "key mismatch") {
		t.Fatalf("mismatched record Load error = %v, want key mismatch error", err)
	}
}

func TestFileStoreKeyIsolation(t *testing.T) {
	store := newTempFileStore(t)
	ctx := context.Background()
	key1 := Key("device-1", "field-a", "v1")
	key2 := Key("device-1", "field-b", "v1")
	rec1 := Record{DeviceID: "device-1", Field: "field-a", ParamVersion: "v1", Fingerprint: "fp1", Truth: 0, Permanent: 0}
	rec2 := Record{DeviceID: "device-1", Field: "field-b", ParamVersion: "v1", Fingerprint: "fp2", Truth: 0, Permanent: 1}
	if err := store.Commit(ctx, key1, rec1); err != nil {
		t.Fatalf("Commit key1: %v", err)
	}
	if err := store.Commit(ctx, key2, rec2); err != nil {
		t.Fatalf("Commit key2: %v", err)
	}
	got1, err := store.Load(ctx, key1)
	if err != nil || got1.Permanent != 0 {
		t.Fatalf("Load key1 = (%+v, %v)", got1, err)
	}
	got2, err := store.Load(ctx, key2)
	if err != nil || got2.Permanent != 1 {
		t.Fatalf("Load key2 = (%+v, %v)", got2, err)
	}
}

func TestFileStoreConcurrentSameKey(t *testing.T) {
	store := newTempFileStore(t)
	ctx := context.Background()
	key := Key("device-1", "field-a", "v1")
	rec := Record{DeviceID: "device-1", Field: "field-a", ParamVersion: "v1", Fingerprint: "fp", Truth: 0, Permanent: 1}

	const n = 16
	var wg sync.WaitGroup
	errs := make([]error, n)
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func(i int) {
			defer wg.Done()
			errs[i] = store.Commit(ctx, key, rec)
		}(i)
	}
	wg.Wait()
	for _, err := range errs {
		if err != nil {
			t.Fatalf("concurrent Commit error: %v", err)
		}
	}
	got, err := store.Load(ctx, key)
	if err != nil {
		t.Fatalf("Load after concurrent commits: %v", err)
	}
	if got != rec {
		t.Fatalf("record = %+v, want %+v", got, rec)
	}
	matches, _ := filepath.Glob(filepath.Join(store.dir, "rec-*.json"))
	if len(matches) != 1 {
		t.Fatalf("record files = %d, want 1", len(matches))
	}
}

func mustJSON(t *testing.T, rec Record) []byte {
	t.Helper()
	data, err := json.Marshal(rec)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return data
}
