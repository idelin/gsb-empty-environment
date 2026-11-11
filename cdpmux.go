package cdpmux

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
)

// ErrConnClosed is returned (directly or wrapped) by every in-flight call when
// the underlying connection is closed. All waiters receive the same instance so
// errors.Is(err, ErrConnClosed) keeps working across wrapping layers.
var ErrConnClosed = errors.New("cdpmux: connection closed")

// ErrSessionDetached is returned by calls made on a session after its target has
// been detached.
var ErrSessionDetached = errors.New("cdpmux: session detached")

// connClosedError is the single shared error that fails every waiter when the
// transport dies. It matches ErrConnClosed via Is while still Unwrap-ing the
// underlying transport cause.
type connClosedError struct {
	cause error
}

func (e *connClosedError) Error() string {
	return fmt.Sprintf("%v: %v", ErrConnClosed, e.cause)
}

func (e *connClosedError) Is(target error) bool {
	return target == ErrConnClosed
}

func (e *connClosedError) Unwrap() error {
	return e.cause
}

// ResponseError mirrors the "error" object of a CDP command response.
type ResponseError struct {
	Code    int64  `json:"code"`
	Message string `json:"message"`
	Data    string `json:"data,omitempty"`
}

func (e *ResponseError) Error() string {
	if e.Data != "" {
		return fmt.Sprintf("cdp error %d: %s (%s)", e.Code, e.Message, e.Data)
	}
	return fmt.Sprintf("cdp error %d: %s", e.Code, e.Message)
}

// Message is a raw CDP protocol message. A command carries Method and Params,
// a response carries Result or Error, an event carries Method and SessionID.
type Message struct {
	ID        int64           `json:"id,omitempty"`
	SessionID string          `json:"sessionId,omitempty"`
	Method    string          `json:"method,omitempty"`
	Params    json.RawMessage `json:"params,omitempty"`
	Result    json.RawMessage `json:"result,omitempty"`
	Error     *ResponseError  `json:"error,omitempty"`
}

// Event is a session-scoped (or browser-level) protocol notification.
type Event struct {
	Method    string
	Params    json.RawMessage
	SessionID string
}

// Transport abstracts the underlying CDP connection. The mux owns it for the
// lifetime of the multiplexer: Close must be safe to call multiple times and
// must unblock any blocked Recv call.
type Transport interface {
	// Send writes one JSON-encoded protocol frame.
	Send(data []byte) error
	// Recv blocks until the next JSON-encoded protocol frame arrives.
	Recv() ([]byte, error)
	// Close tears down the connection and unblocks blocked Recv calls.
	Close() error
}

const defaultEventBuffer = 64

type pendingCall struct {
	resultCh chan Message
}

// Mux multiplexes a single CDP Transport across many page sessions.
// It matches command responses by message id, routes events by sessionId and
// fans connection-level failures out to every waiting caller.
type Mux struct {
	transport Transport
	logger    func(format string, args ...any)
	eventBuf  int

	mu       sync.Mutex
	nextID   int64
	pending  map[int64]*pendingCall
	sessions map[string]*Session

	browserEvents chan Event

	closeOnce sync.Once
	done      chan struct{}
	closeErr  error
	closeWG   sync.WaitGroup
}

// Option customizes a Mux at construction time.
type Option func(*Mux)

// WithEventBuffer sets the per-session (and browser-level) event channel
// capacity. A subscriber that falls behind has its events dropped instead of
// blocking delivery to any other session.
func WithEventBuffer(size int) Option {
	return func(m *Mux) {
		if size > 0 {
			m.eventBuf = size
		}
	}
}

// WithLogger installs a diagnostic logger used for protocol anomalies.
func WithLogger(logf func(format string, args ...any)) Option {
	return func(m *Mux) {
		if logf != nil {
			m.logger = logf
		}
	}
}

// New wraps transport and starts the read loop. Call Close to release it.
func New(transport Transport, opts ...Option) *Mux {
	m := &Mux{
		transport:     transport,
		logger:        func(string, ...any) {},
		eventBuf:      defaultEventBuffer,
		pending:       make(map[int64]*pendingCall),
		sessions:      make(map[string]*Session),
		browserEvents: make(chan Event, defaultEventBuffer),
		done:          make(chan struct{}),
	}
	for _, opt := range opts {
		opt(m)
	}
	if cap(m.browserEvents) != m.eventBuf {
		m.browserEvents = make(chan Event, m.eventBuf)
	}

	m.closeWG.Add(1)
	go m.readLoop()
	return m
}

// Send issues a browser-level command and blocks until its response arrives or
// ctx / the connection terminates.
func (m *Mux) Send(ctx context.Context, method string, params any) (json.RawMessage, error) {
	return m.send(ctx, "", method, params)
}

func (m *Mux) send(ctx context.Context, sessionID, method string, params any) (json.RawMessage, error) {
	raw, err := marshalParams(params)
	if err != nil {
		return nil, err
	}

	m.mu.Lock()
	if m.isClosedLocked() {
		m.mu.Unlock()
		return nil, m.closeErr
	}
	m.nextID++
	id := m.nextID
	call := &pendingCall{resultCh: make(chan Message, 1)}
	m.pending[id] = call
	m.mu.Unlock()

	msg := Message{ID: id, Method: method, Params: raw}
	if sessionID != "" {
		msg.SessionID = sessionID
	}
	frame, err := json.Marshal(msg)
	if err != nil {
		m.removePending(id)
		return nil, err
	}
	if err := m.transport.Send(frame); err != nil {
		m.removePending(id)
		m.shutdown(fmt.Errorf("cdpmux: transport send failed: %w", err))
		return nil, m.CloseErr()
	}

	select {
	case resp := <-call.resultCh:
		if resp.Error != nil {
			return resp.Result, resp.Error
		}
		return resp.Result, nil
	case <-ctx.Done():
		result, err := m.removePending(id)
		if err != nil {
			// The response landed while ctx was being canceled; honor it.
			return result, err
		}
		return nil, ctx.Err()
	case <-m.done:
		m.removePending(id)
		return nil, m.CloseErr()
	}
}

