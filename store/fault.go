package store

import "fmt"

// Fault injection points let tests and the CLI simulate process
// crashes at durability boundaries. A hook returning an error makes
// the in-flight apply abort exactly as a crash would: everything
// already committed stays committed, nothing half-written is visible.
type FaultPoint string

const (
	FaultBeforeStep   FaultPoint = "before_step"   // before a step starts
	FaultBeforeRename FaultPoint = "before_rename" // temp written+fsynced, not yet renamed
	FaultAfterRename  FaultPoint = "after_rename"  // renamed, journal not yet updated
	FaultAfterJournal FaultPoint = "after_journal" // step journaled, before next step
)

// FaultHook, when non-nil, is invoked at every FaultPoint during Apply.
// Returning a non-nil error aborts the apply (simulated crash).
// Production code leaves it nil; tests and the CLI demo arm it.
var FaultHook func(point FaultPoint, stepID string) error

// InjectedFault is the error type returned when a hook fires, so
// callers can distinguish simulated crashes from real failures.
type InjectedFault struct {
	Point  FaultPoint
	StepID string
}

func (e *InjectedFault) Error() string {
	return fmt.Sprintf("injected fault at %s (step %s)", e.Point, e.StepID)
}

func runFault(point FaultPoint, stepID string) error {
	if FaultHook == nil {
		return nil
	}
	if err := FaultHook(point, stepID); err != nil {
		return err
	}
	return nil
}
