package netsec

import (
	"errors"
	"fmt"
	"testing"
)

// attacker is a malicious contract whose receive callback re-enters the
// vault to withdraw again within the same transaction.
type attacker struct {
	account  string
	amount   uint64
	attempts int // reentry attempts per callback
	swallow  bool // swallow the reentry error instead of propagating it

	calls         int
	balanceSeen   []uint64 // vault balance observed inside each callback
	reentryErrors []error
}

func (a *attacker) OnReceive(e *Engine, v *Vault, amount uint64) error {
	a.calls++
	a.balanceSeen = append(a.balanceSeen, v.BalanceOf(a.account))
	var first error
	for i := 0; i < a.attempts; i++ {
		err := v.Withdraw(a.account, a.amount, nil)
		a.reentryErrors = append(a.reentryErrors, err)
		if errors.Is(err, ErrTooManyReentries) {
			return err // transaction-level abort, must propagate
		}
		if err != nil && first == nil {
			first = err
		}
	}
	if a.swallow {
		return nil
	}
	return first
}

// relay forwards the call to the next contract, building deep call chains.
type relay struct{ next Contract }

func (r *relay) OnReceive(e *Engine, v *Vault, amount uint64) error {
	if r.next == nil {
		return nil
	}
	return e.Call(r.next, v, amount)
}

// failer always fails its callback, simulating a failed external transfer.
type failer struct{ err error }

func (f *failer) OnReceive(e *Engine, v *Vault, amount uint64) error { return f.err }

// honest is a well-behaved receiver that records what it observes.
type honest struct {
	received    uint64
	balanceSeen []uint64
}

func (h *honest) OnReceive(e *Engine, v *Vault, amount uint64) error {
	h.received += amount
	return nil
}

// observer records the vault balance of account at callback time.
type observer struct {
	account     string
	balanceSeen []uint64
}

func (o *observer) OnReceive(e *Engine, v *Vault, amount uint64) error {
	o.balanceSeen = append(o.balanceSeen, v.BalanceOf(o.account))
	return nil
}

func TestReentrantCallbackBlocked(t *testing.T) {
	e := NewEngine()
	v := NewVault(e)
	v.SetBlock(1)

	atk := &attacker{account: "attacker", amount: 100, attempts: 1}
	v.Deposit("attacker", 100)

	e.BeginTx()
	err := v.Withdraw("attacker", 100, atk)
	if !errors.Is(err, ErrReentrancy) {
		t.Fatalf("expected ErrReentrancy, got %v", err)
	}
	if len(atk.reentryErrors) != 1 || !errors.Is(atk.reentryErrors[0], ErrReentrancy) {
		t.Fatalf("reentry was not blocked: %v", atk.reentryErrors)
	}
	// Effects happened before the interaction: the callback must already
	// observe the debited balance.
	if len(atk.balanceSeen) != 1 || atk.balanceSeen[0] != 0 {
		t.Fatalf("state not updated before external call: %v", atk.balanceSeen)
	}
	// The failed transaction rolled back every state change.
	if got := v.BalanceOf("attacker"); got != 100 {
		t.Fatalf("balance not rolled back: got %d, want 100", got)
	}
	if got := v.TotalPaidOut(); got != 0 {
		t.Fatalf("paidOut not rolled back: got %d, want 0", got)
	}
	if got := v.WithdrawnInBlock(1, "attacker"); got != 0 {
		t.Fatalf("withdrawn not rolled back: got %d, want 0", got)
	}
}

func TestReentrantCallbackSwallowedCannotOverdraw(t *testing.T) {
	e := NewEngine()
	v := NewVault(e)
	v.SetBlock(1)

	// Even if the attacker ignores the blocked reentry and lets the outer
	// call succeed, it can only ever receive its own balance once.
	atk := &attacker{account: "attacker", amount: 100, attempts: 3, swallow: true}
	v.Deposit("attacker", 100)

	e.BeginTx()
	if err := v.Withdraw("attacker", 100, atk); err != nil {
		t.Fatalf("outer withdraw should succeed: %v", err)
	}
	for i, err := range atk.reentryErrors {
		if !errors.Is(err, ErrReentrancy) {
			t.Fatalf("reentry %d not blocked: %v", i, err)
		}
	}
	if got := v.BalanceOf("attacker"); got != 0 {
		t.Fatalf("balance: got %d, want 0", got)
	}
	if got := v.TotalPaidOut(); got != 100 {
		t.Fatalf("paidOut: got %d, want 100 (no over-withdrawal)", got)
	}
}

