package netsec

// Token is an immutable request identity issued by an Authenticator. Callers
// may copy the pointer, but every use is checked against the live request and
// connection generation, so a token retained by an old callback fails closed.
type Token struct {
	host     *Host
	auth     *Authenticator
	id       uint64
	primary  bool
	request  *Request
	level    Level
	deadline Instant
}

func (t *Token) ID() uint64 {
	if t == nil {
		return 0
	}
	return t.id
}

func (t *Token) IsPrimary() bool {
	return t != nil && t.primary
}

func (t *Token) Level() Level {
	if t == nil {
		return Identification
	}
	return t.level
}

func (t *Token) Deadline() Instant {
	if t == nil {
		return 0
	}
	return t.deadline
}

func (t *Token) ConnectionID() string {
	if t == nil {
		return ""
	}
	if t.primary {
		return t.host.primary.ConnectionID
	}
	if t.request == nil {
		return ""
	}
	return t.request.conn.id
}

func (t *Token) RequestID() string {
	if t == nil || t.request == nil {
		return ""
	}
	return t.request.id
}

func (t *Token) Generation() uint64 {
	if t == nil || t.request == nil {
		return 0
	}
	return t.request.generation
}

// Snapshot returns identity data only. It does not carry the token's ability
// to enter a thread or call a resource.
func (t *Token) Snapshot() Snapshot {
	if t == nil || t.host == nil {
		return Snapshot{}
	}
	t.host.mu.Lock()
	defer t.host.mu.Unlock()
	return t.snapshotLocked()
}

func (t *Token) snapshotLocked() Snapshot {
	if t == nil {
		return Snapshot{}
	}
	s := Snapshot{
		name:     t.host.primary.Name,
		level:    t.level,
		deadline: t.deadline,
	}
	if t.primary {
		s.connectionID = t.host.primary.ConnectionID
		s.requestID = t.host.primary.RequestID
		s.generation = t.host.primary.Generation
		return s
	}
	if t.request != nil {
		s.connectionID = t.request.conn.id
		s.requestID = t.request.id
		s.generation = t.request.generation
	}
	return s
}

// Valid revalidates the token against its request, connection generation, and
// host monotonic deadline.
func (t *Token) Valid() error {
	if t == nil || t.host == nil {
		return ErrInvalidToken
	}
	t.host.mu.Lock()
	defer t.host.mu.Unlock()
	return t.host.verifyTokenLocked(t)
}
