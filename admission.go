// Package netsec provides embeddable stream-admission primitives for a
// reverse proxy that must survive HTTP/2-style rapid-reset traffic.
//
// The central type is Controller, which turns the life of a proxied stream
// into an explicit state machine and bounds the number of in-flight tokens.
// A token is handed back only after BOTH the stream side and the
// backend-work side have released it, so a stream reset can never return
// capacity that orphaned backend work is still consuming.
package netsec

import (
	"context"
	"errors"
	"sync"
)

// StreamState is the explicit lifecycle state of an admitted stream.
type StreamState int

const (
	// StateQueued: the stream waits for an admission token.
	StateQueued StreamState = iota
	// StateActive: admitted; the stream is alive, backend not started yet.
	StateActive
	// StateStarting: backend enumeration/dial is in progress.
	StateStarting
	// StateWorking: backend work is running, stream still alive.
	StateWorking
	// StateReset: the stream was reset; any backend work keeps draining.
	StateReset
	// StateReleased: terminal, all capacity has been returned.
	StateReleased
)

// String renders a state for diagnostics.
func (s StreamState) String() string {
	switch s {
	case StateQueued:
		return "queued"
	case StateActive:
		return "active"
	case StateStarting:
		return "starting"
	case StateWorking:
		return "working"
	case StateReset:
		return "reset"
	case StateReleased:
		return "released"
	default:
		return "unknown"
	}
}

var (
	// ErrQueueFull is returned when a connection already holds MaxQueuedPerConn
	// waiting streams.
	ErrQueueFull = errors.New("netsec: admission queue full for connection")
	// ErrConnectionClosed is returned when the owning connection is closed while
	// a stream is queued, or when a method targets an unknown connection.
	ErrConnectionClosed = errors.New("netsec: connection closed")
	// ErrInvalidTransition is returned for a lifecycle call that the current
	// state does not permit.
	ErrInvalidTransition = errors.New("netsec: invalid stream state transition")
	// ErrStaleTicket is returned when a ticket can no longer be matched to a
	// live (connection, stream) pair: the entry was already fully released, so
	// the callback belongs to an old stream number and must have no effect.
	ErrStaleTicket = errors.New("netsec: stale stream ticket")
	// ErrControllerClosed is returned after Controller.Shutdown.
	ErrControllerClosed = errors.New("netsec: controller shut down")
	// ErrExhausted is returned by Enumerator.Next when no backends remain.
	ErrExhausted = errors.New("netsec: backend enumerator exhausted")
)

// Config configures a Controller.
type Config struct {
	// MaxInFlight bounds simultaneously admitted streams plus backend work
	// that has not been reclaimed.
	MaxInFlight int
	// MaxActivePerConn bounds streams of one connection that hold a token at
	// the same time.
	MaxActivePerConn int
	// MaxQueuedPerConn bounds the number of queued streams per connection.
	MaxQueuedPerConn int
}

func (c Config) withDefaults() Config {
	if c.MaxInFlight <= 0 {
		c.MaxInFlight = 100
	}
	if c.MaxActivePerConn <= 0 {
		c.MaxActivePerConn = c.MaxInFlight
	}
	if c.MaxQueuedPerConn <= 0 {
		c.MaxQueuedPerConn = c.MaxActivePerConn
	}
	return c
}

// ConnID identifies a proxy-facing connection.
type ConnID uint64

// StreamID identifies a stream within a connection (e.g. an HTTP/2 stream id).
type StreamID uint32

// Ticket represents one stream's admission slot. A Ticket is bound to a
// concrete (connection, stream) pair; after the entry is released the ticket
// becomes stale and every lifecycle method returns ErrStaleTicket.
type Ticket struct {
	connID  ConnID
	stream  StreamID
	gen     uint64
	connGen uint64
}

// ConnID returns the owning connection identifier.
func (t Ticket) ConnID() ConnID { return t.connID }

// Stream returns the stream number within the connection.
func (t Ticket) Stream() StreamID { return t.stream }

type entry struct {
	connID  ConnID
	stream  StreamID
	gen     uint64
	connGen uint64
	state   StreamState

	queued bool

	streamDone bool
	workDone   bool
	detached   bool // owning connection generation is gone; drains alone

	// granted is closed once the ticket leaves the queue, either with a token
	// or with a terminal queued failure.
	granted  chan struct{}
	grantErr error

	// resetCh is closed at most once when the stream is reset, allowing
	// backend work to observe cancellation.
	resetCh   chan struct{}
	resetOnce sync.Once
}