func TestReentryBudgetExhausted(t *testing.T) {
	e := NewEngine()
	v := NewVault(e)
	v.SetBlock(1)

	atk := &attacker{account: "attacker", amount: 10, attempts: e.MaxReentries + 5}
	v.Deposit("attacker", 100)

	e.BeginTx()
	err := v.Withdraw("attacker", 10, atk)
	if !errors.Is(err, ErrTooManyReentries) {
		t.Fatalf("expected ErrTooManyReentries, got %v", err)
	}
	if got := v.BalanceOf("attacker"); got != 100 {
		t.Fatalf("balance not rolled back: got %d, want 100", got)
	}
	if got := v.TotalPaidOut(); got != 0 {
		t.Fatalf("paidOut not rolled back: got %d, want 0", got)
	}
}

func TestDepthAttackBlocked(t *testing.T) {
	e := NewEngine()
	e.MaxDepth = 4
	v := NewVault(e)
	v.SetBlock(1)

	// Chain of relays deeper than MaxDepth: vault -> r1 -> ... -> r6.
	var head Contract = &relay{}
	for i := 0; i < 6; i++ {
		head = &relay{next: head}
	}
	v.Deposit("alice", 50)

	e.BeginTx()
	err := v.Withdraw("alice", 50, head)
	if !errors.Is(err, ErrMaxDepth) {
		t.Fatalf("expected ErrMaxDepth, got %v", err)
	}
	if got := v.BalanceOf("alice"); got != 50 {
		t.Fatalf("balance not rolled back: got %d, want 50", got)
	}
	if got := v.TotalPaidOut(); got != 0 {
		t.Fatalf("paidOut not rolled back: got %d, want 0", got)
	}
}

func TestGasLimitEnforced(t *testing.T) {
	e := NewEngine()
	e.GasLimit = 3 * e.CallGasCost // room for exactly 3 external calls
	v := NewVault(e)
	v.SetBlock(1)

	var head Contract = &relay{}
	for i := 0; i < 5; i++ {
		head = &relay{next: head}
	}
	v.Deposit("alice", 50)

	e.BeginTx()
	err := v.Withdraw("alice", 50, head)
	if !errors.Is(err, ErrOutOfGas) {
		t.Fatalf("expected ErrOutOfGas, got %v", err)
	}
	if got := v.BalanceOf("alice"); got != 50 {
		t.Fatalf("balance not rolled back: got %d, want 50", got)
	}
}

func TestPartialFailureRollsBackEverything(t *testing.T) {
	e := NewEngine()
	v := NewVault(e)
	v.SetBlock(7)

	good := &honest{}
	bad := &failer{err: errors.New("transfer rejected")}
	v.Deposit("alice", 100)
	v.Deposit("bob", 100)

	e.BeginTx()
	err := v.BatchWithdraw(
		WithdrawRequest{Account: "alice", Amount: 60, To: good},
		WithdrawRequest{Account: "bob", Amount: 40, To: bad},
	)
	if err == nil {
		t.Fatal("expected batch to fail")
	}
	// Alice's withdrawal succeeded internally, but the batch failure must
	// roll it back along with every other state change.
	if got := v.BalanceOf("alice"); got != 100 {
		t.Fatalf("alice balance not rolled back: got %d, want 100", got)
	}
	if got := v.BalanceOf("bob"); got != 100 {
		t.Fatalf("bob balance not rolled back: got %d, want 100", got)
	}
	if got := v.TotalPaidOut(); got != 0 {
		t.Fatalf("paidOut not rolled back: got %d, want 0", got)
	}
	if got := v.WithdrawnInBlock(7, "alice"); got != 0 {
		t.Fatalf("alice withdrawn not rolled back: got %d, want 0", got)
	}
}

func TestLegitimateWithdrawal(t *testing.T) {
	e := NewEngine()
	v := NewVault(e)
	v.SetBlock(3)

	obs := &observer{account: "carol"}
	v.Deposit("carol", 200)

	e.BeginTx()
	if err := v.Withdraw("carol", 70, obs); err != nil {
		t.Fatalf("legitimate withdrawal failed: %v", err)
	}
	if got := v.BalanceOf("carol"); got != 130 {
		t.Fatalf("balance: got %d, want 130", got)
	}
	if got := v.TotalPaidOut(); got != 70 {
		t.Fatalf("paidOut: got %d, want 70", got)
	}
	if got := v.WithdrawnInBlock(3, "carol"); got != 70 {
		t.Fatalf("withdrawn in block 3: got %d, want 70", got)
	}
	// Checks-effects-interactions: the external call observes the
	// already-updated balance.
	if len(obs.balanceSeen) != 1 || obs.balanceSeen[0] != 130 {
		t.Fatalf("callback observed stale balance: %v", obs.balanceSeen)
	}

	// A second withdrawal by the same account in the same block is
	// processed against the updated state.
	e.BeginTx()
	if err := v.Withdraw("carol", 30, nil); err != nil {
		t.Fatalf("second withdrawal failed: %v", err)
	}
	if got := v.WithdrawnInBlock(3, "carol"); got != 100 {
		t.Fatalf("same-block withdrawn: got %d, want 100", got)
	}
	// Per-block accounting resets on a new block.
	v.SetBlock(4)
	e.BeginTx()
	if err := v.Withdraw("carol", 20, nil); err != nil {
		t.Fatalf("next-block withdrawal failed: %v", err)
	}
	if got := v.WithdrawnInBlock(4, "carol"); got != 20 {
		t.Fatalf("block 4 withdrawn: got %d, want 20", got)
	}
	if got := v.WithdrawnInBlock(3, "carol"); got != 100 {
		t.Fatalf("block 3 withdrawn changed: got %d, want 100", got)
	}
}

