package netsec

import "errors"

var (
	// ErrReentrancy is returned when a contract attempts to re-enter the
	// vault while a withdrawal is still in progress.
	ErrReentrancy = errors.New("netsec: reentrant call blocked")
	// ErrTooManyReentries is returned when the per-transaction reentry
	// attempt budget is exhausted; the whole transaction must abort.
	ErrTooManyReentries = errors.New("netsec: reentry attempt budget exhausted")
	// ErrMaxDepth is returned when the call stack exceeds MaxDepth.
	ErrMaxDepth = errors.New("netsec: max call depth exceeded")
	// ErrOutOfGas is returned when the transaction gas limit is exceeded.
	ErrOutOfGas = errors.New("netsec: out of gas")
	// ErrZeroAmount is returned for non-positive withdrawal amounts.
	ErrZeroAmount = errors.New("netsec: amount must be positive")
	// ErrInsufficient is returned when the balance cannot cover a withdrawal.
	ErrInsufficient = errors.New("netsec: insufficient balance")
)

// Contract is any external contract callable by the execution engine.
type Contract interface {
	// OnReceive is invoked by the engine when the vault pays this contract.
	OnReceive(e *Engine, v *Vault, amount uint64) error
}

// Engine is the contract execution engine. It meters call depth and gas,
// and caps the number of reentry attempts allowed within one transaction.
type Engine struct {
	// MaxDepth is the maximum allowed external-call stack depth.
	MaxDepth int
	// GasLimit is the per-transaction gas budget.
	GasLimit uint64
	// CallGasCost is the gas charged for every external call.
	CallGasCost uint64
	// MaxReentries is the number of blocked reentry attempts tolerated
	// per transaction before the transaction is aborted outright.
	MaxReentries int

	depth     int
	gas       uint64
	reentries int
}

// NewEngine returns an Engine with safe default limits.
func NewEngine() *Engine {
	return &Engine{
		MaxDepth:     8,
		GasLimit:     100_000,
		CallGasCost:  700,
		MaxReentries: 3,
	}
}

// BeginTx resets all per-transaction meters. Call it before executing a
// new top-level transaction.
func (e *Engine) BeginTx() {
	e.depth = 0
	e.gas = 0
	e.reentries = 0
}

// GasUsed reports the gas consumed so far in the current transaction.
func (e *Engine) GasUsed() uint64 { return e.gas }

// Depth reports the current external-call stack depth.
func (e *Engine) Depth() int { return e.depth }

// Call performs a metered external call: it enforces the depth limit,
// charges gas, and then invokes the target contract.
func (e *Engine) Call(c Contract, v *Vault, amount uint64) error {
	if e.depth >= e.MaxDepth {
		return ErrMaxDepth
	}
	e.depth++
	defer func() { e.depth-- }()

	if e.gas+e.CallGasCost > e.GasLimit {
		return ErrOutOfGas
	}
	e.gas += e.CallGasCost

	return c.OnReceive(e, v, amount)
}

// noteReentry records a blocked reentry attempt. Once the per-transaction
// budget is exceeded it returns ErrTooManyReentries so the caller aborts
// the entire transaction instead of merely rejecting the single call.
func (e *Engine) noteReentry() error {
	e.reentries++
	if e.reentries > e.MaxReentries {
		return ErrTooManyReentries
	}
	return nil
}
