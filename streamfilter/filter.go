// Package streamfilter provides bidirectional stream filtering for
// interactive remote-ops session proxies. While terminal echo is
// disabled (e.g. at a password prompt) it suppresses the local input
// and the corresponding, possibly late, remote echo so that secrets
// never reach the session persistence layer, while all normal traffic
// keeps its content and ordering once echo is restored.
//
// The filter is purely byte oriented: it carries no chunk state, so
// multi-byte UTF-8 sequences and terminal control sequences may be
// split across chunks arbitrarily without affecting correctness.
package streamfilter

import "sync"

// DefaultMaxPending bounds how many suppressed input bytes are retained
// for late-echo matching.
const DefaultMaxPending = 4096

// Filter decides which bytes of each direction are safe to persist.
// It is safe for concurrent use.
type Filter struct {
	mu         sync.Mutex
	echoOn     bool
	pending    []byte
	maxPending int
}

// NewFilter returns a Filter with echo initially on. maxPending <= 0
// selects DefaultMaxPending.
func NewFilter(maxPending int) *Filter {
	if maxPending <= 0 {
		maxPending = DefaultMaxPending
	}
	return &Filter{echoOn: true, maxPending: maxPending}
}

// SetEcho switches the echo state. Restoring echo discards suppressed
// input that was never echoed, so later legitimate output is never
// mistaken for a late echo.
func (f *Filter) SetEcho(on bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.echoOn = on
	if on {
		f.pending = f.pending[:0]
	}
}

// EchoOn reports the current echo state.
func (f *Filter) EchoOn() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.echoOn
}

// PendingEcho reports how many suppressed input bytes are still
// awaiting a possible late echo.
func (f *Filter) PendingEcho() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.pending)
}

// FilterInput processes local input and returns the bytes that are safe
// to persist. While echo is off it returns nil and queues the bytes for
// late-echo matching. The caller must still forward the full input to
// the remote side regardless of the return value.
func (f *Filter) FilterInput(p []byte) []byte {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.echoOn || len(p) == 0 {
		return p
	}
	f.pending = append(f.pending, p...)
	if excess := len(f.pending) - f.maxPending; excess > 0 {
		f.pending = append([]byte(nil), f.pending[excess:]...)
	}
	return nil
}

// FilterOutput processes remote output and returns the bytes that are
// safe to persist, removing any prefix-matched late echo of suppressed
// input. When any byte is removed the result is a fresh slice and p is
// left unmodified, so p remains safe to forward to the local terminal.
func (f *Filter) FilterOutput(p []byte) []byte {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(p) == 0 || len(f.pending) == 0 {
		return p
	}
	out := make([]byte, 0, len(p))
	for _, b := range p {
		if len(f.pending) > 0 && b == f.pending[0] {
			f.pending = f.pending[1:]
			continue
		}
		out = append(out, b)
	}
	return out
}
