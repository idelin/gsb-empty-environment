package replayhandoff

type DeviceID string

type NodeID string

type KeyGeneration uint64

type Outcome int

const (
	OutcomeConflict Outcome = iota
	OutcomeNeedAnchor
	OutcomeRejected
	OutcomeAccepted
	OutcomeInvalidAuthentication
)

func (o Outcome) String() string {
	switch o {
	case OutcomeConflict:
		return "conflict"
	case OutcomeNeedAnchor:
		return "need_anchor"
	case OutcomeRejected:
		return "rejected"
	case OutcomeAccepted:
		return "accepted"
	case OutcomeInvalidAuthentication:
		return "invalid_authentication"
	default:
		return "unknown"
	}
}

type ConflictCode string

const (
	ConflictBadSignature          ConflictCode = "bad_signature"
	ConflictConflictingState      ConflictCode = "conflicting_state"
	ConflictDuplicateNodeLog      ConflictCode = "duplicate_node_log"
	ConflictInvalidBitWidth       ConflictCode = "invalid_bit_width"
	ConflictInvalidReorderBound   ConflictCode = "invalid_reorder_bound"
	ConflictLowOutOfRange         ConflictCode = "low_out_of_range"
	ConflictMissingCheckpoint     ConflictCode = "missing_checkpoint"
	ConflictMissingMaterial       ConflictCode = "missing_material"
	ConflictNoCompatibleSequence  ConflictCode = "no_compatible_sequence"
	ConflictUnsatisfiableOrder    ConflictCode = "unsatisfiable_order"
	ConflictVerifierNotConfigured ConflictCode = "verifier_not_configured"
)

type Conflict struct {
	Code   ConflictCode
	Node   NodeID
	Low    uint64
	Full   uint64
	Detail string
}

type StateKey struct {
	Device     DeviceID
	Generation KeyGeneration
}

type Checkpoint struct {
	FullSequence uint64
	Signature    []byte
}

type LogEntry struct {
	LowSequence uint64
	Admitted    bool
}

type NodeLog struct {
	Node    NodeID
	Entries []LogEntry
}

type HandoffMaterial struct {
	Device       DeviceID
	Generation   KeyGeneration
	Checkpoints  []Checkpoint
	NodeLogs     []NodeLog
	ReorderBound uint64
}

type Frame struct {
	Device      DeviceID
	Generation  KeyGeneration
	LowSequence uint64
}

type CheckpointVerifier func(device DeviceID, generation KeyGeneration, fullSequence uint64, signature []byte) bool

type Config struct {
	Bits int

	// Zero defaults to 2^(Bits-1), which is also the replay window size.
	ReorderBound uint64

	Verifier CheckpointVerifier
}

type AssignedEntry struct {
	Node     NodeID
	Low      uint64
	Full     uint64
	Admitted bool
}

type CompleteAssignment struct {
	ReorderBound   uint64
	HorizonLow     uint64
	HorizonHigh    uint64
	Checkpoints    []uint64
	Entries        []AssignedEntry
	HasCandidate   bool
	CandidateLow   uint64
	CandidateFull  uint64
	CandidateFresh bool
}

type Decision struct {
	Outcome           Outcome
	Device            DeviceID
	Generation        KeyGeneration
	ReplayWindowTop   uint64
	FullSequences     []uint64
	ImportedSequences []uint64
	NeedAnchorLow     uint64
	Alternatives      []CompleteAssignment
	Conflicts         []Conflict
}
