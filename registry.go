package netsec

import (
	"errors"
	"sync"
	"sync/atomic"
	"time"
)

// ConnectID is the QUIC stream ID of the HTTP/3 CONNECT request that
// established a WebTransport session. The registry treats it as an opaque
// routing key; uniqueness and ownership are enforced per Registry.
type ConnectID uint64

// StreamID is the QUIC stream ID of an incoming WebTransport stream. It is
// distinct from ConnectID so callers cannot accidentally swap the two.
type StreamID uint64

// Reset codes passed to Stream.Reset. They are diagnostics chosen by this
// package; applications may map them to their own WebTransport error codes.
const (
	ResetUnknownSession uint64 = 0x1
	ResetSessionClosed  uint64 = 0x2
	ResetBufferOverflow uint64 = 0x3
	ResetPendingTimeout uint64 = 0x4
	ResetPendingLimit   uint64 = 0x5
)

// Defaults applied by New when the corresponding option is zero or negative.
const (
	DefaultPendingTimeout     = 5 * time.Second
	DefaultMaxPendingConnects = 4096
	DefaultMaxPendingStreams  = 16384
)

var (
	// ErrOriginNotAllowed means the CONNECT Origin was missing or not in the
	// allowlist.
	ErrOriginNotAllowed = errors.New("netsec: CONNECT origin not allowed")
	// ErrSessionExists means a session is already open for that CONNECT ID.
	ErrSessionExists = errors.New("netsec: session already registered")
	// ErrSessionClosed means that CONNECT ID has already had a session and
	// cannot be registered again on this connection.
	ErrSessionClosed = errors.New("netsec: session already closed")
	// ErrNilSink means RegisterConnect was called without a delivery target.
	ErrNilSink = errors.New("netsec: nil session sink")
)

// Options configures a Registry.
type Options struct {
	// AllowedOrigins is an exact-match allowlist for CONNECT Origin values.
	// Invalid entries are ignored. An empty slice denies all CONNECTs.
	AllowedOrigins []string

	// PendingBufferLimit is the maximum number of stream payload bytes held
	// for one not-yet-registered CONNECT ID. A stream that would exceed the
	// limit is reset immediately with ResetBufferOverflow. Zero permits only
	// zero-length stream notifications.
	PendingBufferLimit int

	// PendingTimeout bounds how long traffic for an unregistered CONNECT ID
	// is retained. When it expires, all buffered streams are reset with
	// ResetPendingTimeout. Values <= 0 use DefaultPendingTimeout.
	PendingTimeout time.Duration

	// MaxPendingConnects bounds the number of distinct unregistered CONNECT
	// IDs tracked at once, limiting metadata allocation. Values <= 0 use
	// DefaultMaxPendingConnects.
	MaxPendingConnects int

	// MaxPendingStreams bounds the total number of buffered stream
	// notifications across all unregistered CONNECT IDs. Values <= 0 use
	// DefaultMaxPendingStreams.
	MaxPendingStreams int
}

// Stream is an incoming WebTransport stream supplied by the HTTP/3 stream
// dispatcher.
//
// Data must return a stable slice for the duration of the HandleStream call.
// The registry copies it before retaining it. Reset must reset the underlying
// stream; it may be called outside the dispatcher goroutine.
type Stream interface {
	ID() StreamID
	Data() []byte
	Reset(code uint64)
}

// StreamData is the payload delivered to a Sink. Data is owned by the
// registry and is never aliased with the dispatcher's input buffer.
type StreamData struct {
	ConnectID ConnectID
	StreamID  StreamID
	Data      []byte

	reset func(uint64)
}

// Reset asks the underlying stream to be reset. It is safe to call after
// delivery; repeated resets should be idempotent at the QUIC layer.
func (d StreamData) Reset(code uint64) {
	if d.reset != nil {
		d.reset(code)
	}
}

// Sink receives traffic that has been routed to an open session.
//
// Calls for one session are serialized in arrival order. A Sink may call
// methods on the Session or Registry from its callback; it must not retain
// Data slices or assume that Datagram payloads are reused.
type Sink interface {
	DeliverStream(session *Session, stream StreamData)
	DeliverDatagram(session *Session, data []byte)
}

// Outcome describes how the Registry handled an inbound item.
type Outcome uint8

const (
	// OutcomeDelivered means the item was handed to the session's Sink.
	OutcomeDelivered Outcome = iota
	// OutcomeBuffered means the stream is waiting for session registration.
	OutcomeBuffered
	// OutcomeReset means the stream was rejected and Stream.Reset was called.
	OutcomeReset
	// OutcomeDropped means the item was discarded (datagrams never buffer).
	OutcomeDropped
)

