// Package router implements a local session routing state machine.
//
// The router tracks sessions (sessionID, generation, owner, expiry tick),
// migrates them off failed nodes with a bumped generation, and rejects any
// renewal / completion / writeback that carries a stale generation.
package router

// SessionState is the lifecycle state of a session.
type SessionState string

const (
	StateActive  SessionState = "active"
	StateExpired SessionState = "expired"
	StateClosed  SessionState = "closed"
)

// Session is the persisted routing record for one sessionID.
type Session struct {
	ID         string       `json:"id"`
	Generation uint64       `json:"generation"`
	Owner      string       `json:"owner"`
	ExpiresAt  uint64       `json:"expires_at"`
	State      SessionState `json:"state"`
	Token      string       `json:"token"`
	Writebacks []string     `json:"writebacks,omitempty"`
}

// Decision is the outcome of one routed request, including the rationale
// (Reason) and the active session count observed at decision time.
type Decision struct {
	Accepted       bool   `json:"accepted"`
	Conflict       bool   `json:"conflict,omitempty"`
	Reason         string `json:"reason"`
	SessionID      string `json:"session_id,omitempty"`
	Generation     uint64 `json:"generation,omitempty"`
	Owner          string `json:"owner,omitempty"`
	Token          string `json:"token,omitempty"`
	ActiveSessions int    `json:"active_sessions"`
}

// RequestRecord binds a request ID to the session it first referenced and
// the decision that was returned, so replays are idempotent and cross-session
// reuse of an ID is detected as a conflict.
type RequestRecord struct {
	RequestID string   `json:"request_id"`
	SessionID string   `json:"session_id"`
	Decision  Decision `json:"decision"`
}

// Event is one entry in the in-memory event log.
type Event struct {
	Tick       uint64 `json:"tick"`
	Kind       string `json:"kind"`
	Node       string `json:"node,omitempty"`
	Session    string `json:"session,omitempty"`
	Generation uint64 `json:"generation,omitempty"`
	Detail     string `json:"detail,omitempty"`
}
