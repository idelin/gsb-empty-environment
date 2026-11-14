// Package netsec provides a server-side share-source component for a Go
// meeting service. A share source is bound by the server before any capture
// starts: the meeting, the presenting subject, the window (or desktop) and a
// single active authorization token are pinned together. Capture is only
// accepted when the presented token and the claimed source match the bound
// scope exactly, so a client cannot widen a window share into a desktop
// share. Switching the source requires explicit re-confirmation, and desktop
// sharing always triggers an explicit prompt.
package netsec

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"
)

var (
	ErrInvalidSource    = errors.New("sharesource: invalid source descriptor")
	ErrSessionNotFound  = errors.New("sharesource: session not found")
	ErrTokenInvalid     = errors.New("sharesource: unknown capture token")
	ErrTokenRevoked     = errors.New("sharesource: capture token revoked")
	ErrScopeMismatch    = errors.New("sharesource: capture outside declared scope")
	ErrSwitchNotPending = errors.New("sharesource: no matching pending switch")
	ErrDesktopDeclined  = errors.New("sharesource: desktop share prompt declined")
	ErrIdentityMismatch = errors.New("sharesource: switch cannot change meeting or subject")
)

// SourceKind identifies the capture surface of a share source.
type SourceKind int

const (
	// SourceWindow scopes capture to a single window identifier.
	SourceWindow SourceKind = iota
	// SourceDesktop scopes capture to the entire desktop and always
	// requires an explicit prompt before it is bound.
	SourceDesktop
)

func (k SourceKind) String() string {
	switch k {
	case SourceWindow:
		return "window"
	case SourceDesktop:
		return "desktop"
	default:
		return "unknown"
	}
}

// Source describes what may be captured. It is bound server-side; clients
// never get to widen it after binding.
type Source struct {
	MeetingID string
	SubjectID string
	Kind      SourceKind
	WindowID  string
}

func (s Source) validate() error {
	if s.MeetingID == "" {
		return fmt.Errorf("%w: meeting id required", ErrInvalidSource)
	}
	if s.SubjectID == "" {
		return fmt.Errorf("%w: subject id required", ErrInvalidSource)
	}
	switch s.Kind {
	case SourceWindow:
		if s.WindowID == "" {
			return fmt.Errorf("%w: window id required for window share", ErrInvalidSource)
		}
	case SourceDesktop:
		if s.WindowID != "" {
			return fmt.Errorf("%w: desktop share must not carry a window id", ErrInvalidSource)
		}
	default:
		return fmt.Errorf("%w: unknown source kind", ErrInvalidSource)
	}
	return nil
}

// scopeKey is the canonical capture scope. Window sharing is scoped to one
// window; desktop sharing covers the whole desktop and is flagged as such.
func (s Source) scopeKey() string {
	if s.Kind == SourceDesktop {
		return "desktop:*"
	}
	return "window:" + s.WindowID
}

// Token authorizes capture for exactly one bound scope. Tokens are issued by
// the server, and at most one token is active per session: a confirmed
// switch revokes the previous token.
type Token struct {
	Value     string
	SessionID string
	Scope     string
	IssuedAt  time.Time
}

// Frame is a captured frame plus the source the capturer claims it came
// from. The claim is verified against the bound scope before the frame is
// recorded or delivered.
type Frame struct {
	Source Source
	Data   []byte
}

// RecordedFrame is a frame accepted into the recording, stamped with the
// declared scope and the participant list at capture time.
type RecordedFrame struct {
	Scope        string
	Participants []string
	Bytes        int
}

// Recording is the recording scope of a session. Scope only changes through
// a confirmed switch, and Participants is synced with the live participant
// list.
type Recording struct {
	MeetingID    string
	Scope        string
	Participants []string
	Frames       []RecordedFrame
}

// SwitchRequest is a pending, not yet confirmed source switch.
type SwitchRequest struct {
	ID        string
	SessionID string
	From      Source
	To        Source
	IssuedAt  time.Time
}

