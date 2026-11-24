package capacitycred_test

import (
	"context"
	"errors"
	"testing"

	"prescreen.local/ps-47c6a59d98/capacitycred"
)

func preparedEngine(t *testing.T, h *host, total uint64) (*capacitycred.Engine, *capacitycred.CountingAllocator, capacitycred.Event, capacitycred.Params) {
	t.Helper()
	engine, alloc := newTestEngine(t, h)
	builder := newBuilder(t, h)
	params := testParams()
	mint, _ := builder.mint(tenantA, root, nodeA, total, params)
	mustReceive(t, engine, mint, h.callbacks())
	if err := engine.ReceiveReceipt(h.callbacks(), builder.receipt(root, tenantA, mint.EventID)); err != nil {
		t.Fatal(err)
	}
	return engine, alloc, mint, params
}

func TestCallbackErrorKeepsBufferAndLaterReleasesOnce(t *testing.T) {
	h := newHost()
	base := h.callbacks()
	engine, alloc, _, params := preparedEngine(t, h, 1024)

	fail := base
	fail.Hash = func(_ context.Context, _ []byte) ([]byte, error) {
		return nil, errors.New("kdf backend failed")
	}

	if err := engine.Reserve(capacitycred.ReservationRequest{TaskID: "err", CredID: root, Tenant: tenantA, Params: params}); err != nil {
		t.Fatal(err)
	}

	if _, err := runWithHash(engine, "err", fail.Hash); err == nil {
		t.Fatal("callback error must be surfaced")
	}
	if alloc.Outstanding() != 128 || alloc.ReleaseCount() != 0 {
		t.Fatalf("failed callback must not release delivered buffer: %+v", alloc)
	}
	if err := engine.Cancel("err"); err != nil {
		t.Fatal(err)
	}
	if alloc.Outstanding() != 0 || alloc.ReleaseCount() != 1 || alloc.EraseCount() != 1 {
		t.Fatalf("cancel after error did not erase/release once: %+v", alloc)
	}
}

func TestPanicAndCancellationDoNotReleaseBeforeErase(t *testing.T) {
	h := newHost()
	base := h.callbacks()
	engine, alloc, _, params := preparedEngine(t, h, 1024)
	if err := engine.Reserve(capacitycred.ReservationRequest{TaskID: "panic", CredID: root, Tenant: tenantA, Params: params}); err != nil {
		t.Fatal(err)
	}
	panicHash := func(context.Context, []byte) ([]byte, error) {
		panic("boom")
	}
	if _, err := runWithHash(engine, "panic", panicHash); err == nil {
		t.Fatal("panic must be converted to an error")
	}
	if alloc.Outstanding() != 128 || alloc.EraseCount() != 0 {
		t.Fatalf("panic path must retain buffer until explicit finish: %+v", alloc)
	}
	if err := engine.Complete("panic"); err != nil {
		t.Fatal(err)
	}
	if alloc.Outstanding() != 0 || alloc.ReleaseCount() != 1 {
		t.Fatalf("completion did not release once: %+v", alloc)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	cancelHash := base
	cancelHash.Hash = func(context.Context, []byte) ([]byte, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	if err := engine.Reserve(capacitycred.ReservationRequest{TaskID: "cancel", CredID: root, Tenant: tenantA, Params: params}); err != nil {
		t.Fatal(err)
	}
	if _, err := runWithHash(engine, "cancel", cancelHash.Hash); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation error: %v", err)
	}
	if alloc.Outstanding() != 128 {
		t.Fatalf("cancelled callback must retain buffer until finish: %+v", alloc)
	}
	if err := engine.Cancel("cancel"); err != nil {
		t.Fatal(err)
	}
	if alloc.Outstanding() != 0 || alloc.ReleaseCount() != 2 {
		t.Fatalf("cancel path did not release exactly once: %+v", alloc)
	}
}

func runWithHash(engine *capacitycred.Engine, id capacitycred.TaskID, hash capacitycred.HashFunc) ([]byte, error) {
	return engine.RunWithHash(context.Background(), id, hash)
}
