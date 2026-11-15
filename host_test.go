package netsec

import (
	"bytes"
	"sync"
	"testing"
)

type recordedEvent struct {
	op         CryptoOp
	generation uint64
	context    Context
}

type recordingObserver struct {
	mu     sync.Mutex
	events []recordedEvent
}

func (r *recordingObserver) callback() CryptoObserver {
	return func(e CryptoEvent) {
		r.mu.Lock()
		defer r.mu.Unlock()
		r.events = append(r.events, recordedEvent{
			op:         e.Op,
			generation: e.Generation,
			context:    cloneContext(e.Context),
		})
	}
}

func (r *recordingObserver) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.events)
}

func (r *recordingObserver) snapshot() []recordedEvent {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]recordedEvent(nil), r.events...)
}

func testRoot(seed byte) []byte {
	root := make([]byte, 32)
	for i := range root {
		root[i] = seed
	}
	return root
}

// handleKey consumes the one-time handle and returns the key the callback saw.
func handleKey(t *testing.T, h *Handle) []byte {
	t.Helper()
	var captured []byte
	err := h.Use(func(key []byte) error {
		captured = append([]byte(nil), key...)
		return nil
	})
	if err != nil {
		t.Fatalf("handle Use failed: %v", err)
	}
	return captured
}

func expectedKey(t *testing.T, root []byte, ctx Context, length int) []byte {
	t.Helper()
	key, err := DeriveKey(nil, root, ctx.canonicalInfo(), length)
	if err != nil {
		t.Fatalf("reference DeriveKey failed: %v", err)
	}
	return key
}

func TestNewHostRejectsEmptyRoot(t *testing.T) {
	if _, err := NewHost(nil); err != errEmptyRoot {
		t.Fatalf("NewHost(nil) error = %v, want %v", err, errEmptyRoot)
	}
	if _, err := NewHost([]byte{}); err != errEmptyRoot {
		t.Fatalf("NewHost(empty) error = %v, want %v", err, errEmptyRoot)
	}
}

func TestRootKeyIsolation(t *testing.T) {
	root := testRoot(0x42)
	obs := &recordingObserver{}
	host, err := NewHost(root, WithCryptoObserver(obs.callback()))
	if err != nil {
		t.Fatalf("NewHost failed: %v", err)
	}

	// Mutating the caller's slice must not change derived material.
	for i := range root {
		root[i] = 0xff
	}
	ctx := validContext()
	handle, err := host.Derive(ctx, 32)
	if err != nil {
		t.Fatalf("Derive failed: %v", err)
	}
	if got, want := handleKey(t, handle), expectedKey(t, testRoot(0x42), ctx, 32); !bytes.Equal(got, want) {
		t.Fatalf("derived key drifted after caller mutated root slice")
	}
}

func TestInvalidContextTriggersNoCrypto(t *testing.T) {
	invalidContexts := []Context{
		{Tenant: nil, Purpose: PurposeEncrypt, ProtocolVersion: []byte("v1"), Direction: DirectionSend, Epoch: 1},
		{Tenant: make([]byte, MaxTenantLength+1), Purpose: PurposeEncrypt, ProtocolVersion: nil, Direction: DirectionSend, Epoch: 1},
		{Tenant: []byte("t"), Purpose: Purpose(7), ProtocolVersion: nil, Direction: DirectionSend, Epoch: 1},
		{Tenant: []byte("t"), Purpose: PurposeEncrypt, ProtocolVersion: nil, Direction: Direction(7), Epoch: 1},
		{Tenant: []byte("t"), Purpose: PurposeEncrypt, ProtocolVersion: make([]byte, MaxProtocolVersionLength+1), Direction: DirectionSend, Epoch: 1},
	}

	obs := &recordingObserver{}
	host, err := NewHost(testRoot(0x01), WithCryptoObserver(obs.callback()))
	if err != nil {
		t.Fatalf("NewHost failed: %v", err)
	}

	for i, ctx := range invalidContexts {
		if _, err := host.Derive(ctx, 32); err == nil {
			t.Fatalf("invalid context %d was accepted", i)
		}
	}
	if _, err := host.Derive(validContext(), 255*sha256Size+1); err != errHKDFLength {
		t.Fatalf("overlong expansion error = %v, want %v", err, errHKDFLength)
	}

	// A revoked host must also fail closed without touching cryptography.
	host.Revoke()
	before := obs.count()
	if _, err := host.Derive(validContext(), 32); err != errRootRevoked {
		t.Fatalf("derivation after revoke error = %v, want %v", err, errRootRevoked)
	}
	if obs.count() != before {
		t.Fatalf("revoked derivation emitted crypto events, want 0")
	}
	if obs.count() != 0 {
		t.Fatalf("invalid/revoked contexts triggered %d crypto operations, want 0", obs.count())
	}
}