// DesktopPrompter obtains explicit user consent before desktop capture is
// bound. It is invoked on initial desktop binds and on switches to desktop.
type DesktopPrompter interface {
	ConfirmDesktopShare(meetingID, subjectID string) bool
}

// DesktopPrompterFunc adapts a function to DesktopPrompter.
type DesktopPrompterFunc func(meetingID, subjectID string) bool

// ConfirmDesktopShare implements DesktopPrompter.
func (f DesktopPrompterFunc) ConfirmDesktopShare(meetingID, subjectID string) bool {
	return f(meetingID, subjectID)
}

// Session is a bound share source with its active token, participant list
// and recording scope.
type Session struct {
	ID     string
	Source Source

	token        Token
	participants map[string]struct{}
	recording    Recording
	pending      *SwitchRequest
}

// Participants returns the synced participant list in stable order.
func (s *Session) Participants() []string {
	out := make([]string, 0, len(s.participants))
	for p := range s.participants {
		out = append(out, p)
	}
	sort.Strings(out)
	return out
}

// Recording returns a copy of the session's recording scope.
func (s *Session) Recording() Recording {
	rec := s.recording
	rec.Participants = append([]string(nil), rec.Participants...)
	rec.Frames = append([]RecordedFrame(nil), rec.Frames...)
	return rec
}

// PendingSwitch returns the pending switch request, if any.
func (s *Session) PendingSwitch() *SwitchRequest {
	if s.pending == nil {
		return nil
	}
	cp := *s.pending
	return &cp
}

// Manager binds share sources server-side and authorizes capture. It is
// safe for concurrent use.
type Manager struct {
	mu       sync.Mutex
	prompter DesktopPrompter
	sessions map[string]*Session
	tokens   map[string]*Session // active token value -> session
	revoked  map[string]struct{} // revoked token values
	now      func() time.Time
}

// NewManager creates a Manager. prompter may be nil, in which case desktop
// sharing is never permitted.
func NewManager(prompter DesktopPrompter) *Manager {
	return &Manager{
		prompter: prompter,
		sessions: make(map[string]*Session),
		tokens:   make(map[string]*Session),
		revoked:  make(map[string]struct{}),
		now:      time.Now,
	}
}

