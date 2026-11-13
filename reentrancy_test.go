package netsec

import (
	"reflect"
	"testing"
)

func vulnerableProgram() Program {
	return Program{
		Check(2),
		Return(),
		CashSend(),
		ExternalCallback(ContinueOnChildFailure),
		SubBalance(),
		Return(),
	}
}

func safeDeductFirstProgram() Program {
	return Program{
		Check(2),
		Return(),
		SubBalance(),
		CashSend(),
		ExternalCallback(ContinueOnChildFailure),
		Return(),
	}
}

func TestSearchFindsShortestReentrancyAttackAndReplays(t *testing.T) {
	cfg := DefaultConfig(100, 255, []int{100})
	result, err := Search(vulnerableProgram(), cfg)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if result.Status != StatusAttack {
		t.Fatalf("status = %s (%s), want attack", result.Status, result.Reason)
	}
	attack := result.Attack
	if attack.Calls != 2 {
		t.Fatalf("calls = %d, want 2", attack.Calls)
	}
	if attack.Depth != 2 {
		t.Fatalf("depth = %d, want 2", attack.Depth)
	}
	wantSeq := []int{100, 100}
	if !reflect.DeepEqual(attack.AmountSequence, wantSeq) {
		t.Fatalf("amount sequence = %v, want %v", attack.AmountSequence, wantSeq)
	}
	if attack.Received != 200 || attack.Final.Balance != 0 || attack.Final.Cash != 55 {
		t.Fatalf("final received/balance/cash = %d/%d/%d, want 200/0/55",
			attack.Received, attack.Final.Balance, attack.Final.Cash)
	}
	if attack.Steps != 10 {
		t.Fatalf("steps = %d, want 10", attack.Steps)
	}
	if len(attack.Execution.Transfers) != 2 {
		t.Fatalf("transfers = %d, want 2", len(attack.Execution.Transfers))
	}
	for _, transfer := range attack.Execution.Transfers {
		if transfer.Status != TransferCommitted {
			t.Fatalf("transfer %d status = %s, want committed", transfer.Index, transfer.Status)
		}
	}

	replayed, err := Replay(vulnerableProgram(), cfg, attack.Plan)
	if err != nil {
		t.Fatalf("Replay: %v", err)
	}
	if !replayed.Attack || replayed.Received != attack.Received ||
		replayed.Steps != attack.Steps || replayed.Calls != attack.Calls {
		t.Fatalf("independent replay diverged: attack=%v received=%d steps=%d calls=%d",
			replayed.Attack, replayed.Received, replayed.Steps, replayed.Calls)
	}
	if !reflect.DeepEqual(replayed.AmountSequence, attack.AmountSequence) {
		t.Fatalf("replay sequence = %v, want %v", replayed.AmountSequence, attack.AmountSequence)
	}
	if replayed.Final != attack.Final {
		t.Fatalf("replay final = %+v, want %+v", replayed.Final, attack.Final)
	}

	report, err := VerifyReplay(vulnerableProgram(), cfg, attack)
	if err != nil {
		t.Fatalf("VerifyReplay: %v", err)
	}
	if !report.Matches || len(report.Issues) != 0 {
		t.Fatalf("VerifyReplay mismatch: %+v", report.Issues)
	}
}

func TestSearchTieBreaksByAmountSequence(t *testing.T) {
	cfg := DefaultConfig(100, 255, []int{100, 50, 10})
	result, err := Search(vulnerableProgram(), cfg)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if result.Status != StatusAttack || result.Attack.Calls != 2 {
		t.Fatalf("status/calls = %s/%d, want attack/2", result.Status, result.Attack.Calls)
	}
	want := []int{10, 100}
	if !reflect.DeepEqual(result.Attack.AmountSequence, want) {
		t.Fatalf("amount sequence = %v, want lexicographically first %v",
			result.Attack.AmountSequence, want)
	}
	if result.Attack.Received != 110 {
		t.Fatalf("received = %d, want 110", result.Attack.Received)
	}
}

func TestSearchFindsThreeCallSequentialCallbackTree(t *testing.T) {
	program := Program{
		Check(2),
		Return(),
		CashSend(),
		ExternalCallback(ContinueOnChildFailure),
		ExternalCallback(ContinueOnChildFailure),
		SubBalance(),
		Return(),
	}
	cfg := DefaultConfig(100, 255, []int{40})
	result, err := Search(program, cfg)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if result.Status != StatusAttack {
		t.Fatalf("status = %s (%s), want attack", result.Status, result.Reason)
	}
	if result.Attack.Calls != 3 || result.Attack.Received != 120 {
		t.Fatalf("calls/received = %d/%d, want 3/120", result.Attack.Calls, result.Attack.Received)
	}
	if result.Attack.Depth != 2 {
		t.Fatalf("depth = %d, want 2 (sequential siblings share depth 2)", result.Attack.Depth)
	}
	if !reflect.DeepEqual(result.Attack.AmountSequence, []int{40, 40, 40}) {
		t.Fatalf("amount sequence = %v, want [40 40 40]", result.Attack.AmountSequence)
	}
	report, err := VerifyReplay(program, cfg, result.Attack)
	if err != nil || !report.Matches {
		t.Fatalf("VerifyReplay: err=%v report=%+v", err, report)
	}
}

