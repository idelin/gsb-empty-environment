package netsec

import (
	"context"
	"testing"
	"time"
)

// TestPausingEncryptorMigrationDoesNotOverwriteNewWrite holds the background
// migration at its sealing step, lets a foreground write commit a new version,
// and then releases the migration. The migration's compare-and-swap must fail
// against the newer version and the migration must re-read instead of
// overwriting the fresh data.
func TestPausingEncryptorMigrationDoesNotOverwriteNewWrite(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()

	aes, ring := mustSeal(t)
	pausing := NewPausingEncryptor(aes)
	vol, err := NewVolume(ctx, store, ring, pausing, Hooks{})
	if err != nil {
		t.Fatalf("new volume: %v", err)
	}
	if _, err := vol.Write(ctx, "blk", []byte("original")); err != nil {
		t.Fatalf("write original: %v", err)
	}
	next := rotate(t, ctx, pausing, ring)

	// Stall the single sealing call of the migration transaction.
	pausing.PauseOnce(next)
	migErr := make(chan error, 1)
	go func() {
		_, e := vol.MigrateBlock(ctx, "blk")
		migErr <- e
	}()

	waitCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := pausing.WaitBlocked(waitCtx, next, 1); err != nil {
		t.Fatalf("migration never reached the sealing stall: %v", err)
	}

	// While the migration is frozen mid-transaction, a foreground write seals
	// under the same current generation and commits a newer version.
	newVersion, err := vol.Write(ctx, "blk", []byte("fresh-write"))
	if err != nil {
		t.Fatalf("foreground write while migration stalled: %v", err)
	}
	rec, _ := store.Get(ctx, "blk")
	if rec.Version != newVersion || string(rec.Ciphertext) == "" {
		t.Fatal("foreground write must be durably committed before resume")
	}

	// Release the held migration sealing and let the transaction finish.
	pausing.Resume()
	select {
	case err := <-migErr:
		if err != nil {
			t.Fatalf("migration after resume: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("migration did not complete after resume")
	}

	// The fresh write must survive: the migration re-read rather than
	// overwrote it, then re-sealed the fresh content under the current gen.
	got, err := vol.Read(ctx, "blk")
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(got) != "fresh-write" {
		t.Fatalf("content = %q, migration overwrote the foreground write", got)
	}
	final, _ := store.Get(ctx, "blk")
	if final.Version.Sum != digest([]byte("fresh-write")) {
		t.Fatal("final version must bind the fresh write digest")
	}
	// On re-read the block is already current, so the migration must keep the
	// foreground write's exact version rather than minting a competing one.
	if final.Version != newVersion {
		t.Fatalf("final version = %+v, must equal foreground write version %+v",
			final.Version, newVersion)
	}
	if err := vol.VerifyContent(ctx); err != nil {
		t.Fatalf("verify: %v", err)
	}
}

// TestPausingEncryptorBlocksOnlyTargetGeneration confirms the gate is scoped
// to one generation: decryption and other generations keep working.
func TestPausingEncryptorBlocksOnlyTargetGeneration(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	aes, _ := mustSeal(t)
	addKey(t, aes, 1)
	pausing := NewPausingEncryptor(aes)

	sealed0, err := pausing.Encrypt(ctx, 0, []byte("x"))
	if err != nil {
		t.Fatalf("baseline encrypt: %v", err)
	}
	if _, err := pausing.Decrypt(ctx, 0, sealed0); err != nil {
		t.Fatalf("decrypt must never be paused: %v", err)
	}

	pausing.Pause(1)
	done := make(chan struct{})
	go func() {
		_, _ = pausing.Encrypt(ctx, 1, []byte("y"))
		close(done)
	}()
	if err := pausing.WaitBlocked(ctx, 1, 1); err != nil {
		t.Fatalf("wait blocked: %v", err)
	}

	// Gen 0 encryption remains responsive while gen 1 is held.
	if _, err := pausing.Encrypt(ctx, 0, []byte("z")); err != nil {
		t.Fatalf("other generation must not be gated: %v", err)
	}
	select {
	case <-done:
		t.Fatal("gated encryption returned before resume")
	default:
	}

	pausing.Resume()
	select {
	case <-done:
	case <-ctx.Done():
		t.Fatal("gated encryption did not return after resume")
	}
}

// TestPausingEncryptorOneShotFiresExactlyOnce verifies the one-shot gate
// clears after a single blocked call so later seals proceed.
func TestPausingEncryptorOneShotFiresExactlyOnce(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	aes, _ := mustSeal(t)
	pausing := NewPausingEncryptor(aes)
	pausing.PauseOnce(0)

	done := make(chan struct{})
	go func() {
		_, _ = pausing.Encrypt(ctx, 0, []byte("first"))
		close(done)
	}()
	if err := pausing.WaitBlocked(ctx, 0, 1); err != nil {
		t.Fatalf("wait blocked: %v", err)
	}
	pausing.Resume()
	<-done

	// The gate must have cleared: a second seal completes immediately.
	finished := make(chan struct{})
	go func() {
		_, _ = pausing.Encrypt(ctx, 0, []byte("second"))
		close(finished)
	}()
	select {
	case <-finished:
	case <-time.After(time.Second):
		t.Fatal("one-shot gate did not clear after firing")
	}
}