func TestWithdrawValidation(t *testing.T) {
	e := NewEngine()
	v := NewVault(e)
	v.Deposit("dave", 10)

	e.BeginTx()
	if err := v.Withdraw("dave", 0, nil); !errors.Is(err, ErrZeroAmount) {
		t.Fatalf("expected ErrZeroAmount, got %v", err)
	}
	e.BeginTx()
	if err := v.Withdraw("dave", 11, nil); !errors.Is(err, ErrInsufficient) {
		t.Fatalf("expected ErrInsufficient, got %v", err)
	}
	if got := v.BalanceOf("dave"); got != 10 {
		t.Fatalf("balance changed by failed withdrawals: got %d, want 10", got)
	}
}

// TestBalanceReconciliation hammers the vault with a deterministic mix of
// deposits, legitimate withdrawals, reentrancy attacks and failing
// batches, then verifies the conservation invariant: no account can ever
// extract more than was deposited.
func TestBalanceReconciliation(t *testing.T) {
	e := NewEngine()
	v := NewVault(e)

	accounts := []string{"alice", "bob", "carol", "mallory"}
	deposited := map[string]uint64{}

	checkInvariant := func(step string) {
		t.Helper()
		var sum uint64
		for _, a := range accounts {
			sum += v.BalanceOf(a)
		}
		if sum+v.TotalPaidOut() != v.TotalDeposited() {
			t.Fatalf("%s: invariant broken: balances=%d paidOut=%d deposited=%d",
				step, sum, v.TotalPaidOut(), v.TotalDeposited())
		}
	}

	mallory := &attacker{account: "mallory", amount: 25, attempts: 2}

	step := 0
	for block := uint64(1); block <= 5; block++ {
		v.SetBlock(block)
		for i, a := range accounts {
			amount := uint64(10 * (i + 1))
			v.Deposit(a, amount)
			deposited[a] += amount
		}
		checkInvariant(fmt.Sprintf("block %d deposits", block))

		// Legitimate withdrawals.
		e.BeginTx()
		if err := v.Withdraw("alice", 5, &honest{}); err != nil {
			t.Fatalf("block %d: alice withdraw: %v", block, err)
		}
		step++
		checkInvariant(fmt.Sprintf("step %d alice withdraw", step))

		// Reentrancy attack by mallory: always fails, never over-withdraws.
		e.BeginTx()
		if err := v.Withdraw("mallory", 25, mallory); !errors.Is(err, ErrReentrancy) {
			t.Fatalf("block %d: expected ErrReentrancy, got %v", block, err)
		}
		checkInvariant(fmt.Sprintf("block %d attack", block))

		// Atomic batch with a failing leg: full rollback.
		e.BeginTx()
		err := v.BatchWithdraw(
			WithdrawRequest{Account: "bob", Amount: 8, To: &honest{}},
			WithdrawRequest{Account: "carol", Amount: 9, To: &failer{err: errors.New("boom")}},
		)
		if err == nil {
			t.Fatalf("block %d: expected batch failure", block)
		}
		checkInvariant(fmt.Sprintf("block %d failing batch", block))

		// Successful batch.
		e.BeginTx()
		if err := v.BatchWithdraw(
			WithdrawRequest{Account: "bob", Amount: 8, To: &honest{}},
			WithdrawRequest{Account: "carol", Amount: 9, To: &honest{}},
		); err != nil {
			t.Fatalf("block %d: good batch: %v", block, err)
		}
		checkInvariant(fmt.Sprintf("block %d good batch", block))
	}

	// No account extracted more than it deposited; mallory's attacks
	// gained nothing.
	for _, a := range accounts {
		var out uint64
		for block := uint64(1); block <= 5; block++ {
			out += v.WithdrawnInBlock(block, a)
		}
		if out > deposited[a] {
			t.Fatalf("%s withdrew %d but only deposited %d", a, out, deposited[a])
		}
		if a == "mallory" && out != 0 {
			t.Fatalf("attacker extracted %d despite reentrancy guard", out)
		}
	}
}
