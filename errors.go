package netsec

import (
	"errors"
	"fmt"
)

var (
	// ErrInvalidCompileInput means an input alias is inconsistent or a
	// required dependency field is missing.
	ErrInvalidCompileInput = errors.New("netsec: invalid compile input")
	// ErrPermissionDenied means the pinned snapshot does not grant every
	// operation the query requires.
	ErrPermissionDenied = errors.New("netsec: permission denied")
	// ErrPrincipalInactive means the principal is absent or revoked.
	ErrPrincipalInactive = errors.New("netsec: principal inactive")
	// ErrPlanMiss means a cache entry no longer matches the pinned snapshot.
	// It must be discarded and recompiled, never executed.
	ErrPlanMiss = errors.New("netsec: plan cache miss")
	// ErrPlanRejected means pre-execution review failed. No operator may run.
	ErrPlanRejected = errors.New("netsec: plan rejected before execution")
	// ErrImmediateRevocation means an immediate REVOKE fenced a transaction
	// or a waiting plan.
	ErrImmediateRevocation = errors.New("netsec: immediate revocation")
	// ErrParameterType means a structured bind value does not match the
	// compiled parameter type list.
	ErrParameterType = errors.New("netsec: parameter type mismatch")
	// ErrSessionClosed means the session/connection is no longer usable.
	ErrSessionClosed = errors.New("netsec: session closed")
	// ErrTransactionDone means the transaction already committed or rolled
	// back.
	ErrTransactionDone = errors.New("netsec: transaction finished")
)

// PermissionError carries the object that failed authorization.
type PermissionError struct {
	Principal PrincipalID
	ObjectID  ObjectID
	Needed    OperationSet
	Granted   OperationSet
	Reason    string
}

func (e *PermissionError) Error() string {
	reason := e.Reason
	if reason == "" {
		reason = "insufficient privileges"
	}
	return fmt.Sprintf("%v: principal %q object %q needs %s has %s (%s)",
		ErrPermissionDenied, e.Principal, e.ObjectID, e.Needed, e.Granted, reason)
}

func (e *PermissionError) Unwrap() error { return ErrPermissionDenied }

// PlanMissError explains why a previously compiled plan cannot be reused.
type PlanMissError struct {
	Reason string
}

func (e *PlanMissError) Error() string {
	return fmt.Sprintf("%v: %s", ErrPlanMiss, e.Reason)
}

func (e *PlanMissError) Unwrap() error { return ErrPlanMiss }

// RevocationError identifies the immediate revocation that fenced execution.
type RevocationError struct {
	Sequence   uint64
	Principal  PrincipalID
	ObjectID   ObjectID
	Operations OperationSet
}

func (e *RevocationError) Error() string {
	return fmt.Sprintf("%v: sequence %d principal %q object %q operations %s",
		ErrImmediateRevocation, e.Sequence, e.Principal, e.ObjectID, e.Operations)
}

func (e *RevocationError) Unwrap() error { return ErrImmediateRevocation }

// ParameterError describes a structured bind failure.
type ParameterError struct {
	Index    int
	Expected ParamType
	Actual   ParamType
	Reason   string
}

func (e *ParameterError) Error() string {
	return fmt.Sprintf("%v: parameter %d expected %q got %q (%s)",
		ErrParameterType, e.Index, e.Expected, e.Actual, e.Reason)
}

func (e *ParameterError) Unwrap() error { return ErrParameterType }
