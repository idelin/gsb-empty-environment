package revco

import "testing"

func TestInflightMessageRevokedButUnknown(t *testing.T) {
	g := NewGraph()
	const obj ObjID = "mapZ"
	root := HoldKey{Gen: 1, ID: "h0"}
	mustObserve(t, g,
		Event{Type: EvIssue, Channel: "cA", Seq: 1, Gen: 1, Object: obj, NewID: "h0",
			Owner: own("A"), Range: Range{Off: 0, Len: 100}, Rights: []Right{Read, Write}},
		Event{Type: EvSend, Channel: "cA", Seq: 2, Gen: 1, Object: obj, NewID: "m1",
			Owner: own("A"), Receiver: own("B"), Source: "h0", DestChan: "cB",
			Range: Range{Off: 40, Len: 20}, Rights: []Right{Write}},
	)
	mustAdvance(t, g, map[ChanID]Seq{"cA": 2})
	host := &recordingHost{}
	co, _ := NewCoordinator(g, host, &MemoryStore{})
	rec, err := co.Isolate(IsolateRequest{
		ID: "req-x", Object: obj, Root: root,
		Range: Range{Off: 0, Len: 100}, Rights: []Right{Read, Write},
	})
	if err != nil {
		t.Fatal(err)
	}
	calls := map[HoldKey]bool{}
	for _, k := range host.calls {
		calls[k] = true
	}
	if !calls[root] || !calls[HoldKey{Gen: 1, ID: "m1"}] {
		t.Fatalf("root and in-flight message must both be revoked at host: %v", host.calls)
	}
	if rec.Complete {
		t.Fatalf("in-flight derivation keeps bytes unprovable until cB advances")
	}
	if !rangesEqual(rec.Unknown, []Range{{Off: 40, Len: 20}}) {
		t.Fatalf("unknown = %+v", rec.Unknown)
	}
	if !rangesEqual(rec.Revoked, []Range{{Off: 0, Len: 40}, {Off: 60, Len: 40}}) {
		t.Fatalf("revoked = %+v", rec.Revoked)
	}
	again, err := co.Isolate(IsolateRequest{
		ID: "req-x", Object: obj, Root: root,
		Range: Range{Off: 0, Len: 100}, Rights: []Right{Read, Write},
	})
	if err != nil || len(host.calls) != 2 {
		t.Fatalf("replay no-op expected, err=%v calls=%d", err, len(host.calls))
	}
	if again.Complete || !rangesEqual(again.Unknown, rec.Unknown) {
		t.Fatalf("replay must stay bound: %+v", again)
	}
}

func TestReceiveAfterRevokeIsBornDead(t *testing.T) {
	g := NewGraph()
	const obj ObjID = "mapR"
	mustObserve(t, g,
		Event{Type: EvIssue, Channel: "cA", Seq: 1, Gen: 1, Object: obj, NewID: "h0",
			Owner: own("A"), Range: Range{Off: 0, Len: 32}, Rights: []Right{Read}},
		Event{Type: EvSend, Channel: "cA", Seq: 2, Gen: 1, Object: obj, NewID: "m1",
			Owner: own("A"), Receiver: own("B"), Source: "h0", DestChan: "cB",
			Range: Range{Off: 0, Len: 32}, Rights: []Right{Read}},
		Event{Type: EvRevoke, Channel: "cA", Seq: 3, Gen: 1, Object: obj, Source: "m1", Owner: own("A")},
	)
	mustAdvance(t, g, map[ChanID]Seq{"cA": 3})
	mustObserve(t, g, Event{Type: EvReceive, Channel: "cB", Seq: 1, Gen: 1, Object: obj,
		NewID: "hB", Owner: own("B"), Source: "m1", SrcGen: 1, DestChan: "cB"})
	mustAdvance(t, g, map[ChanID]Seq{"cB": 1})

	h := g.Snapshot().Holds[HoldKey{Gen: 1, ID: "hB"}]
	if h == nil {
		t.Fatalf("receive must materialize a hold even when revoked")
	}
	if !h.Dead {
		t.Fatalf("hold delivered after revocation must be born dead")
	}
}
