package netsec

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
)

// reopenVolume simulates a process restart by re-opening the durable store and
// rebuilding the encryptor and key ring from provisioned key material.
func reopenVolume(t *testing.T, ctx context.Context, path string, current KeyGen, hooks Hooks) (*Volume, *AESGCMEncryptor, *KeyRing) {
	t.Helper()
	store, err := OpenFileStore(path)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	enc, err := NewAESGCMEncryptor(genKey(0))
	if err != nil {
		t.Fatalf("new encryptor: %v", err)
	}
	for gen := KeyGen(1); gen <= current; gen++ {
		if err := enc.Rotate(gen, genKey(gen)); err != nil {
			t.Fatalf("provision gen %d: %v", gen, err)
		}
	}
	ring := NewKeyRing()
	for gen := KeyGen(1); gen <= current; gen++ {
		if err := ring.Rotate(gen); err != nil {
			t.Fatalf("rotate to %d: %v", gen, err)
		}
	}
	ring.OnDestroy = func(gen KeyGen) { enc.RemoveKey(gen) }
	vol, err := NewVolume(ctx, store, ring, enc, hooks)
	if err != nil {
		t.Fatalf("rebuild volume: %v", err)
	}
	return vol, enc, ring
}

// TestRestartAtEachMigrationPoint aborts a migration at distinct transaction
// points and restarts the host there, proving an uncommitted migration never
// leaves partial state and the resumed run converges without data loss.
func TestRestartAtEachMigrationPoint(t *testing.T) {
	ctx := context.Background()

	checkpoints := map[string]func(calls *int) Hooks{
		"before_commit": func(calls *int) Hooks {
			return Hooks{
				BeforeMigrateCommit: func(string, Record, Record) error {
					*calls++
					return errors.New("simulated crash before commit")
				},
			}
		},
		"after_read": func(calls *int) Hooks {
			return Hooks{
				AfterMigrateRead: func(string, Record) {
					*calls++
					if *calls == 1 {
						panic(ErrAborted)
					}
				},
			}
		},
	}

	for name, buildHooks := range checkpoints {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "store.json")

			// Seed a block under gen 0.
			seedVol, enc0, ring0 := newDeterministicVolume(t, ctx, mustOpenFileStore(t, path), Hooks{})
			ring0.OnDestroy = func(gen KeyGen) { enc0.RemoveKey(gen) }
			if _, err := seedVol.Write(ctx, "blk", []byte("durable")); err != nil {
				t.Fatalf("seed write: %v", err)
			}
			rotateDet(t, enc0, ring0)

			// Abort the migration at the checkpoint (a panic models a crash).
			calls := 0
			crashVol, _, _ := reopenVolume(t, ctx, path, 1, buildHooks(&calls))
			snapshotBefore := mustGetRecord(t, ctx, crashVol.Store(), "blk")
			func() {
				defer func() {
					if r := recover(); r != nil && r != ErrAborted {
						t.Fatalf("unexpected panic: %v", r)
					}
				}()
				_, _ = crashVol.MigrateBlock(ctx, "blk")
			}()

			// On-disk state must be untouched after the aborted transaction.
			snapshotAfter := mustGetRecord(t, ctx, crashVol.Store(), "blk")
			if snapshotAfter.Version != snapshotBefore.Version {
				t.Fatalf("aborted migration changed disk: before=%+v after=%+v",
					snapshotBefore.Version, snapshotAfter.Version)
			}

			// Restart fresh and complete the migration.
			vol, enc, ring := reopenVolume(t, ctx, path, 1, Hooks{})
			if _, err := vol.MigrateBlock(ctx, "blk"); err != nil {
				t.Fatalf("migrate after restart: %v", err)
			}
			got, err := vol.Read(ctx, "blk")
			if err != nil {
				t.Fatalf("read after restart: %v", err)
			}
			if string(got) != "durable" {
				t.Fatalf("content = %q, want durable", got)
			}
			if !ring.IsDestroyed(0) || enc.HasKey(0) {
				t.Fatal("old generation must be destroyed and its key removed")
			}
			if err := vol.VerifyContent(ctx); err != nil {
				t.Fatalf("verify: %v", err)
			}
		})
	}
}

