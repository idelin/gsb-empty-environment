package revocation

// State is the lifecycle state of a [Subscriber].
type State int

const (
	// StateSyncing means the subscriber has not yet proven it is contiguous
	// with the publisher's latest checkpoint. All credential judgments are
	// denied in this state.
	StateSyncing State = iota
	// StateSynced means a contiguous run of events has been applied up to the
	// latest known checkpoint. Credential judgments are trustworthy.
	StateSynced
	// StateGap means a discontinuity was detected. The subscriber denies all
	// judgments until an authoritative snapshot restores continuity.
	StateGap
)

// String returns the stable wire name of the state.
func (s State) String() string {
	switch s {
	case StateSynced:
		return "synced"
	case StateGap:
		return "gap"
	default:
		return "syncing"
	}
}

// Event is one revocation-log record.
//
// Sequence numbers are dense and start at 1 within a generation. A generation
// rollover is an explicit discontinuity: all in-flight state from the prior
// generation must be discarded and a fresh snapshot requested.
type Event struct {
	// Generation groups a contiguous log epoch. It must be positive.
	Generation int64
	// Sequence is the 1-based dense position inside the generation.
	Sequence int64
	// CredentialID identifies the credential whose status changed.
	CredentialID string
	// Revoked is true for a revocation and false when a revocation is lifted.
	Revoked bool
}

// Checkpoint is the publisher's authoritative high-water mark. A subscriber
// may only report caught-up status once it has applied every event at or
// before this coordinate.
type Checkpoint struct {
	Generation int64
	Sequence   int64
}

// Snapshot is the complete revocation state at a single log coordinate.
//
// Revoked holds every credential that is revoked as of LastSequence; the
// boolean value is retained so future status extensions remain source
// compatible.
type Snapshot struct {
	Generation int64
	// LastSequence is the dense position the snapshot materializes
	// (inclusive).
	LastSequence int64
	// Revoked is the full authoritative revocation set.
	Revoked map[string]bool
}

// Status is an immutable point-in-time view of subscriber health.
type Status struct {
	State      State
	Generation int64
	// NextSequence is the sequence number of the next expected event.
	NextSequence int64
	// Applied is the last contiguous event applied.
	Applied Checkpoint
	// Latest is the latest checkpoint announced by the publisher.
	Latest Checkpoint
	// SnapshotInFlight reports whether a snapshot request is outstanding.
	SnapshotInFlight bool
}

// Synced reports whether the local view is trustworthy.
func (s Status) Synced() bool { return s.State == StateSynced }

// PersistedState is the durable representation of a subscriber. It is part of
// the on-disk contract and must remain backward compatible as it evolves.
type PersistedState struct {
	// Version identifies the persistence format. Currently always 1.
	Version int `json:"version"`

	State      State `json:"state"`
	Generation int64 `json:"generation"`
	NextSeq    int64 `json:"next_seq"`

	// Revoked is the applied revocation set.
	Revoked map[string]bool `json:"revoked"`
	// Pending buffers events observed while a snapshot is in flight.
	Pending map[int64]Event `json:"pending,omitempty"`

	Applied Checkpoint `json:"applied"`
	Latest  Checkpoint `json:"latest"`

	// WaitingGeneration is the generation the outstanding snapshot belongs
	// to; zero when no snapshot is requested.
	WaitingGeneration int64 `json:"waiting_generation,omitempty"`
}

// StateStore durably records subscriber state.
//
// Save must be atomic and durable: either the previous state remains fully
// readable or the new state does. Implementations must be safe for
// concurrent use.
type StateStore interface {
	Save(state PersistedState) error
	Load() (PersistedState, bool, error)
}