func TestDeductBeforeCallIsSafe(t *testing.T) {
	cfg := DefaultConfig(100, 255, []int{100, 50, 10})
	result, err := Search(safeDeductFirstProgram(), cfg)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if result.Status != StatusSafe {
		t.Fatalf("status = %s (%s), want safe", result.Status, result.Reason)
	}

	plan := Plan{RootAmount: 100, Decisions: []Decision{Enter(100)}}
	execution, err := ExecutePlan(safeDeductFirstProgram(), cfg, plan)
	if err != nil {
		t.Fatalf("ExecutePlan: %v", err)
	}
	if execution.Attack || execution.Received > 100 {
		t.Fatalf("safe program attack=%v received=%d", execution.Attack, execution.Received)
	}
}

func TestDepthLimitBlocksReentry(t *testing.T) {
	cfg := DefaultConfig(100, 255, []int{100})
	cfg.MaxDepth = 1
	result, err := Search(vulnerableProgram(), cfg)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if result.Status != StatusSafe {
		t.Fatalf("status = %s (%s), want safe when reentry is disabled", result.Status, result.Reason)
	}
}

func TestParentRevertRollsBackSuccessfulChildTransfers(t *testing.T) {
	program := Program{
		Check(2),
		Return(),
		CashSend(),
		ExternalCallback(ContinueOnChildFailure),
		AddBalance(),
		AddBalance(),
		Return(),
	}
	cfg := DefaultConfig(100, 255, []int{100, 10})
	plan := Plan{RootAmount: 100, Decisions: []Decision{Enter(10), ReturnNow()}}
	execution, err := ExecutePlan(program, cfg, plan)
	if err != nil {
		t.Fatalf("ExecutePlan: %v", err)
	}

	if execution.Outcome != OutcomeReverted {
		t.Fatalf("outcome = %s, want reverted", execution.Outcome)
	}
	if execution.Attack {
		t.Fatalf("transient received %d was incorrectly reported as a committed attack", execution.Received)
	}
	if execution.Received != 0 || execution.Final.Balance != 100 || execution.Final.Cash != 255 {
		t.Fatalf("final received/balance/cash = %d/%d/%d, want 0/100/255",
			execution.Received, execution.Final.Balance, execution.Final.Cash)
	}

	child := execution.Root.Callbacks[0].Child
	if child == nil || child.Outcome != OutcomeReturned {
		t.Fatalf("child outcome = %v, want returned success before ancestor revert", child)
	}
	if execution.Root.BeforeRollback == nil || execution.Root.BeforeRollback.Received != 110 {
		t.Fatalf("root before-rollback snapshot = %+v, want transient received 110", execution.Root.BeforeRollback)
	}
	evidence := execution.RollbackEvidence()
	if len(evidence) != 1 {
		t.Fatalf("rollback evidence layers = %d, want 1 ancestor rollback", len(evidence))
	}
	if len(evidence[0].Transfers) != 2 {
		t.Fatalf("rolled-back transfers = %d, want root and child transfer", len(evidence[0].Transfers))
	}
	countRolled := 0
	for _, transfer := range execution.Transfers {
		if transfer.Status == TransferRolledBack {
			countRolled++
		}
	}
	if countRolled != 2 {
		t.Fatalf("rolled-back journal entries = %d, want 2", countRolled)
	}
}

func TestSearchDoesNotCountRolledBackTransientTransfer(t *testing.T) {
	program := Program{
		Check(2),
		Return(),
		CashSend(),
		ExternalCallback(ContinueOnChildFailure),
		AddBalance(),
		AddBalance(),
		Return(),
	}
	cfg := DefaultConfig(100, 255, []int{100, 10})
	result, err := Search(program, cfg)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if result.Status != StatusSafe {
		t.Fatalf("status = %s (%s), want safe; transient received must not count",
			result.Status, result.Reason)
	}
}