func TestFieldBoundaryCollisionRejectedByDerivation(t *testing.T) {
	obs := &recordingObserver{}
	host, err := NewHost(testRoot(0x02), WithCryptoObserver(obs.callback()))
	if err != nil {
		t.Fatalf("NewHost failed: %v", err)
	}

	first := validContext()
	first.Tenant = []byte("ab")
	first.ProtocolVersion = []byte("c")
	second := validContext()
	second.Tenant = []byte("a")
	second.ProtocolVersion = []byte("bc")

	h1, err := host.Derive(first, 32)
	if err != nil {
		t.Fatalf("Derive first failed: %v", err)
	}
	h2, err := host.Derive(second, 32)
	if err != nil {
		t.Fatalf("Derive second failed: %v", err)
	}
	if bytes.Equal(handleKey(t, h1), handleKey(t, h2)) {
		t.Fatalf("boundary-colliding contexts produced the same derived key")
	}

	events := obs.snapshot()
	if len(events) != 4 {
		t.Fatalf("expected extract+expand for each context, got %d events", len(events))
	}
	for _, e := range events {
		if e.generation != 1 {
			t.Fatalf("event generation = %d, want 1", e.generation)
		}
	}
}

func TestDirectionMixingIsolated(t *testing.T) {
	obs := &recordingObserver{}
	host, err := NewHost(testRoot(0x03), WithCryptoObserver(obs.callback()))
	if err != nil {
		t.Fatalf("NewHost failed: %v", err)
	}

	sendCtx := validContext()
	sendCtx.Direction = DirectionSend
	recvCtx := validContext()
	recvCtx.Direction = DirectionRecv

	sendHandle, err := host.Derive(sendCtx, 32)
	if err != nil {
		t.Fatalf("Derive send failed: %v", err)
	}
	recvHandle, err := host.Derive(recvCtx, 32)
	if err != nil {
		t.Fatalf("Derive recv failed: %v", err)
	}

	sendKey := handleKey(t, sendHandle)
	recvKey := handleKey(t, recvHandle)
	if bytes.Equal(sendKey, recvKey) {
		t.Fatalf("send and recv directions shared a key")
	}
	if want := expectedKey(t, testRoot(0x03), sendCtx, 32); !bytes.Equal(sendKey, want) {
		t.Fatalf("send key does not match reference HKDF output")
	}

	dirs := map[Direction]bool{}
	for _, e := range obs.snapshot() {
		if e.op != CryptoExtract && e.op != CryptoExpand {
			t.Fatalf("unexpected crypto op %q", e.op)
		}
		dirs[e.context.Direction] = true
	}
	if !dirs[DirectionSend] || !dirs[DirectionRecv] {
		t.Fatalf("observer did not see both directions: %v", dirs)
	}
}

func TestCrossTenantCacheIsolation(t *testing.T) {
	obs := &recordingObserver{}
	host, err := NewHost(testRoot(0x04), WithCryptoObserver(obs.callback()))
	if err != nil {
		t.Fatalf("NewHost failed: %v", err)
	}

	tenantA := validContext()
	tenantA.Tenant = []byte("tenant-a")
	tenantB := validContext()
	tenantB.Tenant = []byte("tenant-b")

	ha1, err := host.Derive(tenantA, 32)
	if err != nil {
		t.Fatalf("Derive tenant A failed: %v", err)
	}
	hb, err := host.Derive(tenantB, 32)
	if err != nil {
		t.Fatalf("Derive tenant B failed: %v", err)
	}
	keyA1 := handleKey(t, ha1)
	keyB := handleKey(t, hb)
	if bytes.Equal(keyA1, keyB) {
		t.Fatalf("tenants shared derived key material")
	}

	// Re-deriving tenant A must hit the cache: same key, no new crypto events.
	eventsBefore := obs.count()
	ha2, err := host.Derive(tenantA, 32)
	if err != nil {
		t.Fatalf("cached Derive failed: %v", err)
	}
	if obs.count() != eventsBefore {
		t.Fatalf("cache hit still emitted crypto events")
	}
	if got := handleKey(t, ha2); !bytes.Equal(got, keyA1) {
		t.Fatalf("cached derivation returned a different key")
	}

	observedTenants := map[string]bool{}
	for _, e := range obs.snapshot() {
		observedTenants[string(e.context.Tenant)] = true
	}
	if !observedTenants["tenant-a"] || !observedTenants["tenant-b"] {
		t.Fatalf("observer missing tenants: %v", observedTenants)
	}
}
