// Package commitwatermark computes the largest Raft commitIndex a leader may
// advertise when the effective cluster configuration changes along the log
// prefix, including joint consensus (C_old,new) transitions and non-voting
// learners.
package commitwatermark

// NodeID identifies a cluster node. A node is either a voter or a learner in
// any given configuration.
type NodeID string

// Config is one membership configuration. Only Voters participate in quorum;
// Learners receive log entries but their acknowledgements never count.
type Config struct {
	Voters   []NodeID
	Learners []NodeID
}

// ChangeKind selects the shape of a membership-change entry.
type ChangeKind int

const (
	// ChangeEnterJoint switches the cluster into joint consensus C_old,new.
	// Both configurations must independently reach quorum while joint.
	ChangeEnterJoint ChangeKind = iota + 1
	// ChangeLeaveJoint switches from a joint configuration to a single C_new.
	ChangeLeaveJoint
)

// ConfigChange is attached to a log entry. A nil pointer means the entry is a
// plain application entry. Per Raft, a configuration entry takes effect for
// its own index and every following index as soon as it is written.
type ConfigChange struct {
	Kind ChangeKind
	// Old is the configuration kept alongside New while in joint consensus.
	// It is required for ChangeEnterJoint and ignored for ChangeLeaveJoint.
	Old Config
	// New is the target configuration. For ChangeLeaveJoint it is the single
	// configuration that becomes effective.
	New Config
}

// LogEntry is one entry of the leader's complete log.
type LogEntry struct {
	Index  uint64
	Term   uint64
	Change *ConfigChange
}

// ReplicaState is the replication state reported by one node.
type ReplicaState struct {
	// MatchIndex is the highest log index known replicated on this node.
	MatchIndex uint64
	// PersistedTerm is the term durably stored on this node (its currentTerm).
	// A node cannot confirm an entry whose term exceeds its persisted term,
	// and a node ahead of the leader cannot be counted at all.
	PersistedTerm uint64
}

// Input is everything required to evaluate the commit watermark.
type Input struct {
	// CurrentTerm is the new leader's current term.
	CurrentTerm uint64
	// Leader is the node acting as leader. It is informational: the leader is
	// counted only if it appears as a voter and is present in Replicas.
	Leader NodeID
	// Initial is the configuration effective before the first listed entry.
	Initial Config
	// PrevCommit is the already committed watermark. Advancement is only
	// evaluated for indices greater than PrevCommit.
	PrevCommit uint64
	// Entries is the leader's complete (possibly truncated) log. Indices must
	// be contiguous and strictly increasing; terms must be non-decreasing and
	// not exceed CurrentTerm.
	Entries []LogEntry
	// Replicas maps every node (voters, learners and the leader itself) to its
	// replication state. Nodes absent from the map acknowledge nothing.
	Replicas map[NodeID]ReplicaState
}

// Bottleneck describes one quorum group of one effective configuration segment
// that blocks advancement toward Input's latest current-term entry.
type Bottleneck struct {
	// FromIndex and ToIndex delimit (within PrevCommit+1 .. TargetIndex) the
	// log range over which this configuration is effective.
	FromIndex uint64
	ToIndex   uint64
	// Joint is true for a C_old,new segment. In that case Group identifies
	// which side fails; otherwise Group is empty.
	Joint bool
	// Group is "old" or "new" for joint segments, empty for single configs.
	Group string
	// Config is the failing quorum group's membership configuration.
	Config Config
	// Voters are the sorted voters that make up the quorum group.
	Voters []NodeID
	// Acks are the sorted voters whose effective match reaches TargetIndex.
	Acks []NodeID
	// Need is the majority threshold (len(Voters)/2 + 1).
	Need int
	// Have is the number of confirming voters, always smaller than Need.
	Have int
}

// Result is the commit-watermark evaluation.
type Result struct {
	// CommitIndex is the precise largest safe commit watermark. It never moves
	// backwards and equals PrevCommit when nothing new is committable.
	CommitIndex uint64
	// TargetIndex is the latest entry whose term equals CurrentTerm. It is the
	// highest index advancement could possibly reach in this call; zero when
	// the log contains no current-term entry.
	TargetIndex uint64
	// Bottlenecks lists every quorum group that fails at TargetIndex. It is
	// empty exactly when CommitIndex reaches TargetIndex (or no target exists).
	Bottlenecks []Bottleneck
}