type connState struct {
	active int // entries holding a token
	queued int // entries waiting in the global queue
	closed bool
	gen    uint64 // connection generation for ConnID reuse
}

// Controller is an embeddable admission controller. The zero value is not
// usable; construct one with New.
type Controller struct {
	cfg Config

	mu      sync.Mutex
	cond    *sync.Cond
	conns   map[ConnID]*connState
	entries map[Ticket]*entry
	active  map[Ticket]*entry // keyed without generation: live (conn, stream)
	queue   []*entry          // global FIFO of queued entries

	inflight int
	granted  uint64
	rejected uint64
	reset    uint64
	nextGen  uint64
	connGen  uint64

	closed bool
}

// New builds a Controller with the given limits.
func New(cfg Config) *Controller {
	c := &Controller{
		cfg:     cfg.withDefaults(),
		conns:   make(map[ConnID]*connState),
		entries: make(map[Ticket]*entry),
		active:  make(map[Ticket]*entry),
	}
	c.cond = sync.NewCond(&c.mu)
	return c
}

// OpenConn registers a proxy-facing connection.
func (c *Controller) OpenConn(id ConnID) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.conns[id] == nil {
		c.connGen++
		c.conns[id] = &connState{gen: c.connGen}
	}
}

// CloseConn marks the connection closed. Queued streams fail with
// ErrConnectionClosed and are removed; streams still holding tokens are kept
// until their backend work is reclaimed, but the ticket is decoupled from the
// connection so old stream-number callbacks can never touch a later
// connection that reuses the same ConnID.
func (c *Controller) CloseConn(id ConnID) {
	c.mu.Lock()
	defer c.mu.Unlock()
	cs := c.conns[id]
	if cs == nil {
		return
	}
	cs.closed = true

	// Fail every queued entry of this connection and compact the FIFO.
	kept := c.queue[:0]
	for _, e := range c.queue {
		if e.connID == id {
			cs.queued--
			c.rejected++
			e.queued = false
			e.state = StateReleased
			e.grantErr = ErrConnectionClosed
			close(e.granted)
			c.deleteEntryLocked(e)
			continue
		}
		kept = append(kept, e)
	}
	c.queue = kept

	// Detach entries still holding tokens: they drain against a dead
	// connection generation, so a reopened ConnID gets a clean namespace and
	// old stream-number callbacks cannot match new streams.
	for _, e := range c.entries {
		if e.connID == id && e.connGen == cs.gen {
			delete(c.active, Ticket{connID: e.connID, stream: e.stream, connGen: e.connGen})
			cs.active--
			e.detached = true
		}
	}

	if cs.active == 0 && cs.queued == 0 {
		delete(c.conns, id)
	}
	c.scheduleLocked()
	c.cond.Broadcast()
}

// Admit queues a stream and waits until it is admitted, the context expires,
// or the connection/controller is closed. On success the returned Ticket owns
// one in-flight token that must eventually be released via StreamEnd and
// WorkFinished (or Abandon).
func (c *Controller) Admit(ctx context.Context, id ConnID, stream StreamID) (Ticket, error) {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return Ticket{}, ErrControllerClosed
	}
	cs := c.conns[id]
	if cs == nil {
		c.mu.Unlock()
		return Ticket{}, ErrConnectionClosed
	}
	if cs.queued >= c.cfg.MaxQueuedPerConn {
		c.mu.Unlock()
		return Ticket{}, ErrQueueFull
	}

	e := &entry{
		connID:  id,
		stream:  stream,
		state:   StateQueued,
		queued:  true,
		granted: make(chan struct{}),
		resetCh: make(chan struct{}),
	}
	dupKey := Ticket{connID: id, stream: stream, connGen: cs.gen}
	if _, dup := c.active[dupKey]; dup {
		c.mu.Unlock()
		return Ticket{}, ErrInvalidTransition
	}
	c.nextGen++
	t := Ticket{connID: id, stream: stream, gen: c.nextGen, connGen: cs.gen}
	e.gen = t.gen
	e.connGen = cs.gen
	c.entries[t] = e
	c.active[dupKey] = e
	c.queue = append(c.queue, e)
	cs.queued++
	c.scheduleLocked()
	c.mu.Unlock()

	select {
	case <-e.granted:
		if e.grantErr != nil {
			return Ticket{}, e.grantErr
		}
		return t, nil
	case <-ctx.Done():
		c.mu.Lock()
		defer c.mu.Unlock()
		// The ticket may have been granted concurrently; then the caller owns a
		// token and must release it through the normal lifecycle.
		select {
		case <-e.granted:
			if e.grantErr != nil {
				return Ticket{}, e.grantErr
			}
			return t, nil
		default:
		}
		if e.queued {
			c.removeQueuedLocked(e)
			e.state = StateReleased
			e.grantErr = ctx.Err()
			close(e.granted)
		}
		return Ticket{}, ctx.Err()
	}
}

