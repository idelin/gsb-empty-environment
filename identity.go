package takeover

// ProcessStartID identifies one process incarnation. PID alone is not a
// fence: a process identifier may be reused after the original process exits.
// BootID and Start are therefore part of every grant and every control
// message.
type ProcessStartID struct {
	BootID string `json:"boot_id"`
	PID    uint32 `json:"pid"`
	Start  uint64 `json:"start"`
}

// StartID is an alias kept for callers that use the shorter name.
type StartID = ProcessStartID

// NewProcessStartID constructs a process start identity.
func NewProcessStartID(bootID string, pid uint32, start uint64) ProcessStartID {
	return ProcessStartID{BootID: bootID, PID: pid, Start: start}
}

// NewStartID is an alias for NewProcessStartID.
func NewStartID(bootID string, pid uint32, start uint64) ProcessStartID {
	return NewProcessStartID(bootID, pid, start)
}

// Valid reports whether the identity has any discriminating component. An
// all-zero start identity is not accepted as a fencing identity.
func (id ProcessStartID) Valid() bool {
	return id.BootID != "" || id.PID != 0 || id.Start != 0
}

// Same reports exact process-incarnation equality.
func (id ProcessStartID) Same(other ProcessStartID) bool {
	return id == other
}

// SessionRoot is the root identity of the debug session. It is deliberately a
// string so that a host can use an opaque session token or a stable path.
type SessionRoot string

// Fence binds a control message to a process incarnation and a session root.
type Fence struct {
	ProcessStartID ProcessStartID `json:"process_start_id"`
	SessionRoot    SessionRoot    `json:"session_root"`
}

// FenceOf returns the fence for a grant.
func (g Grant) FenceOf() Fence {
	return Fence{ProcessStartID: g.ProcessStartID, SessionRoot: g.SessionRoot}
}

// Valid reports whether the session root is non-empty.
func (s SessionRoot) Valid() bool {
	return s != ""
}
