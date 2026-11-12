package netsec

import (
	"errors"
	"net/http"
	"sync"
)

// Frame delivery errors. Callers can use errors.Is to match them.
var (
	// ErrUnknownRequest is returned when a frame references a request
	// instance that has no bound stream (never bound, finalized or canceled).
	ErrUnknownRequest = errors.New("netsec: no response stream bound to request instance")
	// ErrAlreadyBound is returned when Bind is called twice for the same
	// request instance.
	ErrAlreadyBound = errors.New("netsec: request instance already has a bound stream")
	// ErrLateHint is returned when an early hint arrives after the final
	// response has been delivered. The hint is never written.
	ErrLateHint = errors.New("netsec: early hint rejected: final response already sent")
	// ErrFinalized is returned when the final response is delivered twice.
	ErrFinalized = errors.New("netsec: final response already sent")
	// ErrStreamClosed is returned when the stream has been canceled, for
	// example because the client request context ended.
	ErrStreamClosed = errors.New("netsec: response stream closed")
	// ErrInvalidStatus is returned when Final is called with a non-final
	// status code.
	ErrInvalidStatus = errors.New("netsec: final status must be a 2xx-5xx status code")
)

// authSensitiveHeader keys are credential-bearing hop fields. They are
// stripped from early hints even if an operator accidentally whitelists
// them, and hint-supplied instances must never reach the final header map.
var authSensitiveHeader = map[string]struct{}{
	"Authorization":       {},
	"Proxy-Authorization": {},
	"Cookie":              {},
	"Set-Cookie":          {},
	"Www-Authenticate":    {},
	"Proxy-Authenticate":  {},
}

// defaultHintWhitelist follows RFC 8297: 103 Early Hints only carry Link.
var defaultHintWhitelist = map[string]struct{}{
	"Link": {},
}

// Sink is the per-request receiver of serialized response frames. Production
// code uses an http.ResponseWriter-backed sink; tests use a recording sink to
// assert the exact frame sequence a receiver observes.
type Sink interface {
	// Hint delivers one sanitized 103 Early Hints frame.
	Hint(h http.Header) error
	// Final delivers the terminal response. After a successful call the
	// stream is unbound and every later hint is rejected.
	Final(status int, h http.Header, body []byte) error
}

// Option customizes a Pipeline.
type Option func(*Pipeline)

// WithHintWhitelist replaces the default early-hint whitelist. Key matching is
// case-insensitive. Authentication-related fields remain blocked regardless
// of the supplied list.
func WithHintWhitelist(keys ...string) Option {
	return func(p *Pipeline) {
		whitelist := make(map[string]struct{}, len(keys))
		for _, key := range keys {
			whitelist[http.CanonicalHeaderKey(key)] = struct{}{}
		}
		p.whitelist = whitelist
	}
}

// Pipeline routes multi-stage response frames (103 early hints followed by one
// final response) to the stream bound to each request instance. A frame can
// only reach the sink of its own request, so hints from one tenant can never
// leak into another tenant's response.
type Pipeline struct {
	mu        sync.Mutex
	streams   map[*http.Request]*Stream
	whitelist map[string]struct{}
}

// NewPipeline creates a pipeline with the RFC 8297 default whitelist (Link).
func NewPipeline(opts ...Option) *Pipeline {
	p := &Pipeline{
		streams:   make(map[*http.Request]*Stream),
		whitelist: cloneWhitelist(defaultHintWhitelist),
	}
	for _, opt := range opts {
		opt(p)
	}
	return p
}

// Bind attaches a new stream to a concrete request instance and its sink.
// Each request instance may be bound exactly once.
func (p *Pipeline) Bind(req *http.Request, sink Sink) (*Stream, error) {
	if req == nil {
		return nil, ErrUnknownRequest
	}
	if sink == nil {
		return nil, errors.New("netsec: sink must not be nil")
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if _, exists := p.streams[req]; exists {
		return nil, ErrAlreadyBound
	}
	stream := &Stream{
		pipeline: p,
		request:  req,
		sink:     sink,
	}
	p.streams[req] = stream
	return stream, nil
}

// Len reports how many active (not finalized, not canceled) streams exist.
func (p *Pipeline) Len() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.streams)
}

