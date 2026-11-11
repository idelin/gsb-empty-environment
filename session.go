package cdpmux

import (
	"context"
	"encoding/json"
	"sync"
)

// Session is one target-scoped CDP session. Multiple sessions share the single
// underlying Transport owned by the Mux.
type Session struct {
	mux *Mux
	id  string

	events chan Event
	done   chan struct{}

	mu      sync.Mutex
	err     error
	dropped int
}

// ID returns the CDP session id.
func (s *Session) ID() string {
	return s.id
}

// Events returns session-scoped notifications. The channel is buffered and
// never closed: after Done fires, events already queued stay readable but no
// new events arrive.
func (s *Session) Events() <-chan Event {
	return s.events
}

// Done is closed when the session is detached or the connection terminates.
func (s *Session) Done() <-chan struct{} {
	return s.done
}

// Dropped reports how many events were discarded because this subscriber could
// not keep up. Dropping never affects any other session.
func (s *Session) Dropped() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.dropped
}

// Err returns the reason the session ended (ErrSessionDetached or the
// connection error), or nil while it is active.
func (s *Session) Err() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.err
}

// Send issues a command on this session; the outgoing frame carries the
// sessionId so the browser routes it to the right target.
func (s *Session) Send(ctx context.Context, method string, params any) (json.RawMessage, error) {
	s.mu.Lock()
	err := s.err
	s.mu.Unlock()
	if err != nil {
		return nil, err
	}
	return s.mux.send(ctx, s.id, method, params)
}

// Detach explicitly detaches the target via Target.detachFromTarget. The
// session ends when the browser confirms with Target.detachedFromTarget.
func (s *Session) Detach(ctx context.Context) (json.RawMessage, error) {
	return s.Send(ctx, "Target.detachFromTarget", map[string]string{
		"sessionId": s.id,
	})
}

// deliver is called only from the mux read-loop goroutine. It must never block
// on a slow subscriber, so an undrainable event is counted and dropped.
func (s *Session) deliver(ev Event) {
	s.mu.Lock()
	if s.err != nil {
		s.dropped++
		s.mu.Unlock()
		s.mux.logger("cdpmux: dropping event %s for ended session %s", ev.Method, s.id)
		return
	}
	s.mu.Unlock()

	select {
	case s.events <- ev:
	default:
		s.mu.Lock()
		s.dropped++
		s.mu.Unlock()
		s.mux.logger("cdpmux: dropping event %s for session %s: subscriber too slow",
			ev.Method, s.id)
	}
}

func (s *Session) shutdown(err error) {
	s.mu.Lock()
	if s.err != nil {
		s.mu.Unlock()
		return
	}
	s.err = err
	s.mu.Unlock()
	close(s.done)
}

// Attach sends Target.attachToTarget and registers the resulting session.
// Sessions created by flflatten-mode Target.attachedToTarget events are
// registered automatically, even before any command response arrives.
func (m *Mux) Attach(ctx context.Context, targetID string, flatten bool) (*Session, error) {
	params := map[string]any{
		"targetId": targetID,
		"flatten":  flatten,
	}
	result, err := m.Send(ctx, "Target.attachToTarget", params)
	if err != nil {
		return nil, err
	}
	var info struct {
		SessionID string `json:"sessionId"`
	}
	if err := json.Unmarshal(result, &info); err != nil {
		return nil, err
	}
	if info.SessionID == "" {
		return nil, &ResponseError{Code: -1, Message: "Target.attachToTarget returned empty sessionId"}
	}
	return m.getOrCreateSession(info.SessionID), nil
}

// Detach detaches a session by id and removes it from the registry immediately.
// The browser's detachedFromTarget notification, if it arrives later, becomes a
// no-op.
func (m *Mux) Detach(ctx context.Context, sessionID string) error {
	_, err := m.Send(ctx, "Target.detachFromTarget", map[string]string{
		"sessionId": sessionID,
	})
	if err != nil {
		return err
	}
	m.removeSession(sessionID, ErrSessionDetached)
	return nil
}

func (m *Mux) getOrCreateSession(sessionID string) *Session {
	m.mu.Lock()
	defer m.mu.Unlock()
	if sess, ok := m.sessions[sessionID]; ok {
		return sess
	}
	sess := &Session{
		mux:    m,
		id:     sessionID,
		events: make(chan Event, m.eventBuf),
		done:   make(chan struct{}),
	}
	m.sessions[sessionID] = sess
	return sess
}

func (m *Mux) removeSession(sessionID string, cause error) {
	m.mu.Lock()
	sess, ok := m.sessions[sessionID]
	if ok {
		delete(m.sessions, sessionID)
	}
	m.mu.Unlock()
	if ok {
		sess.shutdown(cause)
	}
}

// routeSessionEvent dispatches a session-scoped notification. Events for
// unknown or detached sessions are dropped rather than blocking the reader.
func (m *Mux) routeSessionEvent(msg Message) {
	m.mu.Lock()
	sess, ok := m.sessions[msg.SessionID]
	m.mu.Unlock()
	if !ok {
		m.logger("cdpmux: dropping event %s for unknown session %s",
			msg.Method, msg.SessionID)
		return
	}
	sess.deliver(eventOf(msg))
}
