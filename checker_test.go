package main

import "testing"

func mustCheck(t *testing.T, h *History, cfg CheckConfig) CheckResult {
	t.Helper()
	res, err := Check(h, cfg)
	if err != nil {
		t.Fatalf("Check failed: %v", err)
	}
	return res
}

func TestLinearizableWitness(t *testing.T) {
	// W(1) overlaps R()=1: read can be ordered after the write.
	h := &History{Ops: []Op{
		{ID: 0, Client: 0, Kind: Write, Value: 1, Invoke: 0, Respond: 10},
		{ID: 1, Client: 1, Kind: Read, Value: 1, Invoke: 5, Respond: 15},
	}}
	res := mustCheck(t, h, CheckConfig{InitValue: 0})
	if res.Verdict != Linearizable {
		t.Fatalf("verdict = %v, want LINEARIZABLE", res.Verdict)
	}
	if len(res.Witness) != 2 {
		t.Fatalf("witness length = %d, want 2", len(res.Witness))
	}
	// The write must be ordered before the read in the witness.
	if res.Witness[0].ID != 0 || res.Witness[1].ID != 1 {
		t.Fatalf("bad witness order: %v", res.Witness)
	}
}

func TestCounterexampleRealTime(t *testing.T) {
	// W(1) completes at 10; R()=0 starts at 11: stale read of initial value.
	h := &History{Ops: []Op{
		{ID: 0, Client: 0, Kind: Write, Value: 1, Invoke: 0, Respond: 10},
		{ID: 1, Client: 1, Kind: Read, Value: 0, Invoke: 11, Respond: 15},
	}}
	res := mustCheck(t, h, CheckConfig{InitValue: 0})
	if res.Verdict != NotLinearizable {
		t.Fatalf("verdict = %v, want NOT LINEARIZABLE", res.Verdict)
	}
}

func TestOverlappingWritesReorderable(t *testing.T) {
	// Two overlapping writes; a later read sees the "older" write's value.
	// Legal because the writes may be ordered either way.
	h := &History{Ops: []Op{
		{ID: 0, Client: 0, Kind: Write, Value: 1, Invoke: 0, Respond: 10},
		{ID: 1, Client: 1, Kind: Write, Value: 2, Invoke: 1, Respond: 9},
		{ID: 2, Client: 2, Kind: Read, Value: 1, Invoke: 11, Respond: 12},
	}}
	res := mustCheck(t, h, CheckConfig{InitValue: 0})
	if res.Verdict != Linearizable {
		t.Fatalf("verdict = %v, want LINEARIZABLE", res.Verdict)
	}
}

func TestNonOverlappingWritesOrdered(t *testing.T) {
	// W(2) finishes before W(1) starts, so final value must be 1.
	h := &History{Ops: []Op{
		{ID: 0, Client: 0, Kind: Write, Value: 2, Invoke: 0, Respond: 5},
		{ID: 1, Client: 1, Kind: Write, Value: 1, Invoke: 6, Respond: 10},
		{ID: 2, Client: 2, Kind: Read, Value: 2, Invoke: 11, Respond: 12},
	}}
	res := mustCheck(t, h, CheckConfig{InitValue: 0})
	if res.Verdict != NotLinearizable {
		t.Fatalf("verdict = %v, want NOT LINEARIZABLE", res.Verdict)
	}
}

func TestIncompleteWriteMayTakeEffect(t *testing.T) {
	// Incomplete W(1) overlaps R()=1: extension places the write before the read.
	h := &History{Ops: []Op{
		{ID: 0, Client: 0, Kind: Write, Value: 1, Invoke: 0, Respond: -1},
		{ID: 1, Client: 1, Kind: Read, Value: 1, Invoke: 5, Respond: 10},
	}}
	res := mustCheck(t, h, CheckConfig{InitValue: 0})
	if res.Verdict != Linearizable {
		t.Fatalf("verdict = %v, want LINEARIZABLE", res.Verdict)
	}
}

func TestIncompleteWriteMayBeDropped(t *testing.T) {
	// Incomplete W(1) then R()=0: legal only if the write never takes effect.
	h := &History{Ops: []Op{
		{ID: 0, Client: 0, Kind: Write, Value: 1, Invoke: 0, Respond: -1},
		{ID: 1, Client: 1, Kind: Read, Value: 0, Invoke: 5, Respond: 10},
	}}
	res := mustCheck(t, h, CheckConfig{InitValue: 0})
	if res.Verdict != Linearizable {
		t.Fatalf("verdict = %v, want LINEARIZABLE", res.Verdict)
	}
}

func TestIncompleteWriteNotRetroactive(t *testing.T) {
	// Incomplete W(1) invoked at 5 cannot justify R()=1 that completed at 4.
	h := &History{Ops: []Op{
		{ID: 0, Client: 0, Kind: Read, Value: 1, Invoke: 0, Respond: 4},
		{ID: 1, Client: 1, Kind: Write, Value: 1, Invoke: 5, Respond: -1},
	}}
	res := mustCheck(t, h, CheckConfig{InitValue: 0})
	if res.Verdict != NotLinearizable {
		t.Fatalf("verdict = %v, want NOT LINEARIZABLE", res.Verdict)
	}
}

func TestIncompleteReadDropped(t *testing.T) {
	// The incomplete read constrains nothing; the rest is trivially legal.
	h := &History{Ops: []Op{
		{ID: 0, Client: 0, Kind: Write, Value: 1, Invoke: 0, Respond: 5},
		{ID: 1, Client: 1, Kind: Read, Value: 99, Invoke: 1, Respond: -1},
	}}
	res := mustCheck(t, h, CheckConfig{InitValue: 0})
	if res.Verdict != Linearizable {
		t.Fatalf("verdict = %v, want LINEARIZABLE", res.Verdict)
	}
	if len(res.Witness) != 1 {
		t.Fatalf("witness length = %d, want 1 (incomplete read dropped)", len(res.Witness))
	}
}

func TestBudgetExhaustedUnknown(t *testing.T) {
	// 12 fully overlapping writes: huge search space, budget of 1 state.
	h := &History{}
	for i := 0; i < 12; i++ {
		h.Ops = append(h.Ops, Op{ID: i, Client: i, Kind: Write, Value: i + 1, Invoke: 0, Respond: 100})
	}
	res := mustCheck(t, h, CheckConfig{InitValue: 0, Budget: 1})
	if res.Verdict != Unknown {
		t.Fatalf("verdict = %v, want UNKNOWN", res.Verdict)
	}
}

func TestBudgetSufficientStillDecides(t *testing.T) {
	h := &History{}
	for i := 0; i < 12; i++ {
		h.Ops = append(h.Ops, Op{ID: i, Client: i, Kind: Write, Value: i + 1, Invoke: 0, Respond: 100})
	}
	res := mustCheck(t, h, CheckConfig{InitValue: 0, Budget: 100000})
	if res.Verdict != Linearizable {
		t.Fatalf("verdict = %v, want LINEARIZABLE", res.Verdict)
	}
}
