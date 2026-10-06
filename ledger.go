// Package ledger implements an account model that keeps available, frozen
// and settled amounts strictly separated. Each freeze is bound to a currency
// and a client request id; after a partial settlement only the remaining
// frozen amount can be released. Concurrent settle/release operations are
// arbitrated by optimistic version commits. Batch cross-account operations
// execute atomically under a per-currency conservation model, and the
// operation journal supports fault replay with duplicate-receipt detection.
package ledger

import (
	"errors"
	"fmt"
	"sort"
	"sync"
)

// Money is an amount in the smallest unit of a currency.
type Money int64

// OpKind identifies the kind of a ledger operation.
type OpKind int

const (
	OpFund OpKind = iota
	OpFreeze
	OpSettle
	OpRelease
)

func (k OpKind) String() string {
	switch k {
	case OpFund:
		return "fund"
	case OpFreeze:
		return "freeze"
	case OpSettle:
		return "settle"
	case OpRelease:
		return "release"
	}
	return "unknown"
}

var (
	ErrAccountNotFound   = errors.New("ledger: account not found")
	ErrFreezeNotFound    = errors.New("ledger: freeze not found")
	ErrFreezeClosed      = errors.New("ledger: freeze already closed")
	ErrInsufficientFunds = errors.New("ledger: insufficient available funds")
	ErrVersionConflict   = errors.New("ledger: freeze version conflict")
	ErrInvalidAmount     = errors.New("ledger: amount must be positive")
	ErrAmountExceeds     = errors.New("ledger: amount exceeds frozen remainder")
	ErrConservation      = errors.New("ledger: per-currency conservation violated")
)

// Balance holds the three strictly separated amounts of one currency.
type Balance struct {
	Available Money
	Frozen    Money
	Settled   Money
}

// Total returns the conserved sum of the three buckets.
func (b Balance) Total() Money { return b.Available + b.Frozen + b.Settled }

// Freeze is a hold on funds, bound to a currency and a request id.
// Version is bumped on every mutation and is used to arbitrate
// concurrent settle/release commits.
type Freeze struct {
	ReqID     string
	Currency  string
	Remaining Money // still frozen; only this part can be released
	Settled   Money // cumulative settled out of this freeze
	Version   uint64
	Closed    bool
}

// Account holds per-currency balances and open/closed freezes.
type Account struct {
	ID       string
	Balances map[string]*Balance
	freezes  map[string]*Freeze
}

// Op is a single ledger operation. For OpSettle and OpRelease, Version is
// the expected freeze version: the commit is rejected with
// ErrVersionConflict when the freeze has moved on. ReceiptID is the
// idempotency key used for duplicate-receipt detection during replay;
// when empty one is generated from the journal sequence.
type Op struct {
	Kind      OpKind
	Account   string
	ReqID     string
	Currency  string
	Amount    Money
	Version   uint64
	ReceiptID string
}

// Receipt is the committed outcome of an operation.
type Receipt struct {
	ID        string
	Kind      OpKind
	Account   string
	ReqID     string
	Currency  string
	Amount    Money
	Version   uint64 // resulting freeze version (0 for OpFund)
	Duplicate bool   // true when the receipt was already recorded
}

// Entry is a journaled operation, the unit of fault replay.
type Entry struct {
	ReceiptID string
	Op        Op
}

type state struct {
	accounts map[string]*Account
	baseline map[string]Money // conserved total per currency
}

// Engine is the ledger. All methods are safe for concurrent use.
type Engine struct {
	mu       sync.Mutex
	st       *state
	receipts map[string]Receipt
	journal  []Entry
	seq      uint64
}

func NewEngine() *Engine {
	return &Engine{
		st:       &state{accounts: make(map[string]*Account), baseline: make(map[string]Money)},
		receipts: make(map[string]Receipt),
	}
}

// OpenAccount creates an empty account.
func (e *Engine) OpenAccount(id string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if _, ok := e.st.accounts[id]; !ok {
		e.st.accounts[id] = &Account{ID: id, Balances: make(map[string]*Balance), freezes: make(map[string]*Freeze)}
	}
}

