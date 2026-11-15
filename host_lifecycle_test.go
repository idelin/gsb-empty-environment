package netsec

import (
	"bytes"
	"errors"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
)

func TestRootRotationGenerationIsolation(t *testing.T) {
	obs := &recordingObserver{}
	root1 := testRoot(0x05)
	host, err := NewHost(root1, WithCryptoObserver(obs.callback()))
	if err != nil {
		t.Fatalf("NewHost failed: %v", err)
	}
	ctx := validContext()

	gen1Handle, err := host.Derive(ctx, 32)
	if err != nil {
		t.Fatalf("generation 1 Derive failed: %v", err)
	}
	if gen1Handle.Generation() != 1 {
		t.Fatalf("first handle generation = %d, want 1", gen1Handle.Generation())
	}

	root2 := testRoot(0x06)
	if err := host.RotateRoot(root2); err != nil {
		t.Fatalf("RotateRoot failed: %v", err)
	}
	if host.Generation() != 2 {
		t.Fatalf("generation after rotation = %d, want 2", host.Generation())
	}
	if err := host.RotateRoot(nil); err != errEmptyRoot {
		t.Fatalf("RotateRoot(nil) error = %v, want %v", err, errEmptyRoot)
	}

	gen2Handle, err := host.Derive(ctx, 32)
	if err != nil {
		t.Fatalf("generation 2 Derive failed: %v", err)
	}
	if gen2Handle.Generation() != 2 {
		t.Fatalf("second handle generation = %d, want 2", gen2Handle.Generation())
	}

	key1 := handleKey(t, gen1Handle)
	key2 := handleKey(t, gen2Handle)
	if bytes.Equal(key1, key2) {
		t.Fatalf("root rotation did not change derived key material")
	}
	if want := expectedKey(t, root2, ctx, 32); !bytes.Equal(key2, want) {
		t.Fatalf("generation 2 key does not match reference HKDF under new root")
	}
	if want := expectedKey(t, root1, ctx, 32); !bytes.Equal(key1, want) {
		t.Fatalf("generation 1 key no longer matches reference HKDF under old root")
	}

	// A further rotation makes generation 3 run fresh cryptography, while the
	// generation prefix on the cache key keeps all generations separate.
	before := obs.count()
	if err := host.RotateRoot(testRoot(0x07)); err != nil {
		t.Fatalf("second RotateRoot failed: %v", err)
	}
	gen3Handle, err := host.Derive(ctx, 32)
	if err != nil {
		t.Fatalf("Derive under generation 3 failed: %v", err)
	}
	if gen3Handle.Generation() != 3 {
		t.Fatalf("third handle generation = %d, want 3", gen3Handle.Generation())
	}
	if got := obs.count() - before; got != 2 {
		t.Fatalf("generation 3 derivation events = %d, want 2 (extract+expand)", got)
	}

	generations := map[uint64]bool{}
	for _, e := range obs.snapshot() {
		generations[e.generation] = true
	}
	if !generations[1] || !generations[2] || !generations[3] {
		t.Fatalf("observer missing generations: %v", generations)
	}

	// Re-deriving under the current generation is a cache hit and emits
	// nothing new even though rotation happened in between.
	cachedBefore := obs.count()
	cached, err := host.Derive(ctx, 32)
	if err != nil {
		t.Fatalf("cached Derive failed: %v", err)
	}
	if obs.count() != cachedBefore {
		t.Fatalf("cache hit emitted new crypto events")
	}
	_ = handleKey(t, cached)
}

func TestRevocationStopsNewDerivation(t *testing.T) {
	obs := &recordingObserver{}
	host, err := NewHost(testRoot(0x08), WithCryptoObserver(obs.callback()))
	if err != nil {
		t.Fatalf("NewHost failed: %v", err)
	}
	ctx := validContext()

	outstanding, err := host.Derive(ctx, 32)
	if err != nil {
		t.Fatalf("Derive before revoke failed: %v", err)
	}

	host.Revoke()
	if _, err := host.Derive(ctx, 32); !errors.Is(err, errRootRevoked) {
		t.Fatalf("post-revoke Derive error = %v, want %v", err, errRootRevoked)
	}
	if err := host.RotateRoot(testRoot(0x09)); !errors.Is(err, errRootRevoked) {
		t.Fatalf("post-revoke RotateRoot error = %v, want %v", err, errRootRevoked)
	}

	// Revocation is idempotent and the outstanding handle stays valid for its
	// single use, producing the same key it would have pre-revocation.
	host.Revoke()
	want := expectedKey(t, testRoot(0x08), ctx, 32)
	if got := handleKey(t, outstanding); !bytes.Equal(got, want) {
		t.Fatalf("outstanding handle changed after revocation")
	}

	// No cryptographic events may be produced by the failed operations.
	if got := obs.count(); got != 2 {
		t.Fatalf("crypto event count after failed ops = %d, want 2 (initial derive)", got)
	}
}