func TestChildRevertRollsBackChildAndParentContinues(t *testing.T) {
	program := Program{
		Check(2),
		Return(),
		CashSend(),
		ExternalCallback(ContinueOnChildFailure),
		AddBalance(),
		AddBalance(),
		AddBalance(),
		Return(),
	}
	cfg := DefaultConfig(100, 255, []int{10, 100})
	plan := Plan{RootAmount: 10, Decisions: []Decision{Enter(100), ReturnNow()}}
	execution, err := ExecutePlan(program, cfg, plan)
	if err != nil {
		t.Fatalf("ExecutePlan: %v", err)
	}
	if execution.Outcome != OutcomeReturned {
		t.Fatalf("root outcome = %s, want returned", execution.Outcome)
	}
	child := execution.Root.Callbacks[0].Child
	if child == nil || child.Outcome != OutcomeReverted || child.Rollback == nil {
		t.Fatalf("child = %+v, want reverted child with rollback evidence", child)
	}
	if len(child.Rollback.Transfers) != 1 || child.Rollback.Transfers[0].Amount != 100 {
		t.Fatalf("child rollback transfers = %+v, want one 100 transfer", child.Rollback.Transfers)
	}
	if execution.Received != 10 {
		t.Fatalf("final received = %d, want only parent's committed 10", execution.Received)
	}
	if execution.Final.Balance != 130 || execution.Final.Cash != 245 {
		t.Fatalf("final balance/cash = %d/%d, want 130/245",
			execution.Final.Balance, execution.Final.Cash)
	}
	if execution.Transfers[1].Status != TransferRolledBack {
		t.Fatalf("child journal status = %s, want rolled_back", execution.Transfers[1].Status)
	}
}

func TestRevertOnChildFailureRollsBackParent(t *testing.T) {
	program := Program{
		Check(2),
		Return(),
		CashSend(),
		ExternalCallback(RevertOnChildFailure),
		AddBalance(),
		AddBalance(),
		Return(),
	}
	cfg := DefaultConfig(100, 255, []int{10, 100})
	plan := Plan{RootAmount: 10, Decisions: []Decision{Enter(100), ReturnNow()}}
	execution, err := ExecutePlan(program, cfg, plan)
	if err != nil {
		t.Fatalf("ExecutePlan: %v", err)
	}
	if execution.Outcome != OutcomeReverted || execution.Attack {
		t.Fatalf("outcome/attack = %s/%v, want reverted/false", execution.Outcome, execution.Attack)
	}
	if execution.Received != 0 || execution.Final.Cash != 255 {
		t.Fatalf("received/cash = %d/%d, want 0/255", execution.Received, execution.Final.Cash)
	}
	if len(execution.Root.Rollback.Transfers) != 1 ||
		execution.Root.Rollback.Transfers[0].Amount != 10 {
		t.Fatalf("parent rollback transfers = %+v, want its own 10 transfer",
			execution.Root.Rollback.Transfers)
	}
}

func TestInsufficientCashRevertsAndCommitsNothing(t *testing.T) {
	program := Program{Check(2), Return(), CashSend(), Return()}
	cfg := Config{InitialBalance: 100, ContractCash: 50}
	execution, err := ExecutePlan(program, cfg, Plan{RootAmount: 100})
	if err != nil {
		t.Fatalf("ExecutePlan: %v", err)
	}
	if execution.Outcome != OutcomeReverted || execution.Attack {
		t.Fatalf("outcome/attack = %s/%v, want reverted/false", execution.Outcome, execution.Attack)
	}
	if len(execution.Transfers) != 0 || execution.Received != 0 {
		t.Fatalf("transfers/received = %d/%d, want 0/0", len(execution.Transfers), execution.Received)
	}
	if execution.Final.Cash != 50 {
		t.Fatalf("cash = %d, want original 50", execution.Final.Cash)
	}
}

func TestCallbackRefusalDoesNotAttack(t *testing.T) {
	cfg := DefaultConfig(100, 255, []int{100})
	execution, err := ExecutePlan(vulnerableProgram(), cfg, Plan{
		RootAmount: 100,
		Decisions:  []Decision{ReturnNow()},
	})
	if err != nil {
		t.Fatalf("ExecutePlan: %v", err)
	}
	if execution.Attack || execution.Received != 100 || execution.Calls != 1 {
		t.Fatalf("attack/received/calls = %v/%d/%d, want false/100/1",
			execution.Attack, execution.Received, execution.Calls)
	}
}

func TestStepBudgetIsUnknownNotAttack(t *testing.T) {
	program := Program{Check(0), Return()}
	cfg := DefaultConfig(100, 255, []int{0})
	result, err := Search(program, cfg)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if result.Status != StatusUnknown {
		t.Fatalf("status = %s (%s), want unknown", result.Status, result.Reason)
	}
	if result.Attack != nil {
		t.Fatalf("unknown execution was reported as an attack")
	}
}

func TestProgramValidationLimits(t *testing.T) {
	var tooLong Program
	for i := 0; i < MaxInstructions+1; i++ {
		tooLong = append(tooLong, Return())
	}
	if err := tooLong.Validate(); err == nil {
		t.Fatalf("program longer than %d validated", MaxInstructions)
	}
	if err := (Program{Check(-1), Return()}).Validate(); err == nil {
		t.Fatalf("negative branch target validated")
	}
	if _, err := DefaultConfig(100, 255, []int{1, 2, 3, 4, 5, 6, 7, 8, 9}).normalized(); err == nil {
		t.Fatalf("nine withdrawal kinds accepted")
	}
}
