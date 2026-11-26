package rpki

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestRoundMachineRestoresLastGoodSnapshot(t *testing.T) {
	w := newHappyWorld(t)
	ctx := context.Background()
	snapshots := NewFileSnapshotStore(filepath.Join(t.TempDir(), "snapshots"))
	machine := &RoundMachine{
		States:    w.store,
		Snapshots: snapshots,
		Policy:    FailurePolicyRestoreSnapshot,
		Options: Options{
			Now:      fixedNow(w.h.now),
			MaxDepth: 8,
		},
	}

	good, err := machine.RunRound(ctx, w.h.tal, w.h.repository())
	if err != nil {
		t.Fatalf("good round: %v", err)
	}
	if !good.Accepted || good.SnapshotID == "" {
		t.Fatalf("unexpected good result: %+v", good)
	}

	delete(w.h.files, "root/root.roa")
	bad, err := machine.RunRound(ctx, w.h.tal, w.h.repository())
	if err == nil {
		t.Fatal("expected tampered round to fail")
	}
	if !errors.Is(err, ErrManifestMissing) {
		t.Fatalf("expected missing file error, got %v", err)
	}
	if bad.LastGoodRepository == nil || bad.LastGoodID != good.SnapshotID {
		t.Fatalf("expected last-good snapshot %s, got %+v", good.SnapshotID, bad)
	}
	if bad.RestoreErr != nil {
		t.Fatalf("restore error: %v", bad.RestoreErr)
	}
	if _, err := bad.LastGoodRepository.Open("root/root.roa"); err != nil {
		t.Fatalf("last-good snapshot should contain ROA: %v", err)
	}
	wm, ok, _ := w.store.LoadWatermark(ctx, w.h.root.Cert.StateKey())
	if !ok || wm.ManifestNumber.Int64() != 1 {
		t.Fatalf("watermark should remain at accepted value, got %+v", wm)
	}
}

func TestOSRepositoryOfflineValidation(t *testing.T) {
	w := newHappyWorld(t)
	root := filepath.Join(t.TempDir(), "repo")
	for name, content := range w.h.files {
		target := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(target, content, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	repo, err := NewOSRepository(root)
	if err != nil {
		t.Fatal(err)
	}
	v := NewValidator(repo, Options{Now: fixedNow(w.h.now), States: NewMemoryStateStore(), MaxDepth: 8})
	report, err := v.ValidateTAL(context.Background(), w.h.tal)
	if err != nil {
		t.Fatalf("OS repository validation: %v", err)
	}
	if !report.Valid {
		t.Fatalf("OS report invalid: %v", report.Err)
	}
}