// removePending detaches a call. If a response was already queued by the read
// loop its result/error is returned so the caller does not lose it.
func (m *Mux) removePending(id int64) (json.RawMessage, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	call, ok := m.pending[id]
	if !ok {
		return nil, nil
	}
	delete(m.pending, id)
	select {
	case resp := <-call.resultCh:
		return resp.Result, resp.Error
	default:
	}
	return nil, nil
}

func (m *Mux) readLoop() {
	defer m.closeWG.Done()
	for {
		frame, err := m.transport.Recv()
		if err != nil {
			m.shutdown(fmt.Errorf("cdpmux: transport recv failed: %w", err))
			return
		}
		var msg Message
		if err := json.Unmarshal(frame, &msg); err != nil {
			m.logger("cdpmux: dropping malformed frame: %v", err)
			continue
		}
		m.dispatch(msg)
	}
}

// dispatch runs on the single read-loop goroutine; locking only guards state
// shared with caller goroutines.
func (m *Mux) dispatch(msg Message) {
	if msg.ID != 0 {
		m.resolveCall(msg)
		return
	}
	if msg.Method == "Target.attachedToTarget" {
		m.handleAttached(msg)
	} else if msg.Method == "Target.detachedFromTarget" {
		m.handleDetached(msg)
	}

	if msg.SessionID != "" {
		m.routeSessionEvent(msg)
		return
	}

	select {
	case m.browserEvents <- eventOf(msg):
	default:
		m.logger("cdpmux: dropping browser event %s: subscriber too slow", msg.Method)
	}
}

func (m *Mux) resolveCall(msg Message) {
	m.mu.Lock()
	call, ok := m.pending[msg.ID]
	if ok {
		delete(m.pending, msg.ID)
	}
	m.mu.Unlock()
	if !ok {
		m.logger("cdpmux: dropping response for unknown command id=%d", msg.ID)
		return
	}
	// Buffered channel, never blocks; a canceled caller drains it on removal.
	call.resultCh <- msg
}

func (m *Mux) handleAttached(msg Message) {
	var info struct {
		SessionID string `json:"sessionId"`
	}
	if err := json.Unmarshal(msg.Params, &info); err != nil || info.SessionID == "" {
		m.logger("cdpmux: malformed Target.attachedToTarget event: %v", err)
		return
	}
	m.getOrCreateSession(info.SessionID)
}

func (m *Mux) handleDetached(msg Message) {
	var info struct {
		SessionID string `json:"sessionId"`
	}
	if err := json.Unmarshal(msg.Params, &info); err != nil || info.SessionID == "" {
		m.logger("cdpmux: malformed Target.detachedFromTarget event: %v", err)
		return
	}
	m.removeSession(info.SessionID, ErrSessionDetached)
}

// shutdown closes the mux exactly once, failing every pending call with the
// same ErrConnClosed-wrapped error and detaching every session.
func (m *Mux) shutdown(cause error) {
	m.closeOnce.Do(func() {
		m.mu.Lock()
		if errors.Is(cause, ErrConnClosed) {
			m.closeErr = cause
		} else {
			m.closeErr = &connClosedError{cause: cause}
		}

		for id := range m.pending {
			delete(m.pending, id)
		}

		sessions := make([]*Session, 0, len(m.sessions))
		for id, sess := range m.sessions {
			sessions = append(sessions, sess)
			delete(m.sessions, id)
		}
		m.mu.Unlock()

		// Waiters wake on m.done and all receive the identical closeErr.
		close(m.done)
		for _, sess := range sessions {
			sess.shutdown(m.closeErr)
		}
		_ = m.transport.Close()
	})
}

// Close terminates the connection and waits for the read loop to exit. It is
// safe to call concurrently and multiple times.
func (m *Mux) Close() error {
	m.shutdown(ErrConnClosed)
	m.closeWG.Wait()
	return nil
}

// CloseErr returns the connection error after the mux has closed.
func (m *Mux) CloseErr() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.closeErr
}

// Done is closed when the underlying connection has terminated.
func (m *Mux) Done() <-chan struct{} {
	return m.done
}

// BrowserEvents delivers session-less events (browser-level notifications and
// the Target lifecycle events). The channel is never closed; after Done fires
// no further events are delivered.
func (m *Mux) BrowserEvents() <-chan Event {
	return m.browserEvents
}

// Session returns the session attached to sessionID, if known.
func (m *Mux) Session(sessionID string) (*Session, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	sess, ok := m.sessions[sessionID]
	return sess, ok
}

// Sessions returns a snapshot of all currently attached sessions.
func (m *Mux) Sessions() []*Session {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]*Session, 0, len(m.sessions))
	for _, sess := range m.sessions {
		out = append(out, sess)
	}
	return out
}

func (m *Mux) isClosedLocked() bool {
	select {
	case <-m.done:
		return true
	default:
		return false
	}
}

func marshalParams(params any) (json.RawMessage, error) {
	if params == nil {
		return nil, nil
	}
	raw, err := json.Marshal(params)
	if err != nil {
		return nil, err
	}
	if string(raw) == "null" {
		return nil, nil
	}
	return raw, nil
}

func eventOf(msg Message) Event {
	return Event{
		Method:    msg.Method,
		Params:    msg.Params,
		SessionID: msg.SessionID,
	}
}