// TestRestartBetweenBlocksMigratesPointByPoint runs a multi-block rotation and
// restarts after every committed block, verifying each completed transaction is
// durable and the resumed host continues from exactly the on-disk versions.
func TestRestartBetweenBlocksMigratesPointByPoint(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "store.json")

	seedVol, enc0, ring0 := newDeterministicVolume(t, ctx, mustOpenFileStore(t, path), Hooks{})
	ring0.OnDestroy = func(gen KeyGen) { enc0.RemoveKey(gen) }
	blocks := map[string]string{
		"b1": "content-1", "b2": "content-2", "b3": "content-3",
	}
	for id, body := range blocks {
		if _, err := seedVol.Write(ctx, id, []byte(body)); err != nil {
			t.Fatalf("seed %s: %v", id, err)
		}
	}
	rotateDet(t, enc0, ring0)

	// Migrate one block per host lifetime, restarting between each commit.
	for i := 0; i < len(blocks); i++ {
		vol, _, ring := reopenVolume(t, ctx, path, 1, Hooks{})
		recs, err := vol.Store().List(ctx)
		if err != nil {
			t.Fatalf("list pass %d: %v", i, err)
		}
		var target string
		for _, rec := range recs {
			if rec.Version.Gen != vol.CurrentGen() {
				target = rec.BlockID
				break
			}
		}
		if target == "" {
			t.Fatalf("pass %d found no old-generation block to migrate", i)
		}
		if _, err := vol.MigrateBlock(ctx, target); err != nil {
			t.Fatalf("migration pass %d: %v", i, err)
		}
		if err := vol.VerifyContent(ctx); err != nil {
			t.Fatalf("verify after pass %d: %v", i, err)
		}
		if i < len(blocks)-1 && ring.IsDestroyed(0) {
			t.Fatalf("pass %d destroyed old key with blocks still pending", i)
		}
	}

	// Final restart: everything is current and the old key stays gone.
	vol, enc, ring := reopenVolume(t, ctx, path, 1, Hooks{})
	if n, _ := vol.MigrateAll(ctx); n != 0 {
		t.Fatalf("final pass migrated %d, want 0 (all current)", n)
	}
	if !ring.IsDestroyed(0) || enc.HasKey(0) {
		t.Fatal("old generation must remain destroyed after restart")
	}
	for id, body := range blocks {
		got, err := vol.Read(ctx, id)
		if err != nil {
			t.Fatalf("read %s: %v", id, err)
		}
		if string(got) != body {
			t.Fatalf("block %s = %q, want %q", id, got, body)
		}
	}
}

// TestRestartPreservesConcurrentWrite replays the write/migration race across
// a restart: a block written under the new generation before a crash must not
// be rolled back or overwritten by a post-restart migration.
func TestRestartPreservesConcurrentWrite(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "store.json")

	seedVol, enc0, ring0 := newDeterministicVolume(t, ctx, mustOpenFileStore(t, path), Hooks{})
	if _, err := seedVol.Write(ctx, "blk", []byte("old")); err != nil {
		t.Fatalf("seed: %v", err)
	}
	rotateDet(t, enc0, ring0)
	if _, err := seedVol.Write(ctx, "blk", []byte("new")); err != nil {
		t.Fatalf("new-generation write: %v", err)
	}

	vol, _, _ := reopenVolume(t, ctx, path, 1, Hooks{})
	if n, _ := vol.MigrateAll(ctx); n != 0 {
		t.Fatalf("current block must be skipped, migrated=%d", n)
	}
	got, err := vol.Read(ctx, "blk")
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(got) != "new" {
		t.Fatalf("content = %q, restart/migration lost the new write", got)
	}
}

var ErrAborted = errors.New("netsec: simulated abort")

func mustOpenFileStore(t *testing.T, path string) *FileStore {
	t.Helper()
	store, err := OpenFileStore(path)
	if err != nil {
		t.Fatalf("open file store: %v", err)
	}
	return store
}

func mustGetRecord(t *testing.T, ctx context.Context, store Store, id string) Record {
	t.Helper()
	rec, err := store.Get(ctx, id)
	if err != nil {
		t.Fatalf("get %s: %v", id, err)
	}
	return rec
}