// Bind pins a meeting, subject and source together and issues the capture
// token. It must be called by the server before any capture starts. Desktop
// sources trigger an explicit prompt.
func (m *Manager) Bind(src Source) (*Session, Token, error) {
	if err := src.validate(); err != nil {
		return nil, Token{}, err
	}
	if src.Kind == SourceDesktop && !m.promptDesktop(src) {
		return nil, Token{}, ErrDesktopDeclined
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	sess := &Session{
		ID:           newID(),
		Source:       src,
		participants: make(map[string]struct{}),
		recording: Recording{
			MeetingID: src.MeetingID,
			Scope:     src.scopeKey(),
		},
	}
	tok := m.issueLocked(sess)
	m.sessions[sess.ID] = sess
	return sess, tok, nil
}

// RequestSwitch registers a pending switch to next. The switch does not
// take effect and no new token is issued until ConfirmSwitch. The meeting
// and subject of a session cannot be changed by a switch.
func (m *Manager) RequestSwitch(sessionID string, next Source) (*SwitchRequest, error) {
	if err := next.validate(); err != nil {
		return nil, err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	sess, ok := m.sessions[sessionID]
	if !ok {
		return nil, ErrSessionNotFound
	}
	if next.MeetingID != sess.Source.MeetingID || next.SubjectID != sess.Source.SubjectID {
		return nil, ErrIdentityMismatch
	}
	req := &SwitchRequest{
		ID:        newID(),
		SessionID: sessionID,
		From:      sess.Source,
		To:        next,
		IssuedAt:  m.now(),
	}
	sess.pending = req
	cp := *req
	return &cp, nil
}

// ConfirmSwitch applies a previously requested switch after explicit
// re-confirmation. Switching to a desktop source triggers the desktop
// prompt; if declined, the current source and token stay active. On success
// the old token is revoked, a new token is issued, and the recording scope
// is updated to the newly declared scope.
func (m *Manager) ConfirmSwitch(sessionID, requestID string) (*Session, Token, error) {
	m.mu.Lock()
	sess, ok := m.sessions[sessionID]
	if !ok {
		m.mu.Unlock()
		return nil, Token{}, ErrSessionNotFound
	}
	req := sess.pending
	if req == nil || req.ID != requestID {
		m.mu.Unlock()
		return nil, Token{}, ErrSwitchNotPending
	}
	next := req.To
	m.mu.Unlock()

	if next.Kind == SourceDesktop && !m.promptDesktop(next) {
		return nil, Token{}, ErrDesktopDeclined
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	// Re-check under lock: the pending request may have changed.
	if sess.pending == nil || sess.pending.ID != requestID {
		return nil, Token{}, ErrSwitchNotPending
	}
	m.revokeLocked(sess)
	sess.Source = next
	sess.recording.Scope = next.scopeKey()
	sess.pending = nil
	tok := m.issueLocked(sess)
	return sess, tok, nil
}

// CancelSwitch discards a pending switch; the current source stays active.
func (m *Manager) CancelSwitch(sessionID, requestID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	sess, ok := m.sessions[sessionID]
	if !ok {
		return ErrSessionNotFound
	}
	if sess.pending == nil || sess.pending.ID != requestID {
		return ErrSwitchNotPending
	}
	sess.pending = nil
	return nil
}

// SyncParticipants atomically replaces the participant list and the
// recording's participant scope, so recording and delivery always cover the
// same audience.
func (m *Manager) SyncParticipants(sessionID string, participants []string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	sess, ok := m.sessions[sessionID]
	if !ok {
		return ErrSessionNotFound
	}
	set := make(map[string]struct{}, len(participants))
	for _, p := range participants {
		if p == "" {
			continue
		}
		set[p] = struct{}{}
	}
	sess.participants = set
	sess.recording.Participants = sess.Participants()
	return nil
}

// Capture authorizes and records one frame. The token must be the session's
// active token, and the frame's claimed source must match the bound scope
// exactly; anything outside the declared scope is rejected and never
// recorded or delivered.
func (m *Manager) Capture(tokenValue string, frame Frame) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	sess, ok := m.tokens[tokenValue]
	if !ok {
		if _, wasRevoked := m.revoked[tokenValue]; wasRevoked {
			return ErrTokenRevoked
		}
		return ErrTokenInvalid
	}
	claimed := frame.Source
	if claimed.MeetingID != sess.Source.MeetingID ||
		claimed.SubjectID != sess.Source.SubjectID ||
		claimed.scopeKey() != sess.Source.scopeKey() {
		return ErrScopeMismatch
	}
	sess.recording.Frames = append(sess.recording.Frames, RecordedFrame{
		Scope:        sess.recording.Scope,
		Participants: sess.Participants(),
		Bytes:        len(frame.Data),
	})
	return nil
}

// Revoke ends the session and invalidates its token.
func (m *Manager) Revoke(sessionID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	sess, ok := m.sessions[sessionID]
	if !ok {
		return ErrSessionNotFound
	}
	m.revokeLocked(sess)
	delete(m.sessions, sessionID)
	return nil
}

func (m *Manager) revokeLocked(sess *Session) {
	delete(m.tokens, sess.token.Value)
	m.revoked[sess.token.Value] = struct{}{}
}

func (m *Manager) promptDesktop(src Source) bool {
	if m.prompter == nil {
		return false
	}
	return m.prompter.ConfirmDesktopShare(src.MeetingID, src.SubjectID)
}

func (m *Manager) issueLocked(sess *Session) Token {
	tok := Token{
		Value:     newID(),
		SessionID: sess.ID,
		Scope:     sess.Source.scopeKey(),
		IssuedAt:  m.now(),
	}
	sess.token = tok
	m.tokens[tok.Value] = sess
	return tok
}

func newID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b[:])
}
