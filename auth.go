package netsec

import "time"

// Authenticator is the only component allowed to mint request tokens. A token
// carries a pointer to this object, making externally forged zero-value tokens
// invalid even if their fields happen to look plausible.
type Authenticator struct {
	host *Host
}

func (a *Authenticator) Host() *Host {
	if a == nil {
		return nil
	}
	return a.host
}

func (a *Authenticator) OpenConnection(id string) (*Connection, error) {
	if a == nil || a.host == nil {
		return nil, ErrDifferentHost
	}
	if id == "" {
		return nil, ErrInvalidConnectionID
	}
	h := a.host
	h.mu.Lock()
	defer h.mu.Unlock()
	if _, exists := h.connections[id]; exists {
		return nil, ErrConnectionExists
	}
	conn := &Connection{
		host: h,
		id:   id,
		open: true,
	}
	h.connections[id] = conn
	return conn, nil
}

// BeginRequest authenticates a new request and advances the connection
// generation. A connection may have only one active request at a time; call
// End or Reuse first.
func (a *Authenticator) BeginRequest(
	conn *Connection,
	requestID string,
	level Level,
	deadline Instant,
) (*Request, error) {
	if a == nil || a.host == nil {
		return nil, ErrDifferentHost
	}
	if conn == nil || conn.host != a.host {
		return nil, ErrDifferentHost
	}
	if requestID == "" {
		return nil, ErrInvalidRequestID
	}
	if !level.Valid() {
		return nil, ErrInvalidLevel
	}
	h := a.host
	h.mu.Lock()
	defer h.mu.Unlock()

	if !conn.open {
		return nil, ErrConnectionClosed
	}
	if conn.request != nil && conn.request.active {
		return nil, ErrConnectionBusy
	}
	if !h.clock.Now().Before(deadline) {
		return nil, ErrDeadlineInPast
	}

	conn.generation++
	req := &Request{
		host:       h,
		conn:       conn,
		id:         requestID,
		generation: conn.generation,
		level:      level,
		deadline:   deadline,
		active:     true,
		tasks:      make(map[uint64]*Task),
		grants:     make(map[uint64]*Grant),
	}
	req.token = &Token{
		host:     h,
		auth:     a,
		id:       h.nextToken(),
		request:  req,
		level:    level,
		deadline: deadline,
	}
	conn.request = req
	return req, nil
}

// BeginRequestAfter is a convenience for hosts that express deadlines as a
// maximum lifetime in host monotonic time.
func (a *Authenticator) BeginRequestAfter(
	conn *Connection,
	requestID string,
	level Level,
	ttl time.Duration,
) (*Request, error) {
	if a == nil || a.host == nil {
		return nil, ErrDifferentHost
	}
	if ttl <= 0 {
		return nil, ErrDeadlineInPast
	}
	return a.BeginRequest(conn, requestID, level, a.host.Now().Add(ttl))
}

// IssueNested mints another token for the same request. Nested tokens may only
// narrow level and deadline; they can never widen either.
func (a *Authenticator) IssueNested(req *Request, level Level, deadline Instant) (*Token, error) {
	if a == nil || a.host == nil {
		return nil, ErrDifferentHost
	}
	if req == nil || req.host != a.host || req.token == nil || req.token.auth != a {
		return nil, ErrTokenRequestMismatch
	}
	if !level.Valid() {
		return nil, ErrInvalidLevel
	}
	h := a.host
	h.mu.Lock()
	defer h.mu.Unlock()
	if err := h.verifyRequestLocked(req); err != nil {
		return nil, err
	}
	if level > req.level {
		return nil, ErrCaptureEscalation
	}
	if deadline.After(req.deadline) {
		return nil, ErrDeadlineExceedsRequest
	}
	if !h.clock.Now().Before(deadline) {
		return nil, ErrDeadlineInPast
	}
	return &Token{
		host:     h,
		auth:     a,
		id:       h.nextToken(),
		request:  req,
		level:    level,
		deadline: deadline,
	}, nil
}

// Reuse revokes the active request (if any), advances the connection
// generation, and prepares the same connection for the next request.
func (c *Connection) Reuse() error {
	if c == nil || c.host == nil {
		return ErrDifferentHost
	}
	h := c.host
	h.mu.Lock()
	defer h.mu.Unlock()
	if c.request != nil && c.request.active {
		c.request.endLocked()
	}
	c.generation++
	c.open = true
	return nil
}

func (c *Connection) Close() error {
	if c == nil || c.host == nil {
		return ErrDifferentHost
	}
	h := c.host
	h.mu.Lock()
	defer h.mu.Unlock()
	if c.request != nil && c.request.active {
		c.request.endLocked()
	}
	c.open = false
	return nil
}

func (c *Connection) ID() string {
	if c == nil {
		return ""
	}
	return c.id
}

func (c *Connection) Generation() uint64 {
	if c == nil || c.host == nil {
		return 0
	}
	c.host.mu.Lock()
	defer c.host.mu.Unlock()
	return c.generation
}

func (c *Connection) ActiveRequest() *Request {
	if c == nil || c.host == nil {
		return nil
	}
	c.host.mu.Lock()
	defer c.host.mu.Unlock()
	return c.request
}

func (c *Connection) Open() bool {
	if c == nil || c.host == nil {
		return false
	}
	c.host.mu.Lock()
	defer c.host.mu.Unlock()
	return c.open
}

// Request is one authenticated request and the unit of async revocation.
type Request struct {
	host       *Host
	conn       *Connection
	id         string
	generation uint64
	level      Level
	deadline   Instant
	token      *Token
	active     bool
	tasks      map[uint64]*Task
	grants     map[uint64]*Grant
}

func (r *Request) ID() string {
	if r == nil {
		return ""
	}
	return r.id
}

func (r *Request) ConnectionID() string {
	if r == nil || r.conn == nil {
		return ""
	}
	return r.conn.id
}

func (r *Request) Generation() uint64 {
	if r == nil {
		return 0
	}
	return r.generation
}

func (r *Request) Level() Level {
	if r == nil {
		return Identification
	}
	return r.level
}

func (r *Request) Deadline() Instant {
	if r == nil {
		return 0
	}
	return r.deadline
}

func (r *Request) Token() *Token {
	if r == nil {
		return nil
	}
	return r.token
}

func (r *Request) Active() bool {
	if r == nil || r.host == nil {
		return false
	}
	r.host.mu.Lock()
	defer r.host.mu.Unlock()
	return r.active
}

// End revokes the request token, every task identity bound to this
// generation, and every host authorization grant for the request.
func (r *Request) End() error {
	if r == nil || r.host == nil {
		return ErrDifferentHost
	}
	r.host.mu.Lock()
	defer r.host.mu.Unlock()
	if !r.active {
		return nil
	}
	r.endLocked()
	return nil
}

func (r *Request) Close() error {
	return r.End()
}

func (r *Request) endLocked() {
	if r == nil || !r.active {
		return
	}
	r.active = false
	if r.conn != nil && r.conn.request == r {
		r.conn.request = nil
	}
	if r.host != nil {
		r.host.revokeTasksLocked(r.conn.id, r.generation)
	}
	for _, grant := range r.grants {
		grant.active = false
	}
}