func (p *Pipeline) lookup(req *http.Request) (*Stream, error) {
	p.mu.Lock()
	stream, ok := p.streams[req]
	p.mu.Unlock()
	if !ok {
		return nil, ErrUnknownRequest
	}
	return stream, nil
}

func (p *Pipeline) unbind(req *http.Request) {
	p.mu.Lock()
	delete(p.streams, req)
	p.mu.Unlock()
}

// Hint routes an early hint to the stream bound to req. Hint headers are
// reduced to the whitelist; authentication fields are always dropped.
func (p *Pipeline) Hint(req *http.Request, h http.Header) error {
	stream, err := p.lookup(req)
	if err != nil {
		return err
	}
	return stream.Hint(h)
}

// Final routes the terminal response to the stream bound to req. The hint
// header path is fully separate, so no hint field is ever merged into h.
func (p *Pipeline) Final(req *http.Request, status int, h http.Header, body []byte) error {
	stream, err := p.lookup(req)
	if err != nil {
		return err
	}
	return stream.Final(status, h, body)
}

// Cancel closes the stream bound to req, if any. Later frames fail with
// ErrStreamClosed. Canceling an unknown or finished request is a no-op.
func (p *Pipeline) Cancel(req *http.Request) {
	stream, err := p.lookup(req)
	if err != nil {
		return
	}
	stream.Cancel()
}

// Stream owns the multi-stage response lifecycle for one request instance.
type Stream struct {
	pipeline *Pipeline
	request  *http.Request
	sink     Sink

	mu      sync.Mutex
	closed  bool
	done    bool
	hintCnt int
}

// Request returns the request instance this stream is bound to.
func (s *Stream) Request() *http.Request {
	return s.request
}

// HintsSent reports how many whitelisted hint frames have been delivered.
func (s *Stream) HintsSent() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.hintCnt
}

// Hint delivers a 103 frame. Only whitelisted fields are forwarded and
// authentication fields are always removed. Frames arriving after the final
// response fail with ErrLateHint; frames on a canceled stream fail with
// ErrStreamClosed. The input header is never modified.
func (s *Stream) Hint(h http.Header) error {
	allowed := sanitizeHintHeaders(h, s.pipeline.whitelist)
	if len(allowed) == 0 {
		return nil
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	switch {
	case s.closed:
		return ErrStreamClosed
	case s.done:
		return ErrLateHint
	}
	if err := s.sink.Hint(allowed); err != nil {
		return err
	}
	s.hintCnt++
	return nil
}

// Final delivers the terminal response exactly once. The outgoing header is
// built solely from h (cloned); headers submitted through Hint live in
// separate maps and can never be merged in. Late calls fail with
// ErrFinalized, calls on a canceled stream with ErrStreamClosed.
func (s *Stream) Final(status int, h http.Header, body []byte) error {
	if status < 200 || status > 599 {
		return ErrInvalidStatus
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	switch {
	case s.closed:
		return ErrStreamClosed
	case s.done:
		return ErrFinalized
	}

	out := h.Clone()
	if out == nil {
		out = http.Header{}
	}
	s.done = true
	s.pipeline.unbind(s.request)
	return s.sink.Final(status, out, body)
}

// Cancel marks the stream closed and unbinds it. It is safe to call multiple
// times and is wired to the request context by the HTTP adapter.
func (s *Stream) Cancel() {
	s.mu.Lock()
	if s.closed || s.done {
		s.mu.Unlock()
		return
	}
	s.closed = true
	s.mu.Unlock()
	s.pipeline.unbind(s.request)
}

func (s *Stream) finished() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.done || s.closed
}

// sanitizeHintHeaders copies only whitelisted, non-authentication fields into
// a fresh header map. The returned map never aliases the input.
func sanitizeHintHeaders(h http.Header, whitelist map[string]struct{}) http.Header {
	out := http.Header{}
	for key, values := range h {
		canonical := http.CanonicalHeaderKey(key)
		if _, sensitive := authSensitiveHeader[canonical]; sensitive {
			continue
		}
		if _, allowed := whitelist[canonical]; !allowed {
			continue
		}
		out[canonical] = append(out[canonical], values...)
	}
	return out
}

func cloneWhitelist(in map[string]struct{}) map[string]struct{} {
	out := make(map[string]struct{}, len(in))
	for key := range in {
		out[key] = struct{}{}
	}
	return out
}
