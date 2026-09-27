package clocksync

import "testing"

func TestCorrectedCausalEdgesNotReversed(t *testing.T) {
	events := []Event{
		{Node: "A", Kind: Send, Msg: "m1", TS: 100},
		{Node: "B", Kind: Recv, Msg: "m1", TS: 157},
		{Node: "B", Kind: Send, Msg: "m2", TS: 160},
		{Node: "A", Kind: Recv, Msg: "m2", TS: 115},
		{Node: "B", Kind: Send, Msg: "m3", TS: 200},
		{Node: "C", Kind: Recv, Msg: "m3", TS: 124},
		{Node: "C", Kind: Send, Msg: "m4", TS: 130},
		{Node: "B", Kind: Recv, Msg: "m4", TS: 216},
	}
	msgs, err := PairMessages(events)
	if err != nil {
		t.Fatal(err)
	}
	res := Solve(Nodes(nil, msgs), DeriveConstraints(msgs))
	if res.Conflict != nil {
		t.Fatalf("unexpected conflict: %v", res.Conflict)
	}
	if v := CheckCausal(msgs, res.Offsets); len(v) != 0 {
		t.Fatalf("reversed causal edges after correction: %v", v)
	}
}

func TestCheckCausalDetectsReversal(t *testing.T) {
	// A deliberately wrong offset must be caught.
	msgs := []Message{{ID: "m1", From: "A", To: "B", SendTS: 100, RecvTS: 105}}
	offsets := map[string]float64{"A": 0, "B": 50}
	v := CheckCausal(msgs, offsets)
	if len(v) != 1 || v[0].Delta >= 0 {
		t.Fatalf("expected one reversed edge, got %v", v)
	}
}

func TestPairMessagesErrors(t *testing.T) {
	if _, err := PairMessages([]Event{{Node: "A", Kind: Recv, Msg: "x", TS: 1}}); err == nil {
		t.Fatal("recv without send should error")
	}
	if _, err := PairMessages([]Event{{Node: "A", Kind: Send, Msg: "x", TS: 1}}); err == nil {
		t.Fatal("send without recv should error")
	}
}
