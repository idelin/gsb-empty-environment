package netsec_test

import (
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	netsec "example.org/netsec/q0150"
)

func TestFailedLayerValidationPublishesNothing(t *testing.T) {
	store := newTensorStore()
	signer := &hmacSigner{key: []byte("test-key")}
	policy := netsec.SecurityPolicyFunc(func(netsec.Digest) bool { return true })
	manager := newManager(t, store, signer, policy, baseDigestA)

	firstQ := target(
		t, store, "attn.q", 1, 4, 3, netsec.DTypeFloat32,
		netsec.Rational{Numerator: 1, Denominator: 1},
		float32Bytes(1, 2, 3, 4),
		float32Bytes(1, 2, 3),
	)
	first, err := manager.Load(adapter(t, signer, "adapter-first", baseDigestA, firstQ))
	if err != nil {
		t.Fatalf("first Load: %v", err)
	}

	secondQ := target(
		t, store, "attn.q", 1, 4, 3, netsec.DTypeFloat32,
		netsec.Rational{Numerator: 2, Denominator: 1},
		float32Bytes(9, 8, 7, 6),
		float32Bytes(6, 5, 4),
	)
	badGate := target(
		t, store, "safety.gate", 1, 2, 2, netsec.DTypeFloat32,
		netsec.Rational{Numerator: 1, Denominator: 1},
		float32Bytes(1, 2),
		float32Bytes(3, 4),
	)
	store.set(badGate.BDigest, netsec.NewReadOnlyTensor(
		float32Bytes(99, 99),
		netsec.TensorShape{Rows: 2, Cols: 1},
		netsec.DTypeFloat32,
	))
	second := adapter(t, signer, "adapter-partial", baseDigestA, secondQ, badGate)
	if _, err := manager.Load(second); !errors.Is(err, netsec.ErrDigestMismatch) {
		t.Fatalf("partial Load error = %v, want ErrDigestMismatch", err)
	}

	current, ok := manager.Current()
	if !ok || current.ID != first.ID() || current.AdapterDigest != first.AdapterDigest() {
		t.Fatalf("current after failure = %+v, %v; want ID %d", current, ok, first.ID())
	}
	lease, err := manager.Acquire()
	if err != nil {
		t.Fatalf("Acquire after failure: %v", err)
	}
	defer lease.Release()
	if lease.Generation().ID() != first.ID() {
		t.Fatalf("acquired ID %d, want %d", lease.Generation().ID(), first.ID())
	}
}

func TestHotSwitchKeepsOldGenerationUntilLastLease(t *testing.T) {
	store := newTensorStore()
	signer := &hmacSigner{key: []byte("test-key")}
	manager := newManager(t, store, signer, nil, baseDigestA)

	firstTarget := target(
		t, store, "attn.q", 1, 4, 3, netsec.DTypeFloat32,
		netsec.Rational{Numerator: 1, Denominator: 1},
		float32Bytes(1, 1, 1, 1),
		float32Bytes(2, 2, 2),
	)
	first, err := manager.Load(adapter(t, signer, "adapter-first", baseDigestA, firstTarget))
	if err != nil {
		t.Fatalf("first Load: %v", err)
	}
	oldLease, err := manager.Acquire()
	if err != nil {
		t.Fatalf("Acquire first: %v", err)
	}

	secondTarget := target(
		t, store, "attn.q", 1, 4, 3, netsec.DTypeFloat32,
		netsec.Rational{Numerator: 1, Denominator: 1},
		float32Bytes(3, 3, 3, 3),
		float32Bytes(4, 4, 4),
	)
	second, err := manager.Load(adapter(t, signer, "adapter-second", baseDigestA, secondTarget))
	if err != nil {
		t.Fatalf("second Load: %v", err)
	}
	if first.ID() == second.ID() {
		t.Fatal("hot switch reused a generation ID")
	}

	newLease, err := manager.Acquire()
	if err != nil {
		t.Fatalf("Acquire second: %v", err)
	}
	defer newLease.Release()
	if newLease.Generation().ID() != second.ID() {
		t.Fatalf("new request got generation %d, want %d", newLease.Generation().ID(), second.ID())
	}
	if first.Zeroized() {
		t.Fatal("old generation was cleared while a request still held it")
	}
	oldLayer, _ := first.Layer("attn.q")
	if allZero(oldLayer.A()) {
		t.Fatal("old generation tensor was already zeroed")
	}

	oldLease.Release()
	if !first.Zeroized() {
		t.Fatal("old generation was not zeroed after its final reference")
	}
	if !allZero(oldLayer.A()) || !allZero(oldLayer.B()) {
		t.Fatal("old generation tensors were not cleared")
	}
	if second.Zeroized() {
		t.Fatal("new generation was cleared with the old generation")
	}
}

