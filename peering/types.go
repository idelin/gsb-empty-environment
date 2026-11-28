package peering

import "fmt"

// OSDID identifies a single OSD daemon inside the cluster.
type OSDID int

// Epoch is an OSDMap epoch number.
type Epoch uint64

// ObjectID identifies a stored object within a PG.
type ObjectID string

// EVersion is a PG-scoped entry version (Ceph eversion_t): a pair of the
// OSDMap epoch in which the write was issued and a monotonically increasing
// per-PG version counter.
type EVersion struct {
	Epoch   Epoch
	Version uint64
}

// Compare orders two eversions first by epoch and then by version counter.
func (v EVersion) Compare(other EVersion) int {
	switch {
	case v.Epoch < other.Epoch:
		return -1
	case v.Epoch > other.Epoch:
		return 1
	}
	switch {
	case v.Version < other.Version:
		return -1
	case v.Version > other.Version:
		return 1
	default:
		return 0
	}
}

// IsZero reports whether the version is the zero value (no version).
func (v EVersion) IsZero() bool {
	return v.Epoch == 0 && v.Version == 0
}

// String renders the canonical Ceph "epoch'version" notation.
func (v EVersion) String() string {
	return fmt.Sprintf("%d'%d", v.Epoch, v.Version)
}

// OpType classifies a PG log entry.
type OpType uint8

const (
	// OpWrite creates or overwrites an object.
	OpWrite OpType = iota + 1
	// OpDelete removes an object.
	OpDelete
)

// LogEntry is one entry of a PG log (Ceph pg_log_entry_t).
type LogEntry struct {
	Version EVersion
	Object  ObjectID
	Op      OpType
}

// PGLog is the per-OSD PG log together with its bookkeeping pointers.
//
// Entries are stored ascending by Version and are contiguous from Tail.
// LastEpochStarted is the OSDMap epoch in which this OSD last completed a
// successful peering activation: entries written without a successful
// peering (an isolated minority) do not advance it.
type PGLog struct {
	Entries          []LogEntry
	Tail             EVersion
	LastUpdate       EVersion
	LastComplete     EVersion
	LastEpochStarted Epoch
}

// Contains reports whether the log carries an entry for object at version.
func (l *PGLog) Contains(version EVersion, object ObjectID) bool {
	for _, entry := range l.Entries {
		if entry.Version.Compare(version) == 0 && entry.Object == object {
			return true
		}
	}
	return false
}

// OSDMapEpoch is the PG-relevant slice of one OSDMap epoch.
type OSDMapEpoch struct {
	Epoch   Epoch
	Up      []OSDID
	Acting  []OSDID
	MinSize int
	// Active reports whether the PG had reached active state during this epoch.
	Active bool
}

// PastInterval is a maximal run of consecutive epochs in which the PG up and
// acting sets were unchanged (Ceph PastInterval).
type PastInterval struct {
	First       Epoch
	Last        Epoch
	Up          []OSDID
	Acting      []OSDID
	MinSize     int
	Active      bool
	MaybeWentRW bool
}

// OSDState is the runtime state of one OSD replica as seen while peering.
type OSDState struct {
	ID OSDID
	// Alive means the OSD answered the current probe.
	Alive bool
	// NeedsBackfill forces the replica onto the backfill path even if it
	// carries a log (e.g. administratively flagged replacement).
	NeedsBackfill bool
	Log           PGLog
	// Objects maps each currently stored object to the version held.
	// Deleted objects are absent.
	Objects map[ObjectID]EVersion
}

// PGState is the terminal state of the peering state machine.
type PGState string

const (
	// StatePeering means peering is still in progress.
	StatePeering PGState = "Peering"
	// StateIncomplete means peering is blocked and the PG must not serve IO.
	StateIncomplete PGState = "Incomplete"
	// StateActive means peering completed and the PG may serve IO subject to
	// the min_size write gate and per-object unfound blocks.
	StateActive PGState = "Active"
)

// Blocking reasons explaining why the PG cannot activate.
const (
	ReasonNone       = ""
	ReasonHistoryGap = "maybe_went_rw interval has no reachable witness"
	ReasonNoReplica  = "no reachable replica answered the log probe"
	ReasonMinSize    = "fewer than min_size recoverable replicas"
)
