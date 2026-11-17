package netsec

import (
	"errors"
	"fmt"
)

var (
	// ErrVerification is wrapped by every trace verification failure.
	ErrVerification = errors.New("netsec: trace verification failed")
	// ErrCheckPanicked is wrapped by a PanicError.
	ErrCheckPanicked = errors.New("netsec: check panicked")
	// ErrInvalidCheckResult is wrapped when a check returns an invalid verdict
	// or a zero evidence digest.
	ErrInvalidCheckResult = errors.New("netsec: invalid check result")
)

// CheckError reports a check that returned an error.
type CheckError struct {
	RuleID string
	Node   string
	Err    error
}

func (e *CheckError) Error() string {
	return fmt.Sprintf("netsec: rule %q at %s failed: %v", e.RuleID, e.Node, e.Err)
}

func (e *CheckError) Unwrap() error {
	return e.Err
}

// CanceledError reports that evaluation stopped because the context was
// canceled. If BeforeInvocation is true, the check was not entered.
type CanceledError struct {
	RuleID           string
	Node             string
	BeforeInvocation bool
	Err              error
}

func (e *CanceledError) Error() string {
	if e.BeforeInvocation {
		return fmt.Sprintf("netsec: canceled before invoking rule %q at %s: %v", e.RuleID, e.Node, e.Err)
	}
	return fmt.Sprintf("netsec: rule %q at %s canceled: %v", e.RuleID, e.Node, e.Err)
}

func (e *CanceledError) Unwrap() error {
	return e.Err
}

// PanicError reports a check panic recovered by the executor.
type PanicError struct {
	RuleID string
	Node   string
	Value  any
}

func (e *PanicError) Error() string {
	return fmt.Sprintf("netsec: rule %q at %s panicked: %s", e.RuleID, e.Node, safeSprint(e.Value))
}

func (e *PanicError) Unwrap() error {
	return ErrCheckPanicked
}

// InvalidResultError reports a check result that cannot be audited.
type InvalidResultError struct {
	RuleID string
	Node   string
	Reason string
}

func (e *InvalidResultError) Error() string {
	return fmt.Sprintf("netsec: rule %q at %s returned an invalid result: %s", e.RuleID, e.Node, e.Reason)
}

func (e *InvalidResultError) Unwrap() error {
	return ErrInvalidCheckResult
}

func safeSprint(v any) (out string) {
	defer func() {
		if recover() != nil {
			out = "<unprintable panic value>"
		}
	}()
	return fmt.Sprint(v)
}

// VerifyCode classifies a verification failure.
type VerifyCode string

const (
	VerifyProgramMismatch VerifyCode = "program-mismatch"
	VerifySealMismatch    VerifyCode = "seal-mismatch"
	VerifyMissingStep     VerifyCode = "missing-step"
	VerifyExtraStep       VerifyCode = "extra-step"
	VerifyStepMismatch    VerifyCode = "step-mismatch"
	VerifyStepState       VerifyCode = "step-state"
	VerifyStatusMismatch  VerifyCode = "status-mismatch"
	VerifyVerdictMismatch VerifyCode = "verdict-mismatch"
	VerifyNotCompleted    VerifyCode = "not-completed"
	VerifyNotAllow        VerifyCode = "not-allow"
	VerifyLiveMismatch    VerifyCode = "live-mismatch"
	VerifyLiveFailed      VerifyCode = "live-failed"
)

// VerificationError describes why a trace was rejected.
type VerificationError struct {
	Code    VerifyCode
	Seq     int
	Node    string
	Message string
}

func (e *VerificationError) Error() string {
	location := ""
	if e.Seq >= 0 || e.Node != "" {
		location = fmt.Sprintf(" (step %d, node %s)", e.Seq, e.Node)
	}
	return fmt.Sprintf("netsec: trace verification failed [%s]%s: %s", e.Code, location, e.Message)
}

func (e *VerificationError) Unwrap() error {
	return ErrVerification
}

func verifyError(code VerifyCode, seq int, node, message string) error {
	if seq < 0 && node == "" {
		return &VerificationError{Code: code, Seq: -1, Message: message}
	}
	return &VerificationError{Code: code, Seq: seq, Node: node, Message: message}
}
