package capacitycred_test

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"prescreen.local/ps-47c6a59d98/capacitycred"
)

func TestMissingCallbacksRejectedAtConstructionAndReceive(t *testing.T) {
	h := newHost()
	cb := h.callbacks()
	alloc := capacitycred.NewCountingAllocator(1024)

	if _, err := capacitycred.NewEngine(capacitycred.Callbacks{Verify: cb.Verify}, nodeA, alloc); !errors.Is(err, capacitycred.ErrMissingCallback) {
		t.Fatalf("missing hash: %v", err)
	}
	if _, err := capacitycred.NewEngine(capacitycred.Callbacks{Hash: cb.Hash}, nodeA, alloc); !errors.Is(err, capacitycred.ErrMissingCallback) {
		t.Fatalf("missing verify: %v", err)
	}
	if _, err := capacitycred.NewEngine(cb, nodeA, nil); !errors.Is(err, capacitycred.ErrMissingCallback) {
		t.Fatalf("missing allocator: %v", err)
	}

	engine, err := capacitycred.NewEngine(cb, nodeA, alloc)
	if err != nil {
		t.Fatal(err)
	}
	builder := newBuilder(t, h)
	mint, _ := builder.mint(tenantA, root, nodeA, 1024, testParams())
	assertIs(t, engine.Receive(capacitycred.Envelope{Event: mint}), capacitycred.ErrMissingCallback)

	bad := cb
	bad.Verify = nil
	assertIs(t, engine.Receive(capacitycred.Envelope{Event: mint, Callbacks: bad}), capacitycred.ErrMissingCallback)

	if _, err := engine.ReplayAll([]capacitycred.Envelope{{Event: mint}}); !errors.Is(err, capacitycred.ErrMissingCallback) {
		t.Fatalf("replay missing callbacks: %v", err)
	}
}

func TestCrossTenantTheftRejected(t *testing.T) {
	h := newHost()
	cb := h.callbacks()
	engine, _ := newTestEngine(t, h)
	builder := newBuilder(t, h)
	params := testParams()

	mint, _ := builder.mint(tenantA, root, nodeA, 1024, params)
	mustReceive(t, engine, mint, cb)

	forged, _ := builder.mint(tenantB, root, nodeA, 1024, params)
	err := engine.Receive(capacitycred.Envelope{Event: forged, Callbacks: cb})
	if !errors.Is(err, capacitycred.ErrDuplicateEvent) && !errors.Is(err, capacitycred.ErrBadSignature) {
		t.Fatalf("tenant B reusing tenant A cred id: %v", err)
	}

	foreignSplit := builder.split(tenantB, root, root, mint.EventID,
		capacitycred.ChildSpec{ID: evil, Range: capacitycred.Interval{Low: 0, High: 1024}})
	assertIs(t, engine.Receive(capacitycred.Envelope{Event: foreignSplit, Callbacks: cb}), capacitycred.ErrTenantMismatch)

	otherMint, _ := builder.mint(tenantB, "other-root", nodeA, 1024, params)
	mustReceive(t, engine, otherMint, cb)
	foreignHandoff := builder.handoff(tenantA, "other-root", "other-root", nodeA, nodeB, otherMint.EventID)
	assertIs(t, engine.Receive(capacitycred.Envelope{Event: foreignHandoff, Callbacks: cb}), capacitycred.ErrTenantMismatch)

	receipt := builder.receipt(root, tenantA, mint.EventID)
	if err := engine.ReceiveReceipt(cb, receipt); err != nil {
		t.Fatal(err)
	}
	err = engine.Reserve(capacitycred.ReservationRequest{
		TaskID: "stolen",
		CredID: root,
		Tenant: tenantB,
		Params: params,
	})
	assertIs(t, err, capacitycred.ErrTenantMismatch)
}