func TestRevokeBlocksNewRequestsAndClearsAfterLastLease(t *testing.T) {
	store := newTensorStore()
	signer := &hmacSigner{key: []byte("test-key")}
	manager := newManager(t, store, signer, nil, baseDigestA)
	target := target(
		t, store, "attn.q", 1, 4, 3, netsec.DTypeFloat32,
		netsec.Rational{Numerator: 1, Denominator: 1},
		float32Bytes(1, 2, 3, 4),
		float32Bytes(1, 2, 3),
	)
	envelope := adapter(t, signer, "adapter-revoke", baseDigestA, target)
	generation, err := manager.Load(envelope)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	lease, err := manager.Acquire()
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}

	if !manager.Revoke("adapter-revoke") {
		t.Fatal("first Revoke returned false")
	}
	if manager.Revoke("adapter-revoke") {
		t.Fatal("second Revoke returned true")
	}
	if _, err := manager.Acquire(); !errors.Is(err, netsec.ErrNoGeneration) {
		t.Fatalf("Acquire after Revoke error = %v, want ErrNoGeneration", err)
	}
	if _, err := manager.Load(envelope); !errors.Is(err, netsec.ErrRevoked) {
		t.Fatalf("Load after Revoke error = %v, want ErrRevoked", err)
	}
	if generation.Zeroized() {
		t.Fatal("revoked generation was cleared while a lease was active")
	}

	lease.Release()
	lease.Release()
	if !generation.Zeroized() {
		t.Fatal("revoked generation was not cleared after the final reference")
	}
}

func TestCallbacksOnlySeeCompleteGeneration(t *testing.T) {
	store := newTensorStore()
	signer := &hmacSigner{key: []byte("test-key")}
	var published atomic.Int64
	manager, err := netsec.New(netsec.Config{
		Base:                baseManifest(baseDigestA),
		Verifier:            signer,
		Tensors:             store,
		Policy:              netsec.SecurityPolicyFunc(func(netsec.Digest) bool { return true }),
		MaxRank:             4,
		AllowedDTypes:       []netsec.DType{netsec.DTypeFloat32},
		MaxScaleNumerator:   100,
		MaxScaleDenominator: 100,
		MaxTensorBytes:      1 << 20,
		MaxManifestBytes:    1 << 20,
		PublishHook: func(generation *netsec.Generation) {
			if generation.LayerCount() != 1 {
				t.Errorf("hook saw incomplete generation with %d layers", generation.LayerCount())
			}
			layer, ok := generation.Layer("attn.q")
			if !ok || len(layer.A()) == 0 || len(layer.B()) == 0 {
				t.Errorf("hook saw incomplete tensor data")
			}
			published.Add(1)
		},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = manager.Close() })

	called := false
	if err := manager.WithGeneration(func(*netsec.Generation) error {
		called = true
		return nil
	}); !errors.Is(err, netsec.ErrNoGeneration) {
		t.Fatalf("WithGeneration before load error = %v, want ErrNoGeneration", err)
	}
	if called {
		t.Fatal("output callback ran without a generation")
	}

	valid := target(
		t, store, "attn.q", 1, 4, 3, netsec.DTypeFloat32,
		netsec.Rational{Numerator: 1, Denominator: 1},
		float32Bytes(1, 2, 3, 4),
		float32Bytes(1, 2, 3),
	)
	if _, err := manager.Load(adapter(t, signer, "adapter-callback", baseDigestA, valid)); err != nil {
		t.Fatalf("Load: %v", err)
	}
	if published.Load() != 1 {
		t.Fatalf("publish hook count = %d, want 1", published.Load())
	}

	invalid := valid
	invalid.ADigest = "not-present"
	if _, err := manager.Load(adapter(t, signer, "adapter-invalid-callback", baseDigestA, invalid)); !errors.Is(err, netsec.ErrTensorRead) {
		t.Fatalf("invalid Load error = %v, want ErrTensorRead", err)
	}
	if published.Load() != 1 {
		t.Fatalf("publish hook ran for a failed admission: count = %d", published.Load())
	}

	err = manager.WithGeneration(func(generation *netsec.Generation) error {
		called = true
		if generation.LayerCount() != 1 {
			return fmt.Errorf("incomplete generation")
		}
		layer, ok := generation.Layer("attn.q")
		if !ok || len(layer.A()) == 0 || len(layer.B()) == 0 {
			return fmt.Errorf("missing tensors")
		}
		return nil
	})
	if err != nil {
		t.Fatalf("WithGeneration: %v", err)
	}
	if !called {
		t.Fatal("output callback did not run for a complete generation")
	}
}