// BalanceOf returns a copy of the account's balance for a currency.
func (e *Engine) BalanceOf(account, currency string) (Balance, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	a, ok := e.st.accounts[account]
	if !ok {
		return Balance{}, ErrAccountNotFound
	}
	if b, ok := a.Balances[currency]; ok {
		return *b, nil
	}
	return Balance{}, nil
}

// FreezeOf returns a copy of the freeze bound to reqID.
func (e *Engine) FreezeOf(account, reqID string) (Freeze, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	a, ok := e.st.accounts[account]
	if !ok {
		return Freeze{}, ErrAccountNotFound
	}
	f, ok := a.freezes[reqID]
	if !ok {
		return Freeze{}, ErrFreezeNotFound
	}
	return *f, nil
}

// Apply executes ops as one atomic batch: either every operation commits
// and the per-currency conservation invariant holds, or no state changes.
func (e *Engine) Apply(ops ...Op) ([]Receipt, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.applyLocked(e.st, ops)
}

// Replay re-applies journaled entries after a fault. Entries whose receipt
// id is already recorded are reported as duplicates and skipped, so a
// replayed journal never moves funds twice.
func (e *Engine) Replay(entries []Entry) (duplicates []string, err error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	for _, en := range entries {
		if en.ReceiptID == "" {
			return duplicates, fmt.Errorf("ledger: replay entry missing receipt id")
		}
		if _, ok := e.receipts[en.ReceiptID]; ok {
			duplicates = append(duplicates, en.ReceiptID)
			continue
		}
		op := en.Op
		op.ReceiptID = en.ReceiptID
		if _, err := e.applyLocked(e.st, []Op{op}); err != nil {
			return duplicates, fmt.Errorf("ledger: replay %s: %v", en.ReceiptID, err)
		}
	}
	return duplicates, nil
}

// Journal returns a copy of all committed entries, in commit order.
func (e *Engine) Journal() []Entry {
	e.mu.Lock()
	defer e.mu.Unlock()
	out := make([]Entry, len(e.journal))
	copy(out, e.journal)
	return out
}

// VerifyConservation checks that for every currency the sum of
// available+frozen+settled over all accounts equals the funded baseline.
func (e *Engine) VerifyConservation() error {
	e.mu.Lock()
	defer e.mu.Unlock()
	return verifyConservation(e.st)
}

// applyLocked commits ops atomically against st. On any failure st is
// left untouched.
func (e *Engine) applyLocked(st *state, ops []Op) ([]Receipt, error) {
	work := cloneState(st)
	receipts := make([]Receipt, 0, len(ops))
	pending := make([]Entry, 0, len(ops))

	for _, op := range ops {
		if op.ReceiptID == "" {
			e.seq++
			op.ReceiptID = fmt.Sprintf("auto-%d", e.seq)
		}
		if prev, ok := e.receipts[op.ReceiptID]; ok {
			prev.Duplicate = true
			receipts = append(receipts, prev)
			continue
		}
		rc, err := applyOp(work, op)
		if err != nil {
			return nil, err
		}
		rc.ID = op.ReceiptID
		receipts = append(receipts, rc)
		pending = append(pending, Entry{ReceiptID: op.ReceiptID, Op: op})
	}

	if err := verifyConservation(work); err != nil {
		return nil, err
	}

	e.st = work
	for _, rc := range receipts {
		if !rc.Duplicate {
			e.receipts[rc.ID] = rc
		}
	}
	e.journal = append(e.journal, pending...)
	return receipts, nil
}

