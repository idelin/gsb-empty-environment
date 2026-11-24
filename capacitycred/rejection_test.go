package capacitycred_test

import (
	"errors"
	"testing"

	"prescreen.local/ps-47c6a59d98/capacitycred"
)

func TestBadReceiptBadEventIDAndInsufficientCapacity(t *testing.T) {
	h := newHost()
	cb := h.callbacks()
	engine, _ := newTestEngine(t, h)
	builder := newBuilder(t, h)
	params := testParams()
	mint, _ := builder.mint(tenantA, root, nodeA, 64, params)
	mustReceive(t, engine, mint, cb)

	wrongRoot := builder.receipt("missing", tenantA, mint.EventID)
	assertIs(t, engine.ReceiveReceipt(cb, wrongRoot), capacitycred.ErrBadReceipt)

	wrongTip := builder.receipt(root, tenantA, "unknown-tip")
	assertIs(t, engine.ReceiveReceipt(cb, wrongTip), capacitycred.ErrBadReceipt)

	tampered := builder.receipt(root, tenantA, mint.EventID)
	tampered.Signature[0] ^= 0x01
	assertIs(t, engine.ReceiveReceipt(cb, tampered), capacitycred.ErrBadReceipt)

	good := builder.receipt(root, tenantA, mint.EventID)
	if err := engine.ReceiveReceipt(cb, good); err != nil {
		t.Fatal(err)
	}

	badID := mint
	badID.EventID = "not-the-canonical-digest"
	err := engine.Receive(capacitycred.Envelope{Event: badID, Callbacks: cb})
	if !errors.Is(err, capacitycred.ErrBadEventID) {
		t.Fatalf("bad event id: %v", err)
	}

	err = engine.Reserve(capacitycred.ReservationRequest{
		TaskID: "too-big",
		CredID: root,
		Tenant: tenantA,
		Params: params,
	})
	assertIs(t, err, capacitycred.ErrInsufficient)
}

func TestUnknownCredentialAndOwnerRejected(t *testing.T) {
	h := newHost()
	cb := h.callbacks()
	engine, _ := newTestEngine(t, h)
	builder := newBuilder(t, h)
	params := testParams()
	mint, _ := builder.mint(tenantA, root, nodeA, 1024, params)
	mustReceive(t, engine, mint, cb)
	if err := engine.ReceiveReceipt(cb, builder.receipt(root, tenantA, mint.EventID)); err != nil {
		t.Fatal(err)
	}
	handoff := builder.handoff(tenantA, root, root, nodeA, nodeB, mint.EventID)
	mustReceive(t, engine, handoff, cb)

	err := engine.Reserve(capacitycred.ReservationRequest{
		TaskID: "unknown",
		CredID: "missing",
		Tenant: tenantA,
		Params: params,
	})
	assertIs(t, err, capacitycred.ErrUnknownCred)

	err = engine.Reserve(capacitycred.ReservationRequest{
		TaskID: "foreign-owner",
		CredID: root,
		Tenant: tenantA,
		Params: params,
	})
	assertIs(t, err, capacitycred.ErrWrongOwner)
}