// TestRevocationRace drives revocation against concurrent derivation. The
// host's locking guarantees that every outcome is one of two safe states:
// Derive fails with errRootRevoked without cryptography, or it returns a
// usable one-time handle whose single Use succeeds.
func TestRevocationRace(t *testing.T) {
	for attempt := 0; attempt < 200; attempt++ {
		obs := &recordingObserver{}
		host, err := NewHost(testRoot(byte(attempt)), WithCryptoObserver(obs.callback()))
		if err != nil {
			t.Fatalf("NewHost failed: %v", err)
		}
		ctx := validContext()

		started := make(chan struct{})
		release := make(chan struct{})
		var wg sync.WaitGroup
		var deriveErr error
		var handle *Handle

		wg.Add(1)
		go func() {
			defer wg.Done()
			close(started)
			<-release
			handle, deriveErr = host.Derive(ctx, 32)
		}()

		<-started
		runtime.Gosched()
		close(release)
		host.Revoke()
		wg.Wait()

		switch {
		case errors.Is(deriveErr, errRootRevoked):
			if handle != nil {
				t.Fatalf("revoked derivation returned both an error and a handle")
			}
		case deriveErr == nil:
			if handle == nil {
				t.Fatalf("successful derivation returned no handle")
			}
			if err := handle.Use(func(key []byte) error {
				if len(key) != 32 {
					t.Fatalf("derived key length = %d, want 32", len(key))
				}
				return nil
			}); err != nil {
				t.Fatalf("handle won by the race must remain usable: %v", err)
			}
		default:
			t.Fatalf("unexpected derivation error: %v", deriveErr)
		}

		// Every observed event must correspond to a successful derivation.
		for _, e := range obs.snapshot() {
			if e.op != CryptoExtract && e.op != CryptoExpand {
				t.Fatalf("unexpected observed op %q", e.op)
			}
		}
	}
}

func TestHandleOneTimeUseAndZeroization(t *testing.T) {
	host, err := NewHost(testRoot(0x0a))
	if err != nil {
		t.Fatalf("NewHost failed: %v", err)
	}
	handle, err := host.Derive(validContext(), 32)
	if err != nil {
		t.Fatalf("Derive failed: %v", err)
	}

	if err := handle.Use(nil); err != errNilUse {
		t.Fatalf("Use(nil) error = %v, want %v", err, errNilUse)
	}

	sentinel := errors.New("callback failure")
	var sawKey []byte
	err = handle.Use(func(key []byte) error {
		sawKey = append([]byte(nil), key...)
		return sentinel
	})
	if !errors.Is(err, sentinel) {
		t.Fatalf("callback error not propagated: got %v, want %v", err, sentinel)
	}
	if len(sawKey) != 32 {
		t.Fatalf("callback saw key length %d, want 32", len(sawKey))
	}

	// The handle's internal copy must be zeroized after the first use.
	handle.mu.Lock()
	for _, b := range handle.key {
		if b != 0 {
			handle.mu.Unlock()
			t.Fatalf("derived key material was not zeroized after Use")
		}
	}
	handle.mu.Unlock()

	// Every subsequent use fails without invoking a callback.
	called := atomic.Bool{}
	err = handle.Use(func(key []byte) error {
		called.Store(true)
		return nil
	})
	if err != errHandleSpent {
		t.Fatalf("second Use error = %v, want %v", err, errHandleSpent)
	}
	if called.Load() {
		t.Fatalf("spent handle invoked its callback")
	}
}

// TestConcurrentHandleUse proves only one of many racing Use calls ever runs.
func TestConcurrentHandleUse(t *testing.T) {
	host, err := NewHost(testRoot(0x0b))
	if err != nil {
		t.Fatalf("NewHost failed: %v", err)
	}
	handle, err := host.Derive(validContext(), 32)
	if err != nil {
		t.Fatalf("Derive failed: %v", err)
	}

	var invocations atomic.Int32
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			err := handle.Use(func(key []byte) error {
				invocations.Add(1)
				return nil
			})
			if err != nil && err != errHandleSpent {
				t.Errorf("unexpected Use error: %v", err)
			}
		}()
	}
	close(start)
	wg.Wait()

	if invocations.Load() != 1 {
		t.Fatalf("callback invocations = %d, want exactly 1", invocations.Load())
	}
}

// TestHandleContextIsCopy ensures callers cannot mutate host-owned context
// through the value returned by Context().
func TestHandleContextIsCopy(t *testing.T) {
	host, err := NewHost(testRoot(0x0c))
	if err != nil {
		t.Fatalf("NewHost failed: %v", err)
	}
	ctx := validContext()
	handle, err := host.Derive(ctx, 32)
	if err != nil {
		t.Fatalf("Derive failed: %v", err)
	}

	exposed := handle.Context()
	exposed.Tenant[0] = 'Z'
	exposed.ProtocolVersion[0] = 'Z'

	again := handle.Context()
	if !bytes.Equal(again.Tenant, ctx.Tenant) {
		t.Fatalf("handle context tenant was mutated through the returned copy")
	}
	if !bytes.Equal(again.ProtocolVersion, ctx.ProtocolVersion) {
		t.Fatalf("handle context version was mutated through the returned copy")
	}
}