func (o Outcome) String() string {
	switch o {
	case OutcomeDelivered:
		return "delivered"
	case OutcomeBuffered:
		return "buffered"
	case OutcomeReset:
		return "reset"
	case OutcomeDropped:
		return "dropped"
	default:
		return "unknown"
	}
}

// ConnectRequest is the security-relevant part of a WebTransport CONNECT.
//
// StreamID must be the request stream ID observed by the HTTP/3 stack, never
// a value taken from a request header, body, or other client-controlled
// field. Otherwise a client could register a session under another session's
// routing key.
type ConnectRequest struct {
	StreamID ConnectID
	Origin   string
}

// SessionState is the lifecycle state of a registered session.
type SessionState uint8

const (
	SessionOpen SessionState = iota
	SessionClosed
)

func (s SessionState) String() string {
	if s == SessionClosed {
		return "closed"
	}
	return "open"
}

// Session is an established WebTransport session. All stream and datagram
// routing is keyed by its CONNECT stream ID.
type Session struct {
	registry *Registry
	id       ConnectID
	origin   string
	sink     Sink
	closed   atomic.Bool

	// deliverMu protects deferred and queue. The sink is never called while
	// deliverMu is held, so callbacks may re-enter the Session or Registry.
	deliverMu  sync.Mutex
	deferred   []StreamData
	queue      []delivery
	delivering bool
}

// ID returns the CONNECT stream ID that established the session.
func (s *Session) ID() ConnectID { return s.id }

// Origin returns the canonical CONNECT Origin accepted for this session.
func (s *Session) Origin() string { return s.origin }

// State reports whether the session is open or closed.
func (s *Session) State() SessionState {
	if s.closed.Load() {
		return SessionClosed
	}
	return SessionOpen
}

// Close closes the session and records a tombstone for its CONNECT ID.
// Subsequent streams for this ID are reset and subsequent datagrams are
// dropped. Close is idempotent.
func (s *Session) Close() {
	s.registry.CloseSession(s.id)
}

type deliveryKind uint8

const (
	deliveryStream deliveryKind = iota
	deliveryDatagram
)

type delivery struct {
	kind     deliveryKind
	stream   StreamData
	datagram []byte
}

// Registry routes WebTransport traffic for one HTTP/3 connection. It is safe
// for concurrent use by a stream dispatcher and timer callbacks.
//
// A Registry must not be shared between HTTP/3 connections: CONNECT stream IDs
// are only unambiguous within one connection.
type Registry struct {
	opts    Options
	allowed map[string]struct{}

	mu       sync.Mutex
	sessions map[ConnectID]*Session
	closed   map[ConnectID]struct{}
	pending  map[ConnectID]*pendingSession

	pendingBytes   int
	pendingStreams int
}

type pendingSession struct {
	streams []pendingStream
	bytes   int
	timer   *time.Timer
}

type pendingStream struct {
	id    StreamID
	data  []byte
	reset func(uint64)
}

// New creates a Registry for one HTTP/3 connection. Invalid allowlist entries
// are ignored and therefore fail closed.
func New(opts Options) *Registry {
	if opts.PendingTimeout <= 0 {
		opts.PendingTimeout = DefaultPendingTimeout
	}
	if opts.MaxPendingConnects <= 0 {
		opts.MaxPendingConnects = DefaultMaxPendingConnects
	}
	if opts.MaxPendingStreams <= 0 {
		opts.MaxPendingStreams = DefaultMaxPendingStreams
	}
	if opts.PendingBufferLimit < 0 {
		opts.PendingBufferLimit = 0
	}

	allowed := make(map[string]struct{}, len(opts.AllowedOrigins))
	for _, raw := range opts.AllowedOrigins {
		if canonical, ok := CanonicalOrigin(raw); ok {
			allowed[canonical] = struct{}{}
		}
	}

	return &Registry{
		opts:     opts,
		allowed:  allowed,
		sessions: make(map[ConnectID]*Session),
		closed:   make(map[ConnectID]struct{}),
		pending:  make(map[ConnectID]*pendingSession),
	}
}