func (c *Controller) removeQueuedLocked(e *entry) {
	for i, q := range c.queue {
		if q == e {
			c.queue = append(c.queue[:i], c.queue[i+1:]...)
			break
		}
	}
	e.queued = false
	if cs := c.conns[e.connID]; cs != nil {
		cs.queued--
	}
	c.deleteEntryLocked(e)
	c.scheduleLocked()
}

// scheduleLocked walks the global FIFO and grants every entry that can
// currently run. Entries blocked by per-connection limits are skipped, so a
// single connection can never starve later connections: every eligible entry
// ahead of a blocked one makes progress, and once it drains the blocked
// connection's head is reached again.
func (c *Controller) scheduleLocked() {
	kept := c.queue[:0]
	for _, e := range c.queue {
		cs := c.conns[e.connID]
		// Defensive: entries are removed on grant; anything already detached
		// must never be granted twice.
		if !e.queued {
			continue
		}
		if c.inflight < c.cfg.MaxInFlight &&
			cs != nil && !cs.closed &&
			cs.active < c.cfg.MaxActivePerConn {
			c.grantLocked(e, cs)
			continue
		}
		kept = append(kept, e)
	}
	c.queue = kept
}

func (c *Controller) grantLocked(e *entry, cs *connState) {
	e.queued = false
	e.state = StateActive
	cs.queued--
	cs.active++
	c.inflight++
	c.granted++
	close(e.granted)
}

func ticketOf(e *entry) Ticket {
	return Ticket{connID: e.connID, stream: e.stream, gen: e.gen, connGen: e.connGen}
}

// deleteEntryLocked drops every index that points at e. The active map is
// keyed without a generation, so once removed a reused stream number gets a
// fresh generation and stale tickets cannot reach it.
func (c *Controller) deleteEntryLocked(e *entry) {
	delete(c.entries, ticketOf(e))
	delete(c.active, Ticket{connID: e.connID, stream: e.stream, connGen: e.connGen})
}

func (c *Controller) lookup(t Ticket) (*entry, error) {
	e, ok := c.entries[t]
	if !ok {
		return nil, ErrStaleTicket
	}
	return e, nil
}

// MarkBackendStarting moves an admitted stream into backend enumeration/dial.
func (c *Controller) MarkBackendStarting(t Ticket) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, err := c.lookup(t)
	if err != nil {
		return err
	}
	switch e.state {
	case StateActive, StateReset:
		e.state = StateStarting
		return nil
	default:
		return ErrInvalidTransition
	}
}

// MarkBackendWorking marks the backend work as running.
func (c *Controller) MarkBackendWorking(t Ticket) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, err := c.lookup(t)
	if err != nil {
		return err
	}
	switch e.state {
	case StateStarting, StateReset:
		e.state = StateWorking
		return nil
	default:
		return ErrInvalidTransition
	}
}

// MarkReset records a client reset. The stream side is released, but the
// in-flight token stays held until WorkFinished reclaims the backend work.
func (c *Controller) MarkReset(t Ticket) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, err := c.lookup(t)
	if err != nil {
		return err
	}
	if e.streamDone {
		// Repeated RST_STREAM frames (or racing reset/end callbacks) are
		// idempotent and must not surface an error to the transport.
		return nil
	}
	e.streamDone = true
	e.state = StateReset
	c.reset++
	e.resetOnce.Do(func() { close(e.resetCh) })
	c.releaseLocked(e)
	return nil
}

