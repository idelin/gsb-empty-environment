package netsec

// WithdrawRequest describes a single withdrawal from the vault.
type WithdrawRequest struct {
	Account string
	Amount  uint64
	// To is the external contract paid by the withdrawal; it may be nil
	// for a plain externally-owned account with no code.
	To Contract
}

// Vault is a DeFi vault contract. All state mutations that depend on
// external calls follow the checks-effects-interactions pattern and are
// journaled so a failure anywhere in the transaction rolls back every
// state change made by that transaction.
type Vault struct {
	engine *Engine

	balances  map[string]uint64
	withdrawn map[uint64]map[string]uint64 // block -> account -> amount
	deposited uint64
	paidOut   uint64
	block     uint64

	locked  bool
	journal []func()
}

// NewVault returns a Vault executed by the given engine.
func NewVault(e *Engine) *Vault {
	return &Vault{
		engine:    e,
		balances:  make(map[string]uint64),
		withdrawn: make(map[uint64]map[string]uint64),
	}
}

// SetBlock advances the vault's block number. Withdrawal accounting is
// tracked per (block, account) pair.
func (v *Vault) SetBlock(n uint64) { v.block = n }

// Deposit credits amount to account. Deposits do not perform external
// calls and therefore need no reentrancy protection.
func (v *Vault) Deposit(account string, amount uint64) {
	v.balances[account] += amount
	v.deposited += amount
}

// BalanceOf returns the current balance of account.
func (v *Vault) BalanceOf(account string) uint64 { return v.balances[account] }

// WithdrawnInBlock returns the amount account has successfully withdrawn
// in the given block.
func (v *Vault) WithdrawnInBlock(block uint64, account string) uint64 {
	return v.withdrawn[block][account]
}

// TotalDeposited returns the cumulative deposited amount.
func (v *Vault) TotalDeposited() uint64 { return v.deposited }

// TotalPaidOut returns the cumulative amount paid out by committed
// withdrawals. The conservation invariant is:
//
//	sum(balances) + TotalPaidOut == TotalDeposited
func (v *Vault) TotalPaidOut() uint64 { return v.paidOut }

// Withdraw pays amount from account to the given contract. It is a
// convenience wrapper around BatchWithdraw for a single request.
func (v *Vault) Withdraw(account string, amount uint64, to Contract) error {
	return v.BatchWithdraw(WithdrawRequest{Account: account, Amount: amount, To: to})
}

// BatchWithdraw executes one or more withdrawals atomically: either all
// of them commit or every state change is rolled back. The whole batch
// runs under the reentrancy lock, and each request is processed in
// checks-effects-interactions order.
func (v *Vault) BatchWithdraw(reqs ...WithdrawRequest) (err error) {
	if v.locked {
		// A reentrant call arrived while a withdrawal is in flight.
		// Tolerate a bounded number of attempts, then abort the
		// transaction entirely.
		if rerr := v.engine.noteReentry(); rerr != nil {
			return rerr
		}
		return ErrReentrancy
	}
	v.locked = true
	defer func() { v.locked = false }()

	checkpoint := len(v.journal)
	defer func() {
		if err != nil {
			// Failure: undo every effect applied since the checkpoint,
			// in reverse order.
			for i := len(v.journal) - 1; i >= checkpoint; i-- {
				v.journal[i]()
			}
		}
		v.journal = v.journal[:checkpoint]
	}()

	for _, req := range reqs {
		if err = v.withdrawCEI(req); err != nil {
			return err
		}
	}
	return nil
}

// withdrawCEI executes a single withdrawal in checks-effects-interactions
// order. The caller must hold the reentrancy lock.
func (v *Vault) withdrawCEI(req WithdrawRequest) error {
	// Checks: validate the request before touching any state.
	if req.Amount == 0 {
		return ErrZeroAmount
	}
	if v.balances[req.Account] < req.Amount {
		return ErrInsufficient
	}

	// Effects: update all state BEFORE any external call, and record
	// the undo operation so a later failure can roll it back.
	account := req.Account
	amount := req.Amount
	block := v.block
	v.balances[account] -= amount
	v.paidOut += amount
	if v.withdrawn[block] == nil {
		v.withdrawn[block] = make(map[string]uint64)
	}
	v.withdrawn[block][account] += amount
	v.journal = append(v.journal, func() {
		v.balances[account] += amount
		v.paidOut -= amount
		v.withdrawn[block][account] -= amount
	})

	// Interactions: only now perform the metered external call.
	if req.To != nil {
		if err := v.engine.Call(req.To, v, amount); err != nil {
			return err
		}
	}
	return nil
}