func TestConcurrentHotSwitchNeverExposesPartialOrZeroedGeneration(t *testing.T) {
	store := newTensorStore()
	signer := &hmacSigner{key: []byte("test-key")}
	var (
		hookMu  sync.Mutex
		hookIDs []uint64
	)
	manager, err := netsec.New(netsec.Config{
		Base:                baseManifest(baseDigestA),
		Verifier:            signer,
		Tensors:             store,
		Policy:              netsec.SecurityPolicyFunc(func(netsec.Digest) bool { return true }),
		MaxRank:             4,
		AllowedDTypes:       []netsec.DType{netsec.DTypeFloat32},
		MaxScaleNumerator:   100,
		MaxScaleDenominator: 100,
		MaxTensorBytes:      1 << 20,
		MaxManifestBytes:    1 << 20,
		PublishHook: func(generation *netsec.Generation) {
			hookMu.Lock()
			hookIDs = append(hookIDs, generation.ID())
			hookMu.Unlock()
			if generation.LayerCount() != 2 {
				t.Errorf("hook saw %d layers, want 2", generation.LayerCount())
			}
			for _, layerID := range []string{"attn.q", "safety.gate"} {
				layer, ok := generation.Layer(layerID)
				if !ok || len(layer.A()) == 0 || len(layer.B()) == 0 {
					t.Errorf("hook saw incomplete layer %q", layerID)
				}
			}
		},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = manager.Close() })

	stop := make(chan struct{})
	errCh := make(chan error, 32)
	started := make(chan struct{})
	var startOnce sync.Once
	var wg sync.WaitGroup
	for worker := 0; worker < 8; worker++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			startOnce.Do(func() { close(started) })
			for {
				select {
				case <-stop:
					return
				default:
				}
				err := manager.WithGeneration(func(generation *netsec.Generation) error {
					if generation.LayerCount() != 2 {
						return fmt.Errorf("generation %d has %d layers", generation.ID(), generation.LayerCount())
					}
					for _, layerID := range []string{"attn.q", "safety.gate"} {
						layer, ok := generation.Layer(layerID)
						if !ok {
							return fmt.Errorf("generation %d missing %s", generation.ID(), layerID)
						}
						a, b := layer.Tensors()
						if len(a) == 0 || len(b) == 0 {
							return fmt.Errorf("generation %d has empty tensors", generation.ID())
						}
						if allZero(a) || allZero(b) {
							return fmt.Errorf("generation %d exposed zeroed tensors", generation.ID())
						}
					}
					return nil
				})
				if err != nil && !errors.Is(err, netsec.ErrNoGeneration) {
					select {
					case errCh <- err:
					default:
					}
					return
				}
			}
		}()
	}

	loadOne := func(index int) {
		t.Helper()
		q := target(
			t, store, "attn.q", 1, 4, 3, netsec.DTypeFloat32,
			netsec.Rational{Numerator: 1, Denominator: 1},
			float32Bytes(float32(index+1), 2, 3, 4),
			float32Bytes(float32(index+1), 2, 3),
		)
		gate := target(
			t, store, "safety.gate", 1, 2, 2, netsec.DTypeFloat32,
			netsec.Rational{Numerator: 1, Denominator: 1},
			float32Bytes(float32(index+1), 2),
			float32Bytes(float32(index+1), float32(index+3)),
		)
		envelope := adapter(t, signer, netsec.Digest(fmt.Sprintf("adapter-%d", index)), baseDigestA, q, gate)
		if _, err := manager.Load(envelope); err != nil {
			t.Fatalf("Load %d: %v", index, err)
		}
	}

	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("request workers did not start")
	}

	const generations = 30
	for i := 0; i < generations; i++ {
		loadOne(i)
	}

	bad := netsec.AdapterTarget{
		LayerID: "attn.q",
		ADigest: "missing-a",
		BDigest: "missing-b",
		DType:   netsec.DTypeFloat32,
		Scale:   netsec.Rational{Numerator: 1, Denominator: 1},
		Rank:    1,
	}
	if _, err := manager.Load(adapter(t, signer, "adapter-concurrent-invalid", baseDigestA, bad)); !errors.Is(err, netsec.ErrTensorRead) {
		t.Fatalf("invalid concurrent Load error = %v, want ErrTensorRead", err)
	}

	close(stop)
	wg.Wait()
	close(errCh)
	for err := range errCh {
		t.Error(err)
	}

	hookMu.Lock()
	defer hookMu.Unlock()
	if len(hookIDs) != generations {
		t.Fatalf("publish hook count = %d, want %d", len(hookIDs), generations)
	}
	for i := 1; i < len(hookIDs); i++ {
		if hookIDs[i] <= hookIDs[i-1] {
			t.Fatalf("publish hook IDs not monotonic: %v", hookIDs)
		}
	}
}
