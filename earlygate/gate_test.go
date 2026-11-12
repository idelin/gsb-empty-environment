package earlygate_test

import (
	"errors"
	"testing"

	"example.org/netsec/q0165/earlygate"
)

func TestBatchParsing_GatesByImmutableClassification(t *testing.T) {
	gate, rec := newTestGate(t)

	first := gate.SubmitBatch(1, []testOp{
		{name: "a", kind: "read"},
		{name: "b", kind: "write"},
		{name: "c", kind: "authget"},
		{name: "d", kind: "idempot"},
	})
	if len(first) != 4 {
		t.Fatalf("batch 1 decisions = %d, want 4", len(first))
	}

	got := refs(first)
	if _, ok := got[1].(earlygate.ExecuteEarly[testOp]); !ok {
		t.Fatalf("request 1 = %T, want ExecuteEarly", got[1])
	}
	if v, ok := got[2].(earlygate.HoldEarly[testOp]); !ok || v.Reason != earlygate.HoldReasonMutating {
		t.Fatalf("request 2 = %#v, want HoldEarly(Mutating)", got[2])
	}
	if v, ok := got[3].(earlygate.HoldEarly[testOp]); !ok || v.Reason != earlygate.HoldReasonIdentity {
		t.Fatalf("request 3 = %#v, want HoldEarly(Identity)", got[3])
	}
	if v, ok := got[4].(earlygate.HoldEarly[testOp]); !ok || v.Reason != earlygate.HoldReasonMutating {
		t.Fatalf("request 4 = %#v, want HoldEarly(Mutating) for non-readonly replayable op", got[4])
	}

	second := gate.SubmitBatch(2, []testOp{{name: "e", kind: "read"}})
	v := second[0].(earlygate.ExecuteEarly[testOp])
	if v.BatchID != 2 || v.RequestID != 5 {
		t.Fatalf("second batch ref = batch %d id %d, want batch 2 id 5", v.BatchID, v.RequestID)
	}

	confirmed, err := gate.Confirm()
	if err != nil {
		t.Fatalf("Confirm: %v", err)
	}
	normal := normalIDs(t, confirmed)
	if len(normal) != 3 || !normal[2] || !normal[3] || !normal[4] {
		t.Fatalf("confirmed dispatch = %v, want requests 2,3,4", normal)
	}

	all := rec.snapshot()
	early := earlyIDs(t, all)
	for id := range early {
		if normalIDs(t, all)[id] {
			t.Fatalf("early request %d was replayed as normal dispatch", id)
		}
	}

	wantStatus := map[uint64]earlygate.Status{
		1: earlygate.StatusEarlyExecuting,
		2: earlygate.StatusDispatched,
		3: earlygate.StatusDispatched,
		4: earlygate.StatusDispatched,
		5: earlygate.StatusEarlyExecuting,
	}
	for _, info := range gate.Requests() {
		if want := wantStatus[info.Ref.RequestID]; info.Status != want {
			t.Fatalf("request %d status = %s, want %s", info.Ref.RequestID, info.Status, want)
		}
	}

	post := gate.SubmitBatch(3, []testOp{{name: "f", kind: "write"}, {name: "g", kind: "read"}})
	postRefs := refs(post)
	if _, ok := postRefs[6].(earlygate.DispatchNormal[testOp]); !ok {
		t.Fatalf("post-confirm write = %T, want DispatchNormal", postRefs[6])
	}
	if _, ok := postRefs[7].(earlygate.DispatchNormal[testOp]); !ok {
		t.Fatalf("post-confirm read = %T, want DispatchNormal (no early stage after confirm)", postRefs[7])
	}
}

