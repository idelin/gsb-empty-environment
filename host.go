package netsec

import (
	"sync"
	"time"
)

type hostConfig struct {
	clock           Clock
	primaryLevel    Level
	primaryDeadline Instant
	executor        Executor
}

// HostOption configures a Host.
type HostOption func(*hostConfig)

func WithClock(c Clock) HostOption {
	return func(cfg *hostConfig) {
		if c != nil {
			cfg.clock = c
		}
	}
}

func WithPrimaryLevel(l Level) HostOption {
	return func(cfg *hostConfig) {
		cfg.primaryLevel = l
	}
}

func WithPrimaryDeadline(d Instant) HostOption {
	return func(cfg *hostConfig) {
		cfg.primaryDeadline = d
	}
}

// WithExecutor makes SpawnAsync submit newly created task handles to e. A
// deterministic scheduler can be supplied here for reproducible workloads.
func WithExecutor(e Executor) HostOption {
	return func(cfg *hostConfig) {
		cfg.executor = e
	}
}

// Host owns the fixed process token and the live request/task registry.
type Host struct {
	mu sync.Mutex

	clock           Clock
	primary         Identity
	primaryLevel    Level
	primaryDeadline Instant
	primaryToken    *Token
	auth            *Authenticator

	nextTokenID uint64
	nextTaskID  uint64
	nextGrantID uint64

	connections map[string]*Connection
	tasks       map[uint64]*Task
	executor    Executor
}

// NewHost creates a host with a fixed primary token. The primary token is the
// thread-stack base and is never accepted by Enter as a client identity.
func NewHost(primary Identity, options ...HostOption) *Host {
	cfg := hostConfig{
		clock:           NewSystemClock(),
		primaryLevel:    Delegation,
		primaryDeadline: MaxInstant,
	}
	for _, option := range options {
		if option != nil {
			option(&cfg)
		}
	}
	if cfg.clock == nil {
		cfg.clock = NewSystemClock()
	}
	if !cfg.primaryLevel.Valid() {
		cfg.primaryLevel = Delegation
	}
	if cfg.primaryDeadline == 0 {
		cfg.primaryDeadline = MaxInstant
	}

	h := &Host{
		clock:           cfg.clock,
		primary:         primary,
		primaryLevel:    cfg.primaryLevel,
		primaryDeadline: cfg.primaryDeadline,
		connections:     make(map[string]*Connection),
		tasks:           make(map[uint64]*Task),
		executor:        cfg.executor,
	}
	h.primaryToken = &Token{
		host:     h,
		primary:  true,
		level:    h.primaryLevel,
		deadline: h.primaryDeadline,
	}
	h.auth = &Authenticator{host: h}
	return h
}

func (h *Host) Now() Instant {
	if h == nil || h.clock == nil {
		return ZeroInstant
	}
	return h.clock.Now()
}

func (h *Host) PrimaryIdentity() Identity {
	if h == nil {
		return Identity{}
	}
	return h.primary
}

func (h *Host) Primary() *Token {
	if h == nil {
		return nil
	}
	return h.primaryToken
}

func (h *Host) Authenticator() *Authenticator {
	if h == nil {
		return nil
	}
	return h.auth
}

func (h *Host) NewThread() *Thread {
	if h == nil {
		return nil
	}
	return &Thread{host: h, primary: h.primaryToken}
}

