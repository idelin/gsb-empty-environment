package sandbox

import "errors"

// Sentinel errors returned by the sandbox. Use errors.Is to match them.
var (
	// ErrBudgetExceeded is returned when the instruction-step budget is exhausted.
	ErrBudgetExceeded = errors.New("BudgetExceeded: instruction step budget exhausted")
	// ErrMemoryExceeded is returned when the allocation budget is exhausted.
	ErrMemoryExceeded = errors.New("MemoryExceeded: allocation budget exhausted")
	// ErrTimeout is returned when the execution deadline is exceeded.
	ErrTimeout = errors.New("Timeout: execution deadline exceeded")
	// ErrUnauthorizedFunction is returned when an expression calls a function
	// that is not on the tenant's whitelist.
	ErrUnauthorizedFunction = errors.New("UnauthorizedFunction: call to non-whitelisted function")
	// ErrValidation is returned when an expression fails static AST validation.
	ErrValidation = errors.New("ValidationError")
)
