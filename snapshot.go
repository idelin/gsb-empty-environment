package netsec

// Identity is the public name of the fixed process principal. It is used only
// to construct a Host; request identities are issued by the Authenticator.
type Identity struct {
	Name         string
	ConnectionID string
	RequestID    string
	Generation   uint64
}

// Snapshot is an immutable, copy-only view of a token's identity. It contains
// no ability to mutate a token or to enter an identity onto a thread.
type Snapshot struct {
	name         string
	connectionID string
	requestID    string
	generation   uint64
	level        Level
	deadline     Instant
}

func (s Snapshot) Name() string         { return s.name }
func (s Snapshot) ConnectionID() string { return s.connectionID }
func (s Snapshot) RequestID() string    { return s.requestID }
func (s Snapshot) Generation() uint64   { return s.generation }
func (s Snapshot) Level() Level         { return s.level }
func (s Snapshot) Deadline() Instant    { return s.deadline }

// IsZero reports whether this snapshot came from a NoIdentity task.
func (s Snapshot) IsZero() bool {
	return s.connectionID == "" && s.requestID == "" && s.generation == 0 && s.deadline == 0
}

func (s Snapshot) String() string {
	if s.IsZero() {
		return "NoIdentity"
	}
	return "identity(" + s.requestID + "@" + s.connectionID + ", gen=" +
		itoa(s.generation) + ", level=" + s.level.String() + ")"
}

func itoa(v uint64) string {
	if v == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for v > 0 {
		i--
		buf[i] = byte('0' + v%10)
		v /= 10
	}
	return string(buf[i:])
}
