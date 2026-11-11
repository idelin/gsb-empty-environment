package netsec

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestKeyRingRotationRetiresOldGeneration(t *testing.T) {
	ring := NewKeyRing()
	if ring.Current() != 0 {
		t.Fatalf("initial current = %d, want 0", ring.Current())
	}
	if err := ring.Rotate(1); err != nil {
		t.Fatalf("rotate: %v", err)
	}
	if ring.Current() != 1 {
		t.Fatalf("current = %d, want 1", ring.Current())
	}
	if !ring.IsRetired(0) {
		t.Fatal("gen 0 should be retired after rotation")
	}
	if ring.IsDestroyed(0) {
		t.Fatal("gen 0 must not be destroyed while references may remain")
	}
	if err := ring.Rotate(1); err == nil {
		t.Fatal("reusing a generation must fail")
	}
}

func TestOldKeyKeptUntilBlocksAndReadersDrain(t *testing.T) {
	ring := NewKeyRing()
	destroyed := make(chan KeyGen, 4)
	ring.OnDestroy = func(gen KeyGen) { destroyed <- gen }

	ring.AddBlock(0)
	ring.AddBlock(0)
	if err := ring.PinReader(0); err != nil {
		t.Fatalf("pin: %v", err)
	}
	if err := ring.Rotate(1); err != nil {
		t.Fatalf("rotate: %v", err)
	}

	// Migrate one block away: a reader is still pinned and one block remains.
	ring.MoveBlock(0, 1)
	if ring.IsDestroyed(0) {
		t.Fatal("gen 0 destroyed with reader and block still present")
	}

	// Migrate the last block: the reader still holds the generation.
	ring.MoveBlock(0, 1)
	if ring.IsDestroyed(0) {
		t.Fatal("gen 0 destroyed with active reader still present")
	}

	// Release the final reader: now destruction is legal.
	ring.ReleaseReader(0)
	select {
	case gen := <-destroyed:
		if gen != 0 {
			t.Fatalf("destroyed gen = %d, want 0", gen)
		}
	default:
		t.Fatal("gen 0 should be destroyed once blocks and readers drain")
	}
	if !ring.IsDestroyed(0) {
		t.Fatal("gen 0 should report destroyed")
	}
	if err := ring.PinReader(0); !errors.Is(err, ErrKeyDestroyed) {
		t.Fatalf("pin after destroy err = %v, want ErrKeyDestroyed", err)
	}
}

func TestRetiredGenerationWithNoReferencesDestroyedImmediately(t *testing.T) {
	ring := NewKeyRing()
	destroyed := make(chan KeyGen, 4)
	ring.OnDestroy = func(gen KeyGen) { destroyed <- gen }

	ring.AddBlock(0)
	if err := ring.Rotate(1); err != nil {
		t.Fatalf("rotate: %v", err)
	}
	ring.MoveBlock(0, 1)

	select {
	case gen := <-destroyed:
		if gen != 0 {
			t.Fatalf("destroyed gen = %d, want 0", gen)
		}
	default:
		t.Fatal("retired gen with no references must be destroyed immediately")
	}
}

func TestWaitDestroyedReturnsWhenKeyZeroized(t *testing.T) {
	ctx := context.Background()
	ring := NewKeyRing()
	ring.AddBlock(0)
	if err := ring.Rotate(1); err != nil {
		t.Fatalf("rotate: %v", err)
	}

	waitErr := make(chan error, 1)
	go func() { waitErr <- ring.WaitDestroyed(ctx, 0) }()

	// Still referenced, so the waiter must not return yet.
	select {
	case err := <-waitErr:
		t.Fatalf("WaitDestroyed returned early with err=%v", err)
	default:
	}

	ring.MoveBlock(0, 1)
	select {
	case err := <-waitErr:
		if err != nil {
			t.Fatalf("WaitDestroyed: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("WaitDestroyed did not return after destruction")
	}

	// Cancellation must also unblock a waiter.
	cctx, cancel := context.WithCancel(ctx)
	cancelled := make(chan error, 1)
	go func() { cancelled <- ring.WaitDestroyed(cctx, 1) }()
	cancel()
	select {
	case err := <-cancelled:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("err = %v, want context.Canceled", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("cancelled WaitDestroyed did not return")
	}
}

func TestReconcileRebuildsReferencesAfterRestart(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()

	// Simulate blocks left on disk under two generations.
	vol, enc, ring := newDeterministicVolume(t, ctx, store, Hooks{})
	if _, err := vol.Write(ctx, "b0", []byte("zero")); err != nil {
		t.Fatalf("write b0: %v", err)
	}
	rotateDet(t, enc, ring)
	if _, err := vol.Write(ctx, "b1", []byte("one")); err != nil {
		t.Fatalf("write b1: %v", err)
	}

	// Fresh ring + encryptor reproduce a process restart.
	enc2, err := NewAESGCMEncryptor(genKey(0))
	if err != nil {
		t.Fatalf("rebuild encryptor: %v", err)
	}
	if err := enc2.Rotate(1, genKey(1)); err != nil {
		t.Fatalf("provision gen1: %v", err)
	}
	ring2 := NewKeyRing()
	if err := ring2.Rotate(1); err != nil {
		t.Fatalf("rotate: %v", err)
	}
	vol2, err := NewVolume(ctx, store, ring2, enc2, Hooks{})
	if err != nil {
		t.Fatalf("reopen volume: %v", err)
	}
	if got := ring2.BlockCount(0); got != 1 {
		t.Fatalf("gen0 blocks after reconcile = %d, want 1", got)
	}
	if got := ring2.BlockCount(1); got != 1 {
		t.Fatalf("gen1 blocks after reconcile = %d, want 1", got)
	}
	if got := ring2.ReaderCount(0); got != 0 {
		t.Fatalf("readers after restart = %d, want 0", got)
	}
	if err := vol2.VerifyContent(ctx); err != nil {
		t.Fatalf("content verification after restart: %v", err)
	}

	// Migrating the last gen-0 block away must allow its destruction.
	if _, err := vol2.MigrateBlock(ctx, "b0"); err != nil {
		t.Fatalf("migrate b0: %v", err)
	}
	if !ring2.IsDestroyed(0) {
		t.Fatal("gen 0 should destroy after the final migrated block on restart")
	}
}
