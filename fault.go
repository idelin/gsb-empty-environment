package segmentfsm

import "fmt"

// FaultPoint names a location in the state machine where a fault can be
// injected. Every fault point sits immediately before an action whose
// completion is not journaled yet, so crashing there always leaves the
// system in a state the recovery path knows how to redo or roll back.
type FaultPoint string

const (
	FaultBuildWriteTmp   FaultPoint = "build.write-tmp"   // before writing the new segment tmp file
	FaultBuildRename     FaultPoint = "build.rename"      // before renaming segment tmp -> final
	FaultValidateRead    FaultPoint = "validate.read"     // before re-reading the built segment
	FaultPublishWriteTmp FaultPoint = "publish.write-tmp" // before writing manifest tmp file
	FaultPublishRename   FaultPoint = "publish.rename"    // before renaming manifest tmp -> final
	FaultReclaimDelete   FaultPoint = "reclaim.delete"    // before deleting each old segment
	FaultJournalWrite    FaultPoint = "journal.write"     // before persisting any journal transition
)

// AllFaultPoints lists every injectable point, used by the crash-matrix test.
var AllFaultPoints = []FaultPoint{
	FaultBuildWriteTmp,
	FaultBuildRename,
	FaultValidateRead,
	FaultPublishWriteTmp,
	FaultPublishRename,
	FaultReclaimDelete,
	FaultJournalWrite,
}

// SimulatedCrash is panicked by an Injector to emulate an abrupt process
// crash (or power cut) at a fault point: it bypasses all defers, exactly
// like a real crash would. Tests and the demo recover it at the top level.
type SimulatedCrash struct {
	Point FaultPoint
}

func (c SimulatedCrash) Error() string {
	return fmt.Sprintf("simulated crash at %s", c.Point)
}

// Injector is called at every FaultPoint. Returning a non-nil error models
// a transient, retryable fault (the step fails and the caller may retry).
// Panicking with SimulatedCrash models an unrecoverable process crash; the
// next Open() resumes from the journaled state.
type Injector func(point FaultPoint) error
