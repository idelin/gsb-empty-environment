package netsec

import "time"

// Rejection reason codes recorded in the evidence ledger. They are stable
// strings so external auditors can diff them mechanically.
const (
	// ReasonUnknownTask means the model's task ID was never started at this
	// barrier, so there is no pinned manifest or generation to vouch for it.
	ReasonUnknownTask = "unknown_task"
	// ReasonTaskCancelled means the training task was cancelled before the
	// callback ran; a late callback may not publish its result.
	ReasonTaskCancelled = "task_cancelled"
	// ReasonTaskTerminal means the task already reached a final state
	// (published, rejected or rebuilt) and cannot publish again.
	ReasonTaskTerminal = "task_terminal"
	// ReasonManifestMismatch means the model was produced from inputs other
	// than the manifest pinned when the task started.
	ReasonManifestMismatch = "manifest_mismatch"
	// ReasonStaleGeneration means deletions exist that were not reflected in
	// the task's pinned generation. The task must be rebuilt rather than
	// republished under a new version number.
	ReasonStaleGeneration = "stale_generation"
	// ReasonDeletedInput means at least one individual whose data has been
	// erased still appears in the model's input manifest.
	ReasonDeletedInput = "deleted_input"
)

// Model is the artifact a training callback hands to the barrier. The
// ManifestDigest and Inputs describe what the trainer actually consumed; the
// Version is caller-controlled metadata and is never trusted for admission.
type Model struct {
	TaskID         string
	Version        string
	ManifestDigest string
	Inputs         []string
}

// Decision is the barrier's verdict on a publish attempt.
type Decision struct {
	Allowed bool
	Reason  string
	// PinnedGeneration is the deletion generation the task observed at start.
	PinnedGeneration int64
	// CurrentGeneration is the deletion generation observed during the check.
	CurrentGeneration int64
	// MissingDeletions lists deletions the task had not incorporated when it
	// was rejected; empty when the model is allowed.
	MissingDeletions []DeletionEvent
	// DeletedInputs lists erased individuals still present in the model
	// inputs; empty when the model is allowed.
	DeletedInputs []string
}

// RejectedModel is one immutable entry in the barrier's evidence ledger.
// It preserves enough information to explain why a model was kept out of
// serving even after the originating task is rebuilt.
type RejectedModel struct {
	Model             Model
	Reason            string
	PinnedGeneration  int64
	CurrentGeneration int64
	MissingDeletions  []DeletionEvent
	DeletedInputs     []string
	RejectedAt        time.Time
}
