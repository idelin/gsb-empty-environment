package kvimport

// FaultPoint identifies a hook in the import pipeline where a fault can be
// injected. It is used exclusively by tests and the demo CLI to simulate
// process crashes / power loss at deterministic locations.
type FaultPoint string

const (
	// FaultBeforeCheckpoint fires after the batch WAL and reject log have
	// been appended and fsynced, but before the checkpoint is written.
	// A crash here leaves the WAL ahead of the checkpoint: recovery must
	// truncate the WAL and replay the last batch without duplicating
	// key versions.
	FaultBeforeCheckpoint FaultPoint = "before_checkpoint"

	// FaultAfterCheckpoint fires right after a checkpoint has been
	// durably written.
	FaultAfterCheckpoint FaultPoint = "after_checkpoint"

	// FaultBeforePublish fires after the input is fully consumed but
	// before the published snapshot is atomically renamed into place.
	// A crash here must leave any previously published store untouched.
	FaultBeforePublish FaultPoint = "before_publish"

	// FaultAfterPublish fires after the published snapshot rename but
	// before the final Done checkpoint is written.
	FaultAfterPublish FaultPoint = "after_publish"
)

// FaultInjector is notified at each FaultPoint. Implementations may panic
// (simulating a crash) or return normally. The production default is
// NoopInjector.
type FaultInjector interface {
	OnFault(point FaultPoint)
}

// NoopInjector never injects faults.
type NoopInjector struct{}

// OnFault implements FaultInjector.
func (NoopInjector) OnFault(FaultPoint) {}