func TestHalfIntervalConflictQuarantinesOverlapButKeepsSibling(t *testing.T) {
	h := newHost()
	cb := h.callbacks()
	builder := newBuilder(t, h)
	params := testParams()
	mint, _ := builder.mint(tenantA, root, nodeA, 1024, params)
	first := builder.split(tenantA, root, root, mint.EventID,
		capacitycred.ChildSpec{ID: childL, Range: capacitycred.Interval{Low: 0, High: 512}},
		capacitycred.ChildSpec{ID: childR, Range: capacitycred.Interval{Low: 512, High: 768}},
		capacitycred.ChildSpec{ID: "child-r2", Range: capacitycred.Interval{Low: 768, High: 1024}})
	competing := builder.split(tenantA, root, root, mint.EventID,
		capacitycred.ChildSpec{ID: evil, Range: capacitycred.Interval{Low: 0, High: 512}},
		capacitycred.ChildSpec{ID: childR, Range: capacitycred.Interval{Low: 512, High: 768}},
		capacitycred.ChildSpec{ID: "child-r2", Range: capacitycred.Interval{Low: 768, High: 1024}})
	competing = builder.WithNonce(99).finish(competing)

	events := []capacitycred.Event{mint, first, competing}
	for _, order := range permutations(events) {
		engine, _ := newTestEngine(t, h)
		for _, ev := range order {
			if err := engine.Receive(capacitycred.Envelope{Event: ev, Callbacks: cb}); err != nil {
				t.Fatalf("order %v: %v", order, err)
			}
		}
		snap := engine.Snapshot()
		left := findCred(t, snap, childL)
		right := findCred(t, snap, childR)
		r2 := findCred(t, snap, "child-r2")
		evilView := findCred(t, snap, evil)
		if !left.Quarantined || !evilView.Quarantined {
			t.Fatalf("overlapping children must be quarantined: %+v %+v", left, evilView)
		}
		if right.Quarantined || right.FreeBytes != 256 || r2.Quarantined || r2.FreeBytes != 256 {
			t.Fatalf("disjoint siblings must remain allocatable: %+v %+v", right, r2)
		}
	}
}

func TestDuplicateCompletionReleasesExactlyOnceAfterErase(t *testing.T) {
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

	req := capacitycred.ReservationRequest{TaskID: "task", CredID: root, Tenant: tenantA, Params: params}
	if err := engine.Reserve(req); err != nil {
		t.Fatal(err)
	}
	if alloc.AcquireCount() != 1 || alloc.Outstanding() != 128 {
		t.Fatalf("s_cost*32 accounting wrong: %+v", alloc)
	}
	if _, err := engine.Run(context.Background(), "task"); err != nil {
		t.Fatal(err)
	}
	if err := engine.Complete("task"); err != nil {
		t.Fatal(err)
	}
	assertIs(t, engine.Complete("task"), capacitycred.ErrTaskCompleted)
	if alloc.Outstanding() != 0 || alloc.ReleasedBytes() != 128 || alloc.ReleaseCount() != 1 {
		t.Fatalf("quota released more than once: %+v", alloc)
	}
	if alloc.EraseCount() != 1 || alloc.EraseCount() > uint64(alloc.ReleaseCount()) {
		t.Fatalf("erase must happen before the single release: %+v", alloc)
	}
}

func TestParameterSubstitutionRejected(t *testing.T) {
	h := newHost()
	cb := h.callbacks()
	engine, _ := newTestEngine(t, h)
	builder := newBuilder(t, h)
	params := testParams()
	mint, commitment := builder.mint(tenantA, root, nodeA, 1024, params)
	mustReceive(t, engine, mint, cb)
	if err := engine.ReceiveReceipt(cb, builder.receipt(root, tenantA, mint.EventID)); err != nil {
		t.Fatal(err)
	}

	changed := params
	changed.SCost++
	err := engine.Reserve(capacitycred.ReservationRequest{TaskID: "substitute", CredID: root, Tenant: tenantA, Params: changed})
	assertIs(t, err, capacitycred.ErrParamsMismatch)

	recomputed, err := capacitycred.ParamsCommitment(cb, params)
	if err != nil || string(recomputed) != string(commitment) {
		t.Fatalf("commitment not deterministic: %v", err)
	}
}

func TestOnlineAndRecomputeAreIdenticalForEveryPermutation(t *testing.T) {
	h := newHost()
	cb := h.callbacks()
	builder := newBuilder(t, h)
	params := testParams()
	mint, _ := builder.mint(tenantA, root, nodeA, 1024, params)
	split := builder.split(tenantA, root, root, mint.EventID,
		capacitycred.ChildSpec{ID: childL, Range: capacitycred.Interval{Low: 0, High: 256}},
		capacitycred.ChildSpec{ID: childR, Range: capacitycred.Interval{Low: 256, High: 1024}})
	handoff := builder.handoff(tenantA, childR, root, nodeA, nodeB, split.EventID)
	revoke := builder.revoke(tenantA, childL, root, split.EventID)
	events := []capacitycred.Event{mint, split, handoff, revoke}

	reference, err := capacitycred.Recompute(envelopeSet(events, cb), nodeA)
	if err != nil {
		t.Fatal(err)
	}
	for _, order := range permutations(events) {
		engine, _ := newTestEngine(t, h)
		for _, ev := range order {
			if err := engine.Receive(capacitycred.Envelope{Event: ev, Callbacks: cb}); err != nil {
				t.Fatalf("order %v: %v", order, err)
			}
		}
		got := engine.Snapshot()
		if !reflect.DeepEqual(got, reference) {
			t.Fatalf("snapshot divergence for order %v", order)
		}
	}
	if len(reference.Sequence) != len(events) {
		t.Fatal("recompute did not retain every applied event")
	}
}
