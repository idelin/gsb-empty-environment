package netsec

import "fmt"

// ErrorKind classifies why untrusted metadata was rejected.
type ErrorKind string

const (
	ErrLimits                ErrorKind = "limits"
	ErrInstructionRange      ErrorKind = "instruction_range"
	ErrInstructionOverlap    ErrorKind = "instruction_overlap"
	ErrFunctionRange         ErrorKind = "function_range"
	ErrFunctionID            ErrorKind = "function_id"
	ErrFunctionOverlap       ErrorKind = "function_overlap"
	ErrFunctionCoverage      ErrorKind = "function_coverage"
	ErrFunctionPrefix        ErrorKind = "function_entry_prefix"
	ErrEntryFunction         ErrorKind = "entry_function"
	ErrEntryRange            ErrorKind = "entry_range"
	ErrEntryBoundary         ErrorKind = "entry_boundary"
	ErrLandingPadRange       ErrorKind = "landing_pad_range"
	ErrLandingPadInstruction ErrorKind = "landing_pad_instruction"
	ErrLandingPadProtected   ErrorKind = "landing_pad_protected"
	ErrLandingPadEntryPrefix ErrorKind = "landing_pad_entry_prefix"
	ErrEntryOverlap          ErrorKind = "entry_overlap"
	ErrActionIndex           ErrorKind = "action_index"
	ErrActionCycle           ErrorKind = "action_cycle"
	ErrActionBudget          ErrorKind = "action_budget"
	ErrActionType            ErrorKind = "action_type"
	ErrPersonality           ErrorKind = "personality"
	ErrEntryBudget           ErrorKind = "entry_budget"
	ErrActionNodeBudget      ErrorKind = "action_node_budget"
)

func (k ErrorKind) Error() string { return string(k) }

// ValidationError reports a precise, indexable validation failure. No handler
// table is returned when this error is produced.
type ValidationError struct {
	Kind       ErrorKind
	Entry      int
	Action     int
	FunctionID FunctionID
	Address    uint64
	Detail     string
}

func (e *ValidationError) Error() string {
	where := ""
	// Zero is a valid entry/action index, so only annotate indexes that are
	// unambiguously present. The Kind and Detail always identify the failure.
	if e.Entry > 0 {
		where += fmt.Sprintf(" entry %d", e.Entry)
	}
	if e.Action > 0 {
		where += fmt.Sprintf(" action %d", e.Action)
	}
	if e.Address != 0 {
		where += fmt.Sprintf(" at 0x%x", e.Address)
	}
	if e.Detail != "" {
		return fmt.Sprintf("%s%s: %s", e.Kind, where, e.Detail)
	}
	return fmt.Sprintf("%s%s", e.Kind, where)
}

// Is lets callers match a ValidationError against an ErrorKind with errors.Is.
func (e *ValidationError) Is(target error) bool {
	k, ok := target.(ErrorKind)
	return ok && e.Kind == k
}