func applyOp(st *state, op Op) (Receipt, error) {
	rc := Receipt{Kind: op.Kind, Account: op.Account, ReqID: op.ReqID, Currency: op.Currency, Amount: op.Amount}
	if op.Kind != OpRelease && op.Amount <= 0 {
		return rc, ErrInvalidAmount
	}
	a, ok := st.accounts[op.Account]
	if !ok {
		return rc, ErrAccountNotFound
	}
	bal := a.Balances[op.Currency]
	if bal == nil {
		bal = &Balance{}
		a.Balances[op.Currency] = bal
	}

	switch op.Kind {
	case OpFund:
		// Funding is the only operation that changes the conserved
		// per-currency baseline.
		bal.Available += op.Amount
		st.baseline[op.Currency] += op.Amount

	case OpFreeze:
		if existing, ok := a.freezes[op.ReqID]; ok {
			// Idempotent re-freeze: the request id is already bound.
			rc.Amount = existing.Remaining
			rc.Version = existing.Version
			rc.Duplicate = true
			return rc, nil
		}
		if bal.Available < op.Amount {
			return rc, ErrInsufficientFunds
		}
		bal.Available -= op.Amount
		bal.Frozen += op.Amount
		a.freezes[op.ReqID] = &Freeze{ReqID: op.ReqID, Currency: op.Currency, Remaining: op.Amount, Version: 1}
		rc.Version = 1

	case OpSettle:
		f, err := arbitrate(a, op.ReqID, op.Version)
		if err != nil {
			return rc, err
		}
		if op.Amount > f.Remaining {
			return rc, ErrAmountExceeds
		}
		// Partial settlement: only the settled part moves to settled;
		// the remainder stays frozen and can later be released.
		bal.Frozen -= op.Amount
		bal.Settled += op.Amount
		f.Remaining -= op.Amount
		f.Settled += op.Amount
		f.Version++
		if f.Remaining == 0 {
			f.Closed = true
		}
		rc.Version = f.Version

	case OpRelease:
		f, err := arbitrate(a, op.ReqID, op.Version)
		if err != nil {
			return rc, err
		}
		// Only the remaining frozen amount is released; amounts already
		// settled out of this freeze stay settled.
		released := f.Remaining
		bal.Frozen -= f.Remaining
		bal.Available += f.Remaining
		f.Remaining = 0
		f.Closed = true
		f.Version++
		rc.Amount = released
		rc.Version = f.Version
	}
	return rc, nil
}

// arbitrate resolves concurrent settle/release commits by version: the
// caller must present the version it read, and exactly one commit wins.
func arbitrate(a *Account, reqID string, expected uint64) (*Freeze, error) {
	f, ok := a.freezes[reqID]
	if !ok {
		return nil, ErrFreezeNotFound
	}
	if f.Closed {
		return nil, ErrFreezeClosed
	}
	if f.Version != expected {
		return nil, ErrVersionConflict
	}
	return f, nil
}

func verifyConservation(st *state) error {
	totals := make(map[string]Money)
	for _, a := range st.accounts {
		for cur, b := range a.Balances {
			totals[cur] += b.Total()
		}
	}
	currencies := make(map[string]struct{}, len(totals)+len(st.baseline))
	for cur := range totals {
		currencies[cur] = struct{}{}
	}
	for cur := range st.baseline {
		currencies[cur] = struct{}{}
	}
	sorted := make([]string, 0, len(currencies))
	for cur := range currencies {
		sorted = append(sorted, cur)
	}
	sort.Strings(sorted)
	for _, cur := range sorted {
		if totals[cur] != st.baseline[cur] {
			return fmt.Errorf("%v: %s total=%d baseline=%d", ErrConservation, cur, totals[cur], st.baseline[cur])
		}
	}
	return nil
}

func cloneState(st *state) *state {
	ns := &state{
		accounts: make(map[string]*Account, len(st.accounts)),
		baseline: make(map[string]Money, len(st.baseline)),
	}
	for id, a := range st.accounts {
		na := &Account{
			ID:       a.ID,
			Balances: make(map[string]*Balance, len(a.Balances)),
			freezes:  make(map[string]*Freeze, len(a.freezes)),
		}
		for cur, b := range a.Balances {
			cp := *b
			na.Balances[cur] = &cp
		}
		for rid, f := range a.freezes {
			cp := *f
			na.freezes[rid] = &cp
		}
		ns.accounts[id] = na
	}
	for cur, total := range st.baseline {
		ns.baseline[cur] = total
	}
	return ns
}
