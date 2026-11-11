package netsec

import (
	"context"
	"testing"
)

func TestWriteReadRoundTripAndVersionBinding(t *testing.T) {
	ctx := context.Background()
	vol, _, ring := newTestVolume(t, ctx, NewMemoryStore(), Hooks{})

	v1, err := vol.Write(ctx, "blk", []byte("payload"))
	if err != nil {
		t.Fatalf("write: %v", err)
	}
	if v1.Gen != 0 || v1.Seq != 1 {
		t.Fatalf("v1 = %+v, want gen0 seq1", v1)
	}
	if v1.Sum != digest([]byte("payload")) {
		t.Fatal("version must bind the content digest")
	}

	got, err := vol.Read(ctx, "blk")
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(got) != "payload" {
		t.Fatalf("read = %q, want payload", got)
	}

	// A rewrite under the same key keeps the digest but bumps the sequence.
	v2, err := vol.Write(ctx, "blk", []byte("payload"))
	if err != nil {
		t.Fatalf("rewrite: %v", err)
	}
	if v2.Seq != 2 || v2.Gen != v1.Gen || v2.Sum != v1.Sum {
		t.Fatalf("v2 = %+v, want seq2 same gen/digest", v2)
	}

	// Different content produces a different digest.
	v3, err := vol.Write(ctx, "blk", []byte("changed"))
	if err != nil {
		t.Fatalf("rewrite changed: %v", err)
	}
	if v3.Sum == v2.Sum || v3.Seq != 3 {
		t.Fatalf("v3 = %+v, want new digest and seq3", v3)
	}
	if ring.BlockCount(0) != 1 {
		t.Fatalf("gen0 block count = %d, want single logical block", ring.BlockCount(0))
	}
}

func TestReadDetectsTamperedCiphertext(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()
	vol, _, _ := newTestVolume(t, ctx, store, Hooks{})
	if _, err := vol.Write(ctx, "blk", []byte("payload")); err != nil {
		t.Fatalf("write: %v", err)
	}

	rec, err := store.Get(ctx, "blk")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	rec.Ciphertext[len(rec.Ciphertext)-1] ^= 0xFF
	store.records["blk"] = rec

	if _, err := vol.Read(ctx, "blk"); err == nil {
		t.Fatal("tampered ciphertext must fail authentication")
	}
}

func TestMigrateBlockReencryptsUnderNewGenerationPreservingContent(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()
	vol, enc, ring := newTestVolume(t, ctx, store, Hooks{})
	if _, err := vol.Write(ctx, "blk", []byte("secret")); err != nil {
		t.Fatalf("write: %v", err)
	}
	before, _ := store.Get(ctx, "blk")

	rotate(t, ctx, enc, ring)
	migrated, err := vol.MigrateBlock(ctx, "blk")
	if err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if migrated.Gen != 1 || migrated.Seq != before.Version.Seq+1 {
		t.Fatalf("migrated = %+v, want gen1 seq bump", migrated)
	}
	after, _ := store.Get(ctx, "blk")
	if after.Version.Sum != before.Version.Sum {
		t.Fatal("migration must preserve the content digest")
	}
	if string(after.Ciphertext) == string(before.Ciphertext) {
		t.Fatal("ciphertext must be re-sealed under the new key")
	}
	got, err := vol.Read(ctx, "blk")
	if err != nil {
		t.Fatalf("read after migrate: %v", err)
	}
	if string(got) != "secret" {
		t.Fatalf("content = %q, want secret", got)
	}
	if ring.BlockCount(0) != 0 || ring.BlockCount(1) != 1 {
		t.Fatalf("refs after migrate: gen0=%d gen1=%d", ring.BlockCount(0), ring.BlockCount(1))
	}
	if !ring.IsDestroyed(0) {
		t.Fatal("gen 0 must be marked destroyed once its last block moves away")
	}
}

func TestMigrateBlockNoopWhenAlreadyCurrent(t *testing.T) {
	ctx := context.Background()
	vol, enc, ring := newTestVolume(t, ctx, NewMemoryStore(), Hooks{})
	rotate(t, ctx, enc, ring)
	v, err := vol.Write(ctx, "blk", []byte("fresh"))
	if err != nil {
		t.Fatalf("write: %v", err)
	}
	got, err := vol.MigrateBlock(ctx, "blk")
	if err != nil {
		t.Fatalf("migrate noop: %v", err)
	}
	if got != v {
		t.Fatalf("migrate returned %+v, want unchanged %+v", got, v)
	}
}

func TestMigrationRereadsWhenConcurrentWriterWins(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()
	var vol *Volume
	conflicts := 0
	hooks := Hooks{
		// After the migration has read the old record and re-sealed it, a
		// foreground writer commits a brand-new version before the migration
		// reaches its compare-and-swap.
		BeforeMigrateCommit: func(blockID string, old, next Record) error {
			if _, err := vol.Write(ctx, blockID, []byte("newer-write")); err != nil {
				return err
			}
			return nil
		},
		OnMigrateConflict: func(blockID string, current Record) {
			conflicts++
		},
	}
	var enc *AESGCMEncryptor
	var ring *KeyRing
	vol, enc, ring = newTestVolume(t, ctx, store, hooks)
	if _, err := vol.Write(ctx, "blk", []byte("original")); err != nil {
		t.Fatalf("write: %v", err)
	}
	rotate(t, ctx, enc, ring)

	if _, err := vol.MigrateBlock(ctx, "blk"); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if conflicts != 1 {
		t.Fatalf("conflicts = %d, want exactly one re-read", conflicts)
	}
	got, err := vol.Read(ctx, "blk")
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(got) != "newer-write" {
		t.Fatalf("content = %q, migration overwrote the concurrent write", got)
	}
	rec, _ := store.Get(ctx, "blk")
	if rec.Version.Gen != 1 {
		t.Fatalf("final gen = %d, want current gen after re-read migration", rec.Version.Gen)
	}
	if rec.Version.Sum != digest([]byte("newer-write")) {
		t.Fatal("final version must bind the newer write's digest")
	}
	if err := vol.VerifyContent(ctx); err != nil {
		t.Fatalf("verify: %v", err)
	}
}

func TestMigrateAllDrivesOldKeyDestruction(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()
	vol, enc, ring := newTestVolume(t, ctx, store, Hooks{})
	ring.OnDestroy = func(gen KeyGen) { enc.RemoveKey(gen) }

	ids := []string{"a", "b", "c"}
	for _, id := range ids {
		if _, err := vol.Write(ctx, id, []byte("data-"+id)); err != nil {
			t.Fatalf("write %s: %v", id, err)
		}
	}
	rotate(t, ctx, enc, ring)
	n, err := vol.MigrateAll(ctx)
	if err != nil {
		t.Fatalf("migrate all: %v", err)
	}
	if n != len(ids) {
		t.Fatalf("migrated = %d, want %d", n, len(ids))
	}
	if !ring.IsDestroyed(0) {
		t.Fatal("gen 0 must be destroyed after all blocks migrated")
	}
	if enc.HasKey(0) {
		t.Fatal("gen 0 key material must be removed on destruction")
	}
	for _, id := range ids {
		got, err := vol.Read(ctx, id)
		if err != nil {
			t.Fatalf("read %s: %v", id, err)
		}
		if string(got) != "data-"+id {
			t.Fatalf("block %s content changed", id)
		}
	}
}