// StreamEnd records normal completion of the stream side.
func (c *Controller) StreamEnd(t Ticket) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, err := c.lookup(t)
	if err != nil {
		return err
	}
	if e.streamDone {
		return ErrInvalidTransition
	}
	e.streamDone = true
	c.releaseLocked(e)
	return nil
}

// WorkFinished reclaims the backend-work side. The token is returned to the
// pool only when both sides are done.
func (c *Controller) WorkFinished(t Ticket) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, err := c.lookup(t)
	if err != nil {
		return err
	}
	if e.workDone {
		return ErrInvalidTransition
	}
	e.workDone = true
	c.releaseLocked(e)
	return nil
}

// Abandon is a convenience for an admitted stream whose backend work never
// started: it releases both sides at once and returns the token.
func (c *Controller) Abandon(t Ticket) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, err := c.lookup(t)
	if err != nil {
		return err
	}
	if !e.streamDone {
		e.streamDone = true
	}
	if !e.workDone {
		e.workDone = true
	}
	c.releaseLocked(e)
	return nil
}

// releaseLocked performs the two-phase release. Capacity is returned exactly
// once, only after both the stream side and the work side are done.
func (c *Controller) releaseLocked(e *entry) {
	if e.state == StateReleased || !e.streamDone || !e.workDone {
		return
	}
	e.state = StateReleased

	c.inflight--
	if cs := c.conns[e.connID]; cs != nil {
		if !e.detached && cs.gen == e.connGen {
			cs.active--
		}
		if cs.closed && cs.active == 0 && cs.queued == 0 {
			delete(c.conns, e.connID)
		}
	}
	c.deleteEntryLocked(e)

	c.scheduleLocked()
	c.cond.Broadcast()
}

// Stats is a point-in-time snapshot of controller counters.
type Stats struct {
	InFlight    int
	Queued      int
	Connections int
	Granted     uint64
	Rejected    uint64
	Resets      uint64
}

// Stats returns a snapshot of the current counters.
func (c *Controller) Stats() Stats {
	c.mu.Lock()
	defer c.mu.Unlock()
	return Stats{
		InFlight:    c.inflight,
		Queued:      len(c.queue),
		Connections: len(c.conns),
		Granted:     c.granted,
		Rejected:    c.rejected,
		Resets:      c.reset,
	}
}

// State returns the current explicit state of a ticket, or StateReleased with
// ok=false if the ticket is stale (already fully released or unknown).
func (c *Controller) State(t Ticket) (state StreamState, ok bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, err := c.lookup(t)
	if err != nil {
		return StateReleased, false
	}
	return e.state, true
}

// ResetContext returns a context derived from parent that is canceled as soon
// as the stream is reset. If the ticket is already stale the returned
// context is immediately canceled, so a backend goroutine racing with
// teardown still stops promptly. The caller must invoke the returned cancel
// function to release parent-related resources.
func (c *Controller) ResetContext(parent context.Context, t Ticket) (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithCancel(parent)
	c.mu.Lock()
	e, err := c.lookup(t)
	var reset <-chan struct{}
	if err == nil {
		reset = e.resetCh
	}
	c.mu.Unlock()
	if err != nil {
		cancel()
		return ctx, cancel
	}
	go func() {
		select {
		case <-reset:
			cancel()
		case <-ctx.Done():
		}
	}()
	return ctx, cancel
}

// Shutdown stops granting new streams. Queued streams fail with
// ErrControllerClosed; already admitted streams keep draining and their
// tokens still count against InFlight until released.
func (c *Controller) Shutdown() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return
	}
	c.closed = true
	for _, e := range c.queue {
		e.queued = false
		e.state = StateReleased
		e.grantErr = ErrControllerClosed
		close(e.granted)
		c.deleteEntryLocked(e)
		if cs := c.conns[e.connID]; cs != nil {
			cs.queued--
		}
	}
	c.queue = nil
	c.cond.Broadcast()
}

// WaitDrained blocks until no token is in flight. Typically used after
// Shutdown to wait for all orphaned backend work to be reclaimed.
func (c *Controller) WaitDrained() {
	c.mu.Lock()
	defer c.mu.Unlock()
	for c.inflight != 0 {
		c.cond.Wait()
	}
}
