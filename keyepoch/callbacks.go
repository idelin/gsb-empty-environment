package keyepoch

import "sync"

// AuthResult is the outcome of authenticating an inbound frame under the key
// generation it claims.
type AuthResult uint8

const (
	// AuthUnknown means the authenticator cannot tell yet (for example the
	// hardware token was busy). The frame must be held and re-checked by its
	// message identity later; it must never be delivered or acted on now.
	AuthUnknown AuthResult = iota
	// AuthRejected means the frame is explicitly invalid under the old key.
	AuthRejected
	// AuthAccepted means the frame authenticated under the named key epoch.
	AuthAccepted
)

// String renders the canonical name of an authentication result.
func (r AuthResult) String() string {
	switch r {
	case AuthUnknown:
		return "unknown"
	case AuthRejected:
		return "rejected"
	case AuthAccepted:
		return "accepted"
	default:
		return "illegal"
	}
}

// WriteResult is the outcome of attempting to put a frame on the wire.
type WriteResult uint8

const (
	// WriteUnknown means the transport lost the answer: the frame may or may
	// not have reached the peer. The engine must freeze the queue and verify
	// by message identity; it must never emit a second copy of an update.
	WriteUnknown WriteResult = iota
	// WriteNotCommitted means the frame demonstrably did not leave. It is
	// safe to retry the same frame (same MsgID, epoch and sequence).
	WriteNotCommitted
	// WriteCommitted means the frame is durably on the wire exactly once.
	WriteCommitted
)

// String renders the canonical name of a write result.
func (r WriteResult) String() string {
	switch r {
	case WriteUnknown:
		return "unknown"
	case WriteNotCommitted:
		return "not-committed"
	case WriteCommitted:
		return "committed"
	default:
		return "illegal"
	}
}

// Authenticator verifies an inbound frame under the read secret's key for
// frame.Epoch. Implementations must be side-effect free with respect to the
// engine; the engine alone owns queue and epoch state.
type Authenticator interface {
	Authenticate(f Frame) AuthResult
}

// Writer attempts to put an outbound frame on the wire. It is invoked once per
// transmission attempt; retries reuse the same Frame value and hence the same
// MsgID.
type Writer interface {
	Write(f Frame) WriteResult
}

// CommittedLookup answers the "what actually happened?" question for a frame
// whose Write callback returned WriteUnknown. The answer is keyed by message
// identity, so ambiguity is resolved without ever producing a second update.
type CommittedLookup interface {
	Lookup(f Frame) WriteResult
}

// Wire is an in-memory stand-in for the network: frames that are known to be
// committed are appended here and can be delivered to the peer engine. It is
// safe for concurrent use because test harnesses may inspect it while the
// engine itself is single-goroutine and event driven.
type Wire struct {
	mu     sync.Mutex
	frames []Frame
	index  map[string]int
}

// NewWire creates an empty in-memory wire.
func NewWire() *Wire {
	return &Wire{index: map[string]int{}}
}

// Commit records a frame as durably sent. It is idempotent per MsgID: the
// first commit wins, so repeated Lookup resolution can never double-send.
func (w *Wire) Commit(f Frame) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if _, ok := w.index[f.MsgID]; ok {
		return
	}
	w.index[f.MsgID] = len(w.frames)
	w.frames = append(w.frames, f)
}

// Frames returns a copy of every frame committed so far, in commit order.
func (w *Wire) Frames() []Frame {
	w.mu.Lock()
	defer w.mu.Unlock()
	out := make([]Frame, len(w.frames))
	copy(out, w.frames)
	return out
}

// Count reports how many distinct frames have been committed.
func (w *Wire) Count() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return len(w.frames)
}

// Scripts is the deterministic callback script used by the default adapters.
// Each map is keyed by frame MsgID. A missing entry yields the default value
// (AuthUnknown for authentication, WriteUnknown for write and lookup), which
// keeps ambiguous outcomes explicit rather than silently optimistic.
type Scripts struct {
	Auth   map[string]AuthResult
	Write  map[string]WriteResult
	Lookup map[string]WriteResult
	// Default* apply when no per-MsgID entry exists. Zero values preserve the
	// conservative behavior of reporting an unknown outcome.
	DefaultAuth   AuthResult
	DefaultWrite  WriteResult
	DefaultLookup WriteResult
}

// NewScripts creates an empty script set.
func NewScripts() *Scripts {
	return &Scripts{
		Auth:   map[string]AuthResult{},
		Write:  map[string]WriteResult{},
		Lookup: map[string]WriteResult{},
	}
}

// Clone returns an independent copy of the script set, used when enumeration
// branches so sibling traces cannot observe each other's script mutations.
func (s *Scripts) Clone() *Scripts {
	clone := NewScripts()
	for k, v := range s.Auth {
		clone.Auth[k] = v
	}
	for k, v := range s.Write {
		clone.Write[k] = v
	}
	for k, v := range s.Lookup {
		clone.Lookup[k] = v
	}
	clone.DefaultAuth = s.DefaultAuth
	clone.DefaultWrite = s.DefaultWrite
	clone.DefaultLookup = s.DefaultLookup
	return clone
}

// ScriptedAuthenticator authenticates with HMAC verification first (bad tags
// are always rejected) and then applies the per-message script.
type ScriptedAuthenticator struct {
	ReadSecret []byte
	Scripts    *Scripts
}

// Authenticate implements Authenticator.
func (a *ScriptedAuthenticator) Authenticate(f Frame) AuthResult {
	if !VerifyFrame(f, a.ReadSecret) {
		return AuthRejected
	}
	if r, ok := a.Scripts.Auth[f.MsgID]; ok {
		return r
	}
	return a.Scripts.DefaultAuth
}

// ScriptedWriter consults the write script and only commits known-successful
// frames to the shared wire, so the wire never contains speculative frames.
type ScriptedWriter struct {
	Wire    *Wire
	Scripts *Scripts
}

// Write implements Writer.
func (w *ScriptedWriter) Write(f Frame) WriteResult {
	r := w.Scripts.DefaultWrite
	if v, ok := w.Scripts.Write[f.MsgID]; ok {
		r = v
	}
	if r == WriteCommitted {
		w.Wire.Commit(f)
	}
	return r
}

// ScriptedLookup resolves ambiguous writes by MsgID and, when the answer turns
// out to be committed, retroactively records the single frame on the wire.
type ScriptedLookup struct {
	Wire    *Wire
	Scripts *Scripts
}

// Lookup implements CommittedLookup.
func (l *ScriptedLookup) Lookup(f Frame) WriteResult {
	r := l.Scripts.DefaultLookup
	if v, ok := l.Scripts.Lookup[f.MsgID]; ok {
		r = v
	}
	if r == WriteCommitted {
		l.Wire.Commit(f)
	}
	return r
}
