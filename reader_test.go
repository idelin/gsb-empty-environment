package netsec

import (
	"context"
	"testing"
	"time"
)

// TestActiveReaderKeepsOldKeyAlive holds a read open while the background
// migration moves the last old-generation block away. The old key must not be
// zeroized until the reader releases its pin, even though no blocks remain.
func TestActiveReaderKeepsOldKeyAlive(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()

	releaseRead := make(chan struct{})
	insideRead := make(chan struct{})
	hooks := Hooks{
		AfterRead: func(blockID string, rec Record) {
			close(insideRead)
			<-releaseRead
		},
	}
	vol, enc, ring := newTestVolume(t, ctx, store, hooks)
	ring.OnDestroy = func(gen KeyGen) { enc.RemoveKey(gen) }

	if _, err := vol.Write(ctx, "blk", []byte("held")); err != nil {
		t.Fatalf("write: %v", err)
	}
	rotate(t, ctx, enc, ring)

	readDone := make(chan []byte, 1)
	go func() {
		plaintext, err := vol.Read(ctx, "blk")
		if err != nil {
			t.Errorf("read: %v", err)
		}
		readDone <- plaintext
	}()

	select {
	case <-insideRead:
	case <-time.After(5 * time.Second):
		t.Fatal("reader never reached its pinned checkpoint")
	}

	// Drain every old-generation block while the reader is still pinned.
	migDone := make(chan error, 1)
	go func() {
		_, err := vol.MigrateAll(ctx)
		migDone <- err
	}()
	select {
	case err := <-migDone:
		if err != nil {
			t.Fatalf("migrate all: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("migration deadlocked behind the active reader")
	}

	if ring.BlockCount(0) != 0 {
		t.Fatal("all gen-0 blocks should have migrated")
	}
	if ring.IsDestroyed(0) || !enc.HasKey(0) {
		t.Fatal("old key must survive while a reader remains pinned")
	}
	if ring.ReaderCount(0) < 1 {
		t.Fatal("expected at least one pinned reader")
	}

	close(releaseRead)
	select {
	case plaintext := <-readDone:
		if string(plaintext) != "held" {
			t.Fatalf("read content = %q, want held", plaintext)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("reader did not complete after release")
	}

	if !ring.IsDestroyed(0) {
		t.Fatal("old generation must destroy once the final reader releases")
	}
	if enc.HasKey(0) {
		t.Fatal("old key material must be removed after destruction")
	}
}