func (h *Host) Connection(id string) (*Connection, error) {
	if h == nil {
		return nil, ErrDifferentHost
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	conn, ok := h.connections[id]
	if !ok {
		return nil, ErrConnectionNotFound
	}
	return conn, nil
}

func (h *Host) nextToken() uint64 {
	h.nextTokenID++
	return h.nextTokenID
}

func (h *Host) nextTask() uint64 {
	h.nextTaskID++
	return h.nextTaskID
}

func (h *Host) nextGrant() uint64 {
	h.nextGrantID++
	return h.nextGrantID
}

func (h *Host) verifyRequestLocked(req *Request) error {
	if req == nil || req.host != h {
		return ErrRequestInactive
	}
	if !req.active {
		return ErrRequestEnded
	}
	if req.conn == nil || !req.conn.open {
		return ErrConnectionClosed
	}
	if req.conn.generation != req.generation {
		return ErrGenerationRevoked
	}
	return nil
}

func (h *Host) verifyTokenLocked(tok *Token) error {
	if tok == nil || tok.host != h {
		return ErrInvalidToken
	}
	if tok.primary {
		return nil
	}
	if tok.auth != h.auth || tok.id == 0 || tok.request == nil {
		return ErrInvalidToken
	}
	if !tok.level.Valid() {
		return ErrInvalidLevel
	}
	if err := h.verifyRequestLocked(tok.request); err != nil {
		return err
	}
	if tok.request != tok.request.conn.request {
		return ErrRequestMismatch
	}
	if tok.deadline.After(tok.request.deadline) {
		return ErrInvalidDeadline
	}
	if !h.clock.Now().Before(tok.deadline) {
		return ErrTokenExpired
	}
	return nil
}

func (h *Host) revokeTasksLocked(connectionID string, generation uint64) {
	for _, task := range h.tasks {
		if task.connectionID != connectionID || task.generation != generation {
			continue
		}
		switch task.state {
		case taskPending, taskRunning:
			task.state = taskRevoked
			task.err = ErrTaskRevoked
		}
	}
}

// Authorize issues an unforgeable, request-bound capability. Possession of the
// returned grant is what lets a task retain a level above Identification.
func (h *Host) Authorize(req *Request, level Level, deadline Instant) (*Grant, error) {
	if h == nil {
		return nil, ErrDifferentHost
	}
	if !level.Valid() {
		return nil, ErrInvalidLevel
	}
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
	grant := &Grant{
		host:      h,
		id:        h.nextGrant(),
		request:   req,
		connID:    req.conn.id,
		requestID: req.id,
		gen:       req.generation,
		level:     level,
		deadline:  deadline,
		active:    true,
	}
	req.grants[grant.id] = grant
	return grant, nil
}

func (h *Host) verifyGrantLocked(grant *Grant, req *Request, level Level, deadline Instant) error {
	if grant == nil || grant.host != h || grant.id == 0 {
		return ErrInvalidGrant
	}
	if !grant.active {
		return ErrGrantRevoked
	}
	if grant.request != req || grant.connID != req.conn.id ||
		grant.requestID != req.id || grant.gen != req.generation {
		return ErrInvalidGrant
	}
	if level > grant.level {
		return ErrGrantInsufficient
	}
	if deadline.After(grant.deadline) {
		return ErrDeadlineExceedsGrant
	}
	return nil
}

// CheckAccess validates a token immediately before one resource call. It is
// deliberately stateful-free: callers must invoke it for every call, not once
// per callback.
func (h *Host) CheckAccess(req *Request, tok *Token, target ResourceKind) error {
	if h == nil {
		return ErrDifferentHost
	}
	h.mu.Lock()
	defer h.mu.Unlock()

	if req == nil {
		return ErrNoCurrentRequest
	}
	if err := h.verifyRequestLocked(req); err != nil {
		return err
	}
	if err := h.verifyTokenLocked(tok); err != nil {
		return err
	}
	if tok.request != req {
		return ErrTokenRequestMismatch
	}
	return checkLevel(tok.level, target)
}

// Access revalidates the token and then performs one local or remote call.
func (h *Host) Access(req *Request, tok *Token, target ResourceKind, call func() error) error {
	if err := h.CheckAccess(req, tok, target); err != nil {
		return err
	}
	if call == nil {
		return nil
	}
	return call()
}

// ReadIdentity returns a read-only identity snapshot. Identification tokens
// are allowed here; they are not allowed to reach CheckAccess.
func (h *Host) ReadIdentity(tok *Token) (Snapshot, error) {
	if h == nil {
		return Snapshot{}, ErrDifferentHost
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if err := h.verifyTokenLocked(tok); err != nil {
		return Snapshot{}, err
	}
	return tok.snapshotLocked(), nil
}

// CheckThreadAccess uses the current stack top and the thread's bound request.
// It is the thread-stack counterpart of CheckAccess.
func (h *Host) CheckThreadAccess(thread *Thread, target ResourceKind) error {
	if thread == nil || thread.host != h {
		return ErrNoCurrentRequest
	}
	thread.mu.Lock()
	tok := thread.primary
	if len(thread.stack) > 0 {
		tok = thread.stack[len(thread.stack)-1]
	}
	req := thread.bound
	if tok == nil || tok.primary || req == nil {
		thread.mu.Unlock()
		return ErrNoCurrentRequest
	}
	// Preserve lock order thread -> host so the binding cannot change between
	// selecting the top token and revalidating it.
	err := h.CheckAccess(req, tok, target)
	thread.mu.Unlock()
	return err
}

// AccessFromThread revalidates the thread's current identity for every call.
func (h *Host) AccessFromThread(thread *Thread, target ResourceKind, call func() error) error {
	if err := h.CheckThreadAccess(thread, target); err != nil {
		return err
	}
	if call == nil {
		return nil
	}
	return call()
}

func checkLevel(level Level, target ResourceKind) error {
	if !target.valid() {
		return ErrInvalidResourceKind
	}
	if !level.Valid() {
		return ErrInvalidLevel
	}
	if level == Identification {
		return ErrIdentityCannotAccessResources
	}
	if target == RemoteResource && level < Delegation {
		return ErrRemoteRequiresDelegation
	}
	return nil
}

// DeadlineAfter is a convenience for building an instant on the host clock.
func (h *Host) DeadlineAfter(ttl time.Duration) Instant {
	return h.Now().Add(ttl)
}
