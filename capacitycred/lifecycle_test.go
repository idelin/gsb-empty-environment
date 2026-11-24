package capacitycred_test

import (
	"context"
	"errors"
	"testing"

	"prescreen.local/ps-47c6a59d98/capacitycred"
)

func TestFinalityRequiredAndRevokeDoesNotReallocateDeliveredBuffer(t *testing.T) {
	h := newHost()
	cb := h.callbacks()
	engine, alloc := newTestEngine(t, h)
	builder := newBuilder(t, h)
	params := testParams()
	mint, _ := builder.mint(tenantA, root, nodeA, 256, params)
	mustReceive(t, engine, mint, cb)

	req := capacitycred.ReservationRequest{TaskID: "held", CredID: root, Tenant: tenantA, Params: params}
	assertIs(t, engine.Reserve(req), capacitycred.ErrNoFinality)

	if err := engine.ReceiveReceipt(cb, builder.receipt(root, tenantA, mint.EventID)); err != nil {
		t.Fatal(err)
	}
	if err := engine.Reserve(req); err != nil {
		t.Fatal(err)
	}
	if alloc.Outstanding() != 128 {
		t.Fatalf("delivered buffer not held: %d", alloc.Outstanding())
	}

	revoke := builder.revoke(tenantA, root, root, mint.EventID)
	mustReceive(t, engine, revoke, cb)
	view := findCred(t, engine.Snapshot(), root)
	if !view.Revoked || view.HeldBytes != 128 {
		t.Fatalf("revocation must retain delivered bytes: %+v", view)
	}
	assertIs(t, engine.Reserve(capacitycred.ReservationRequest{
		TaskID: "new", CredID: root, Tenant: tenantA, Params: params,
	}), capacitycred.ErrBlocked)

	if _, err := engine.Run(context.Background(), "held"); err != nil {
		t.Fatal(err)
	}
	if err := engine.Complete("held"); err != nil {
		t.Fatal(err)
	}
	if alloc.Outstanding() != 0 {
		t.Fatalf("explicit completion did not release: %d", alloc.Outstanding())
	}
}

func TestCrashReentryDoesNotDoubleAllocateAndCancelsOnce(t *testing.T) {
	h := newHost()
	cb := h.callbacks()
	engine, alloc := newTestEngine(t, h)
	builder := newBuilder(t, h)
	params := testParams()
	mint, _ := builder.mint(tenantA, root, nodeA, 1024, params)
	mustReceive(t, engine, mint, cb)
	if err := engine.ReceiveReceipt(cb, builder.receipt(root, tenantA, mint.EventID)); err != nil {
		t.Fatal(err)
	}

	req := capacitycred.ReservationRequest{TaskID: "reentry", CredID: root, Tenant: tenantA, Params: params}
	if err := engine.Reserve(req); err != nil {
		t.Fatal(err)
	}
	if err := engine.Reserve(req); err != nil {
		t.Fatalf("identical crash reentry must be idempotent: %v", err)
	}
	if alloc.AcquireCount() != 1 || alloc.Outstanding() != 128 {
		t.Fatalf("reentry opened a second reservation: %+v", alloc)
	}

	changed := req
	changed.Params.Label = "different"
	assertIs(t, engine.Reserve(changed), capacitycred.ErrTaskReentry)

	if err := engine.Cancel("reentry"); err != nil {
		t.Fatal(err)
	}
	assertIs(t, engine.Cancel("reentry"), capacitycred.ErrTaskNotFound)
	assertIs(t, engine.Complete("reentry"), capacitycred.ErrTaskNotFound)
	if alloc.Outstanding() != 0 || alloc.ReleaseCount() != 1 {
		t.Fatalf("cancel did not release exactly once: %+v", alloc)
	}
}

func TestHandoffForkQuarantinesBothBranches(t *testing.T) {
	h := newHost()
	cb := h.callbacks()
	builder := newBuilder(t, h)
	params := testParams()
	mint, _ := builder.mint(tenantA, root, nodeA, 1024, params)
	first := builder.handoff(tenantA, root, root, nodeA, nodeB, mint.EventID)
	second := builder.handoff(tenantA, root, root, nodeA, nodeB, mint.EventID)
	second = builder.WithNonce(7).finish(second)

	for _, order := range permutations([]capacitycred.Event{mint, first, second}) {
		engine, _ := newTestEngine(t, h)
		for _, ev := range order {
			if err := engine.Receive(capacitycred.Envelope{Event: ev, Callbacks: cb}); err != nil {
				t.Fatal(err)
			}
		}
		view := findCred(t, engine.Snapshot(), root)
		if !view.Quarantined || view.Owner != "" || view.FreeBytes != 0 {
			t.Fatalf("forked handoff must not remain spendable: %+v", view)
		}
	}
}

func TestOverflowProtectedAndBadSignatureRejected(t *testing.T) {
	if _, err := capacitycred.MemoryBytes(^uint64(0)); !errors.Is(err, capacitycred.ErrOverflow) {
		t.Fatalf("memory overflow not detected: %v", err)
	}
	size, err := capacitycred.MemoryBytes(4)
	if err != nil || size != 128 {
		t.Fatalf("exact s_cost*32 = %d, err=%v", size, err)
	}

	h := newHost()
	cb := h.callbacks()
	engine, _ := newTestEngine(t, h)
	builder := newBuilder(t, h)
	mint, _ := builder.mint(tenantA, root, nodeA, 1024, testParams())
	mint.Signature = append([]byte(nil), mint.Signature...)
	mint.Signature[0] ^= 0xff
	err = engine.Receive(capacitycred.Envelope{Event: mint, Callbacks: cb})
	if !errors.Is(err, capacitycred.ErrBadSignature) {
		t.Fatalf("tampered signature: %v", err)
	}
}

func TestOutOfOrderChildrenBeforeParent(t *testing.T) {
	h := newHost()
	cb := h.callbacks()
	engine, _ := newTestEngine(t, h)
	builder := newBuilder(t, h)
	params := testParams()
	mint, _ := builder.mint(tenantA, root, nodeA, 1024, params)
	split := builder.split(tenantA, root, root, mint.EventID,
		capacitycred.ChildSpec{ID: childL, Range: capacitycred.Interval{Low: 0, High: 512}},
		capacitycred.ChildSpec{ID: childR, Range: capacitycred.Interval{Low: 512, High: 1024}})

	if err := engine.Receive(capacitycred.Envelope{Event: split, Callbacks: cb}); err != nil {
		t.Fatal(err)
	}
	if len(engine.Snapshot().Credentials) != 0 {
		t.Fatal("child event must remain buffered until parent arrives")
	}
	mustReceive(t, engine, mint, cb)
	snap := engine.Snapshot()
	if len(snap.Credentials) != 3 {
		t.Fatalf("buffered split did not flush: %+v", snap.Credentials)
	}
}

func TestRecomputeRejectsIncompleteChain(t *testing.T) {
	h := newHost()
	cb := h.callbacks()
	builder := newBuilder(t, h)
	mint, _ := builder.mint(tenantA, root, nodeA, 1024, testParams())
	split := builder.split(tenantA, root, root, mint.EventID,
		capacitycred.ChildSpec{ID: childL, Range: capacitycred.Interval{Low: 0, High: 512}},
		capacitycred.ChildSpec{ID: childR, Range: capacitycred.Interval{Low: 512, High: 1024}})
	_, err := capacitycred.Recompute(envelopeSet([]capacitycred.Event{split}, cb), nodeA)
	assertIs(t, err, capacitycred.ErrUnresolved)
}
