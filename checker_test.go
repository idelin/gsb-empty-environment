package main

import "testing"

const testBudget = 1000000

func check(t *testing.T, ops []Op, init int) Result {
	t.Helper()
	return Checker{Init: init, Budget: testBudget}.Check(ops)
}

func mustVerdict(t *testing.T, ops []Op, init int, want Verdict) Result {
	t.Helper()
	res := check(t, ops, init)
	if res.Verdict != want {
		t.Fatalf("verdict = %s, want %s (reason: %s)", res.Verdict, want, res.Reason)
	}
	return res
}

// Non-overlapping operations must respect real-time order: a read that
// starts after a write finished must see it.
func TestRealTimeOrderEnforced(t *testing.T) {
	hist := []Op{
		{ID: 1, Kind: OpWrite, Val: 1, Invoke: 0, Respond: 2},
		{ID: 2, Kind: OpRead, Val: 0, Invoke: 3, Respond: 4}, // stale: must fail
	}
	res := mustVerdict(t, hist, 0, NotLinearizable)
	if res.Reason == "" {
		t.Fatal("counterexample evidence (reason) is empty")
	}

	hist[1].Val = 1 // fresh read of the same interval passes
	res = mustVerdict(t, hist, 0, Linearizable)
	if err := VerifyLinearization(hist, res.Witness, 0); err != nil {
		t.Fatalf("witness does not verify: %v", err)
	}
}

// Overlapping writes may be linearized in either order: a later read
// returning either value is fine.
func TestOverlappingWritesAnyOrder(t *testing.T) {
	base := []Op{
		{ID: 1, Kind: OpWrite, Val: 1, Invoke: 0, Respond: 5},
		{ID: 2, Kind: OpWrite, Val: 2, Invoke: 1, Respond: 6},
	}
	for _, v := range []int{1, 2} {
		hist := append(append([]Op{}, base...),
			Op{ID: 3, Kind: OpRead, Val: v, Invoke: 7, Respond: 8})
		mustVerdict(t, hist, 0, Linearizable)
	}
	// But a read returning the initial value sees neither write: illegal.
	hist := append(append([]Op{}, base...),
		Op{ID: 3, Kind: OpRead, Val: 0, Invoke: 7, Respond: 8})
	mustVerdict(t, hist, 0, NotLinearizable)
}

// A read of a value nobody wrote is an immediate counterexample.
func TestNeverWrittenValue(t *testing.T) {
	hist := []Op{
		{ID: 1, Kind: OpWrite, Val: 1, Invoke: 0, Respond: 2},
		{ID: 2, Kind: OpRead, Val: 9, Invoke: 1, Respond: 3},
	}
	res := mustVerdict(t, hist, 0, NotLinearizable)
	if got := res.Reason; got == "" {
		t.Fatal("expected counterexample evidence")
	}
}

// Pending calls use the completion/removal extension semantics.
func TestPendingWriteJustifiesRead(t *testing.T) {
	// Read returns 1; only the pending write of 1 can justify it.
	hist := []Op{
		{ID: 1, Kind: OpWrite, Val: 1, Invoke: 0, Pending: true},
		{ID: 2, Kind: OpRead, Val: 1, Invoke: 1, Respond: 3},
	}
	res := mustVerdict(t, hist, 0, Linearizable)
	if err := VerifyLinearization(hist, res.Witness, 0); err != nil {
		t.Fatalf("witness does not verify: %v", err)
	}
}

func TestPendingWriteCanBeDropped(t *testing.T) {
	// Read returns the initial value; the pending write must be dropped
	// (or placed after the read) for the history to be legal.
	hist := []Op{
		{ID: 1, Kind: OpWrite, Val: 5, Invoke: 0, Pending: true},
		{ID: 2, Kind: OpRead, Val: 0, Invoke: 1, Respond: 3},
	}
	mustVerdict(t, hist, 0, Linearizable)
}

func TestPendingReadIsWildcard(t *testing.T) {
	// The pending read cannot constrain anything; the completed part is
	// what decides the verdict.
	good := []Op{
		{ID: 1, Kind: OpWrite, Val: 2, Invoke: 0, Respond: 2},
		{ID: 2, Kind: OpRead, Val: 2, Invoke: 3, Respond: 4},
		{ID: 3, Kind: OpRead, Invoke: 5, Pending: true},
	}
	res := mustVerdict(t, good, 0, Linearizable)
	if len(res.Witness) != 3 {
		t.Fatalf("witness should include the pending read, got %d ops", len(res.Witness))
	}
	bad := []Op{
		{ID: 1, Kind: OpWrite, Val: 2, Invoke: 0, Respond: 2},
		{ID: 2, Kind: OpRead, Val: 0, Invoke: 3, Respond: 4}, // stale
		{ID: 3, Kind: OpRead, Invoke: 5, Pending: true},
	}
	mustVerdict(t, bad, 0, NotLinearizable)
}

// A non-default initial register value is honored.
func TestInitialValue(t *testing.T) {
	hist := []Op{{ID: 1, Kind: OpRead, Val: 42, Invoke: 0, Respond: 1}}
	mustVerdict(t, hist, 42, Linearizable)
	mustVerdict(t, hist, 0, NotLinearizable)
}

// When the budget is exhausted the verdict is UNKNOWN, not a guess.
func TestBudgetExhausted(t *testing.T) {
	res := Checker{Init: 0, Budget: 200}.Check(hardHistory())
	if res.Verdict != Unknown {
		t.Fatalf("verdict = %s, want UNKNOWN", res.Verdict)
	}
	// The same history is decidable with a full budget.
	res = check(t, hardHistory(), 0)
	if res.Verdict != Linearizable {
		t.Fatalf("verdict = %s, want LINEARIZABLE", res.Verdict)
	}
	if err := VerifyLinearization(hardHistory(), res.Witness, 0); err != nil {
		t.Fatalf("witness does not verify: %v", err)
	}
}

// Invalid histories are rejected, not misjudged.
func TestInvalidHistory(t *testing.T) {
	hist := []Op{{ID: 1, Kind: OpRead, Val: 0, Invoke: 5, Respond: 2}}
	if res := check(t, hist, 0); res.Verdict != Unknown {
		t.Fatalf("verdict = %s, want UNKNOWN for invalid input", res.Verdict)
	}
}