func TestClassifierUpdate_AffectsOnlyNewRequests(t *testing.T) {
	gate, _ := newTestGate(t)

	before := gate.SubmitBatch(1, []testOp{{name: "a", kind: "read"}})
	if _, ok := refs(before)[1].(earlygate.ExecuteEarly[testOp]); !ok {
		t.Fatalf("request 1 should execute early under initial classifier")
	}

	if err := gate.UpdateClassifier(classifierFor("read", earlygate.Classification{
		ReadOnly: true, Replayable: true, Identity: earlygate.IdentityHandshake,
	})); err != nil {
		t.Fatalf("UpdateClassifier: %v", err)
	}

	after := gate.SubmitBatch(2, []testOp{{name: "b", kind: "read"}, {name: "c", kind: "write"}})
	got := refs(after)
	hold, ok := got[2].(earlygate.HoldEarly[testOp])
	if !ok || hold.Reason != earlygate.HoldReasonIdentity {
		t.Fatalf("request 2 = %#v, want HoldEarly(Identity) under updated classifier", got[2])
	}

	info, ok := gate.Request(1)
	if !ok {
		t.Fatalf("request 1 missing")
	}
	if !info.Early || info.Classification.Identity != earlygate.IdentityNone {
		t.Fatalf("request 1 classification mutated: %+v", info)
	}

	if err := gate.UpdateClassifier(nil); !errors.Is(err, earlygate.ErrNilClassifier) {
		t.Fatalf("nil classifier err = %v, want ErrNilClassifier", err)
	}
}

func TestReject_HeldRequestsFailAndEarlyRequestsAreNotReplayed(t *testing.T) {
	gate, rec := newTestGate(t)

	gate.SubmitBatch(1, []testOp{
		{name: "a", kind: "read"},
		{name: "b", kind: "write"},
		{name: "c", kind: "read"},
	})

	if err := gate.AckEarlyExecution(1); err != nil {
		t.Fatalf("AckEarlyExecution(1): %v", err)
	}

	rejectReason := errors.New("certificate rejected")
	rejected, err := gate.Reject(rejectReason)
	if err != nil {
		t.Fatalf("Reject: %v", err)
	}
	rejectedSet := rejectedIDs(t, rejected)
	if len(rejectedSet) != 1 || !rejectedSet[2] {
		t.Fatalf("rejected decisions = %v, want only held request 2", rejectedSet)
	}

	all := rec.snapshot()
	early := earlyIDs(t, all)
	if !early[1] || !early[3] {
		t.Fatalf("early set = %v, want requests 1 and 3", early)
	}
	for id := range early {
		if normalIDs(t, all)[id] {
			t.Fatalf("early request %d replayed after rejection", id)
		}
	}

	info1, _ := gate.Request(1)
	info3, _ := gate.Request(3)
	if info1.Status != earlygate.StatusEarlyUnconfirmed || !info1.EarlyFinished {
		t.Fatalf("request 1 = %s finished=%v, want EarlyUnconfirmed+finished", info1.Status, info1.EarlyFinished)
	}
	if info3.Status != earlygate.StatusEarlyUnconfirmed || info3.EarlyFinished {
		t.Fatalf("request 3 = %s finished=%v, want EarlyUnconfirmed without finish ack", info3.Status, info3.EarlyFinished)
	}

	earlyList := gate.EarlyRequests()
	if len(earlyList) != 2 || earlyList[0] != 1 || earlyList[1] != 3 {
		t.Fatalf("EarlyRequests = %v, want [1 3]", earlyList)
	}

	post := gate.SubmitBatch(2, []testOp{{name: "d", kind: "read"}, {name: "e", kind: "write"}})
	if got := rejectedIDs(t, post); len(got) != 2 {
		t.Fatalf("post-reject batch = %v, want 2 rejections", got)
	}

	if _, err := gate.Reject(nil); !errors.Is(err, earlygate.ErrHandshakeSettled) {
		t.Fatalf("second Reject err = %v, want ErrHandshakeSettled", err)
	}
	if _, err := gate.Confirm(); !errors.Is(err, earlygate.ErrHandshakeSettled) {
		t.Fatalf("Confirm after reject err = %v, want ErrHandshakeSettled", err)
	}
	if err := gate.UpdateClassifier(staticClassifier()); !errors.Is(err, earlygate.ErrHandshakeSettled) {
		t.Fatalf("UpdateClassifier after reject err = %v, want ErrHandshakeSettled", err)
	}
}
