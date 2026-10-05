package sessionrouter

// Tick is a logical time unit driven by an injected Clock.
type Tick int64

type NodeID string
type SessionID string
type RequestID string

type OpType string

const (
	OpCreate    OpType = "create"
	OpRenew     OpType = "renew"
	OpComplete  OpType = "complete"
	OpWriteback OpType = "writeback"
	OpMigrate   OpType = "migrate"
)

// Code is the machine-readable decision rationale.
type Code string

const (
	CodeOK              Code = "ok"
	CodeConflict        Code = "request-id-conflict"
	CodeNoHealthyNodes  Code = "no-healthy-nodes"
	CodeSessionExists   Code = "session-already-active"
	CodeUnknownSession  Code = "unknown-session"
	CodeSessionClosed   Code = "session-closed"
	CodeStaleGeneration Code = "stale-generation"
	CodeInvalidTTL      Code = "invalid-ttl"
)

// Session is one routing-table entry.
type Session struct {
	ID          SessionID
	Generation  uint64
	Owner       NodeID
	Token       string
	ExpiresAt   Tick
	Closed      bool
	CloseReason string
	Writebacks  []string
}

// Event is one entry in the persisted audit log.
type Event struct {
	Tick      Tick
	Type      string
	SessionID SessionID
	Detail    string
}

// Decision is the outcome of one request, including rationale and the
// number of active sessions observed when the decision was made.
type Decision struct {
	ReqID          RequestID
	Op             OpType
	Allowed        bool
	Code           Code
	Reason         string
	SessionID      SessionID
	Generation     uint64
	Owner          NodeID
	Token          string
	Replayed       bool
	ActiveSessions int
}

// IdemRecord persists the first decision made for a request ID so retries
// return the identical decision and parameter reuse is detected.
type IdemRecord struct {
	Fingerprint string
	Decision    Decision
}

// PersistedState is the full recoverable router state.
type PersistedState struct {
	Tick         Tick
	Sessions     map[SessionID]*Session
	Idem         map[RequestID]IdemRecord
	Healthy      map[NodeID]bool
	Events       []Event
	GenHighWater map[SessionID]uint64
}

func emptyState() *PersistedState {
	return &PersistedState{
		Sessions:     map[SessionID]*Session{},
		Idem:         map[RequestID]IdemRecord{},
		Healthy:      map[NodeID]bool{},
		GenHighWater: map[SessionID]uint64{},
	}
}
