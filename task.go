package netsec

// Task lifecycle states. A task only publishes while in stateRunning; every
// other state is terminal except that Rebuild can retire a task explicitly.
const (
	stateRunning   = "running"
	stateCancelled = "cancelled"
	statePublished = "published"
	stateRejected  = "rejected"
	stateRebuilt   = "rebuilt"
)

// TrainingTask is the immutable training contract fixed at start time: the
// exact input manifest and the deletion generation observed then. Nothing in
// this structure can be changed afterwards, which is why a stale task cannot
// be "upgraded" by bumping a model version and must instead be rebuilt.
type TrainingTask struct {
	id             string
	manifest       Manifest
	manifestDigest string
	pinnedGen      int64
	state          string
}

// ID returns the task identifier.
func (t *TrainingTask) ID() string { return t.id }

// Manifest returns the input manifest pinned at start time.
func (t *TrainingTask) Manifest() Manifest { return t.manifest }

// ManifestDigest returns the digest of the pinned manifest.
func (t *TrainingTask) ManifestDigest() string { return t.manifestDigest }

// PinnedGeneration returns the deletion generation observed when the task
// started.
func (t *TrainingTask) PinnedGeneration() int64 { return t.pinnedGen }

// State returns the current lifecycle state.
func (t *TrainingTask) State() string { return t.state }

// IsStale reports whether the task predates at least one recorded deletion.
// Staleness never clears: once a newer generation exists the pinned inputs can
// only be made safe by starting a fresh task through Rebuild.
func (t *TrainingTask) IsStale(currentGeneration int64) bool {
	return currentGeneration > t.pinnedGen
}