// RegisterConnect establishes a session after validating its Origin. If
// traffic for this CONNECT ID arrived earlier, it is handed to sink in arrival
// order before newly arriving traffic.
//
// The caller should send the successful CONNECT response only after this
// method returns a Session. A rejected CONNECT must not create a session.
func (r *Registry) RegisterConnect(req ConnectRequest, sink Sink) (*Session, error) {
	if sink == nil {
		return nil, ErrNilSink
	}
	canonical, ok := CanonicalOrigin(req.Origin)
	if !ok {
		return nil, ErrOriginNotAllowed
	}
	if _, ok := r.allowed[canonical]; !ok {
		return nil, ErrOriginNotAllowed
	}

	r.mu.Lock()
	if _, ok := r.closed[req.StreamID]; ok {
		r.mu.Unlock()
		return nil, ErrSessionClosed
	}
	if _, ok := r.sessions[req.StreamID]; ok {
		r.mu.Unlock()
		return nil, ErrSessionExists
	}

	// The pending queue, if any, becomes the session's deferred delivery
	// prefix. It is written before the session becomes visible, so later
	// routing observes it under the registry lock.
	var deferred []StreamData
	if queued := r.pending[req.StreamID]; queued != nil {
		delete(r.pending, req.StreamID)
		if queued.timer != nil {
			queued.timer.Stop()
		}
		r.pendingBytes -= queued.bytes
		r.pendingStreams -= len(queued.streams)
		deferred = make([]StreamData, 0, len(queued.streams))
		for _, item := range queued.streams {
			deferred = append(deferred, StreamData{
				ConnectID: req.StreamID,
				StreamID:  item.id,
				Data:      item.data,
				reset:     item.reset,
			})
		}
		queued.streams = nil
	}

	session := &Session{
		registry: r,
		id:       req.StreamID,
		origin:   canonical,
		sink:     sink,
		deferred: deferred,
	}
	r.sessions[req.StreamID] = session
	r.mu.Unlock()

	session.activate()
	return session, nil
}

// HandleStream routes one stream chunk to the session identified by
// connectID. It returns OutcomeReset when the stream was rejected and Reset
// was invoked.
//
// connectID must be the session association established by the HTTP/3
// dispatcher, never a client-supplied value. The stream ID is routing metadata
// only; the CONNECT ID is the binding key.
func (r *Registry) HandleStream(connectID ConnectID, stream Stream) Outcome {
	if stream == nil {
		return OutcomeDropped
	}
	streamID := stream.ID()
	var reset func(uint64)
	if stream != nil {
		reset = stream.Reset
	}

	data := stream.Data()
	if len(data) > r.opts.PendingBufferLimit {
		// Avoid copying a payload that can never fit in the pending budget.
		r.mu.Lock()
		session := r.sessions[connectID]
		closed := false
		if session == nil {
			_, closed = r.closed[connectID]
		}
		r.mu.Unlock()

		if session != nil {
			// An open session has no pending byte budget; copy and deliver.
			return r.deliverLive(session, connectID, streamID, data, reset)
		}
		if closed {
			if reset != nil {
				reset(ResetSessionClosed)
			}
			return OutcomeReset
		}
		if reset != nil {
			reset(ResetBufferOverflow)
		}
		return OutcomeReset
	}

	r.mu.Lock()
	if session := r.sessions[connectID]; session != nil {
		r.mu.Unlock()
		return r.deliverLive(session, connectID, streamID, data, reset)
	}
	if _, ok := r.closed[connectID]; ok {
		r.mu.Unlock()
		if reset != nil {
			reset(ResetSessionClosed)
		}
		return OutcomeReset
	}

	queued := r.pending[connectID]
	if queued == nil {
		if len(r.pending) >= r.opts.MaxPendingConnects {
			r.mu.Unlock()
			if reset != nil {
				reset(ResetPendingLimit)
			}
			return OutcomeReset
		}
		queued = &pendingSession{}
		r.pending[connectID] = queued
		queued.timer = time.AfterFunc(r.opts.PendingTimeout, func() {
			r.expire(connectID, queued)
		})
	}

	if r.pendingStreams >= r.opts.MaxPendingStreams {
		if len(queued.streams) == 0 {
			delete(r.pending, connectID)
			queued.timer.Stop()
		}
		r.mu.Unlock()
		if reset != nil {
			reset(ResetPendingLimit)
		}
		return OutcomeReset
	}

	payload := copyBytes(data)
	queued.bytes += len(payload)
	if queued.bytes > r.opts.PendingBufferLimit {
		queued.bytes -= len(payload)
		if len(queued.streams) == 0 {
			delete(r.pending, connectID)
			queued.timer.Stop()
		}
		r.mu.Unlock()
		if reset != nil {
			reset(ResetBufferOverflow)
		}
		return OutcomeReset
	}

	queued.streams = append(queued.streams, pendingStream{
		id:    streamID,
		data:  payload,
		reset: reset,
	})
	r.pendingBytes += len(payload)
	r.pendingStreams++
	r.mu.Unlock()
	return OutcomeBuffered
}

