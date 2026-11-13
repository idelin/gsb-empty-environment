package netsec

import "errors"

var (
	// ErrInvalidProgram reports malformed CFG or instruction data.
	ErrInvalidProgram = errors.New("invalid program")
	// ErrTooManyNodes reports a CFG larger than MaxNodes.
	ErrTooManyNodes = errors.New("program exceeds node limit")
	// ErrBudgetExceeded reports that the analysis budget was exhausted.
	ErrBudgetExceeded = errors.New("analysis budget exceeded")
	// ErrTooManySecrets reports that differential execution was requested for
	// more than three secret boolean inputs.
	ErrTooManySecrets = errors.New("more than three secret inputs")
	// ErrExecutionBudget reports that concrete execution did not terminate
	// within its step limit.
	ErrExecutionBudget = errors.New("concrete execution budget exceeded")
)