func (r *Registry) deliverLive(session *Session, connectID ConnectID, streamID StreamID, data []byte, reset func(uint64)) Outcome {
	session.enqueue(delivery{
		kind: deliveryStream,
		stream: StreamData{
			ConnectID: connectID,
			StreamID:  streamID,
			Data:      copyBytes(data),
			reset:     reset,
		},
	})
	return OutcomeDelivered
}

// HandleDatagram routes a WebTransport datagram by CONNECT ID. Datagrams are
// lossy by design: those for unregistered, pending, or closed IDs are dropped
// and are never buffered.
func (r *Registry) HandleDatagram(connectID ConnectID, data []byte) Outcome {
	r.mu.Lock()
	session := r.sessions[connectID]
	r.mu.Unlock()
	if session == nil {
		return OutcomeDropped
	}
	session.enqueue(delivery{
		kind:     deliveryDatagram,
		datagram: copyBytes(data),
	})
	return OutcomeDelivered
}

// CloseSession closes the session for connectID and records a tombstone.
// It reports whether an open session was transitioned to closed.
func (r *Registry) CloseSession(connectID ConnectID) bool {
	r.mu.Lock()
	session := r.sessions[connectID]
	if session == nil {
		r.mu.Unlock()
		return false
	}
	delete(r.sessions, connectID)
	r.closed[connectID] = struct{}{}
	session.closed.Store(true)
	r.mu.Unlock()

	// If delivery is already running it will observe closed before handing
	// out any queued item. Otherwise this drains and resets queued/deferred
	// traffic now.
	session.activate()
	return true
}

// ActiveSessions returns the number of open sessions.
func (r *Registry) ActiveSessions() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.sessions)
}

// PendingConnects returns the number of distinct CONNECT IDs with buffered
// traffic.
func (r *Registry) PendingConnects() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.pending)
}

// PendingBytes returns the number of stream payload bytes currently buffered
// for unregistered CONNECT IDs.
func (r *Registry) PendingBytes() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.pendingBytes
}

func (r *Registry) expire(connectID ConnectID, expected *pendingSession) {
	r.mu.Lock()
	queued := r.pending[connectID]
	if queued == nil || queued != expected {
		r.mu.Unlock()
		return
	}
	delete(r.pending, connectID)
	r.pendingBytes -= queued.bytes
	r.pendingStreams -= len(queued.streams)
	streams := queued.streams
	queued.streams = nil
	r.mu.Unlock()

	for _, item := range streams {
		if item.reset != nil {
			item.reset(ResetPendingTimeout)
		}
	}
}

func (s *Session) activate() {
	s.deliverMu.Lock()
	if s.delivering {
		s.deliverMu.Unlock()
		return
	}
	s.delivering = true
	s.prependDeferredLocked()
	s.deliverMu.Unlock()
	s.drain()
}

func (s *Session) enqueue(item delivery) {
	s.deliverMu.Lock()
	if s.delivering {
		s.queue = append(s.queue, item)
		s.deliverMu.Unlock()
		return
	}
	s.delivering = true
	s.prependDeferredLocked()
	s.queue = append(s.queue, item)
	s.deliverMu.Unlock()
	s.drain()
}

func (s *Session) prependDeferredLocked() {
	if len(s.deferred) == 0 {
		return
	}
	prefix := make([]delivery, 0, len(s.deferred)+len(s.queue))
	for _, stream := range s.deferred {
		prefix = append(prefix, delivery{kind: deliveryStream, stream: stream})
	}
	s.deferred = nil
	prefix = append(prefix, s.queue...)
	s.queue = prefix
}

func (s *Session) drain() {
	for {
		s.deliverMu.Lock()
		if len(s.queue) == 0 {
			s.queue = nil
			s.delivering = false
			s.deliverMu.Unlock()
			return
		}
		item := s.queue[0]
		s.queue[0] = delivery{}
		s.queue = s.queue[1:]
		s.deliverMu.Unlock()

		s.deliverOne(item)
	}
}

func (s *Session) deliverOne(item delivery) {
	if item.kind == deliveryDatagram {
		if !s.closed.Load() {
			s.sink.DeliverDatagram(s, item.datagram)
		}
		return
	}
	if s.closed.Load() {
		item.stream.Reset(ResetSessionClosed)
		return
	}
	s.sink.DeliverStream(s, item.stream)
}

func copyBytes(data []byte) []byte {
	if len(data) == 0 {
		return nil
	}
	return append([]byte(nil), data...)
}
