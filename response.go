package netsec

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sync"
)

// RequestID identifies one downstream request instance. It is carried on every
// emitted stage so a shared pipeline can keep request records separate.
type RequestID string

// Valid reports whether the request identifier is non-empty.
func (id RequestID) Valid() bool {
	return id != ""
}

func (id RequestID) String() string {
	return string(id)
}

// StageKind identifies the kind of response stage.
type StageKind uint8

const (
	// StageHint is an informational 103 response.
	StageHint StageKind = iota + 1
	// StageFinal is the final non-informational response.
	StageFinal
)

func (k StageKind) String() string {
	switch k {
	case StageHint:
		return "hint"
	case StageFinal:
		return "final"
	default:
		return fmt.Sprintf("stage(%d)", k)
	}
}

// Stage is one response event emitted for a request. Header is an immutable
// snapshot owned by the receiver; callers must not use it to mutate the
// MultiStageResponse's state. Sequence is 1-based and increases per request.
type Stage struct {
	RequestID RequestID
	Kind      StageKind
	Status    int
	Header    http.Header
	Sequence  uint64
}

// Emitter delivers a stage to the response pipeline. Emit is called while the
// originating MultiStageResponse is locked, so stages for one request are
// delivered in order and a final stage is never followed by a hint. An
// Emitter shared by multiple requests must be safe for concurrent use, and an
// Emit implementation must not call back into the response that emitted the
// stage.
type Emitter interface {
	Emit(Stage) error
}

// EmitterFunc adapts a function to Emitter.
type EmitterFunc func(Stage) error

// Emit calls f(stage).
func (f EmitterFunc) Emit(stage Stage) error {
	return f(stage)
}

// ResponseState is the lifecycle state of a MultiStageResponse.
type ResponseState uint8

const (
	// StateOpen accepts hints and one final response.
	StateOpen ResponseState = iota
	// StateFinalized has committed its final response.
	StateFinalized
	// StateCanceled has been canceled by the request context or Cancel.
	StateCanceled
)

func (s ResponseState) String() string {
	switch s {
	case StateOpen:
		return "open"
	case StateFinalized:
		return "finalized"
	case StateCanceled:
		return "canceled"
	default:
		return fmt.Sprintf("state(%d)", s)
	}
}

var (
	// ErrFinalized is returned when a final response is attempted after the
	// handle is already terminal.
	ErrFinalized = errors.New("netsec: final response already committed")
	// ErrLateHint is returned for a hint that arrives after the final
	// response. It also matches ErrFinalized.
	ErrLateHint = fmt.Errorf("%w: hint arrived too late", ErrFinalized)
	// ErrCanceled is returned when the request context is canceled or Cancel
	// is called before the operation can be performed.
	ErrCanceled = errors.New("netsec: response canceled")
	// ErrInvalidRequestID is returned for an empty RequestID.
	ErrInvalidRequestID = errors.New("netsec: invalid request id")
	// ErrNilEmitter is returned when no Emitter was supplied.
	ErrNilEmitter = errors.New("netsec: nil emitter")
	// ErrInvalidStatus is returned for a status that cannot be a final HTTP
	// response or for a non-103 hint status.
	ErrInvalidStatus = errors.New("netsec: invalid response status")
	// ErrNoHintFields is returned when a hint has no whitelisted fields left.
	ErrNoHintFields = errors.New("netsec: no allowed early-hint fields")
	// ErrInvalidHeader is returned for an invalid field name or value.
	ErrInvalidHeader = errors.New("netsec: invalid header")
)

// Option configures a MultiStageResponse.
type Option func(*responseConfig) error

type responseConfig struct {
	policy *HintPolicy
}

// WithHintPolicy replaces the default hint whitelist.
func WithHintPolicy(policy *HintPolicy) Option {
	return func(cfg *responseConfig) error {
		if policy == nil {
			return errors.New("netsec: nil hint policy")
		}
		cfg.policy = policy
		return nil
	}
}

// MultiStageResponse is a request-scoped response handle. It accepts zero or
// more early hints followed by exactly one final response or a cancellation.
// It is safe for concurrent use.
type MultiStageResponse struct {
	id      RequestID
	emitter Emitter
	policy  *HintPolicy
	ctx     context.Context

	mu          sync.Mutex
	state       ResponseState
	sequence    uint64
	stopContext func() bool
	done        chan struct{}
	doneOnce    sync.Once
}

// NewMultiStageResponse creates a response handle bound to one request
// instance. The context controls cancellation; a nil context is treated as
// context.Background.
func NewMultiStageResponse(ctx context.Context, id RequestID, emitter Emitter, opts ...Option) (*MultiStageResponse, error) {
	cfg, err := applyOptions(opts)
	if err != nil {
		return nil, err
	}
	return newMultiStageResponse(ctx, id, emitter, cfg)
}

func newMultiStageResponse(ctx context.Context, id RequestID, emitter Emitter, cfg responseConfig) (*MultiStageResponse, error) {
	if !id.Valid() {
		return nil, ErrInvalidRequestID
	}
	if isNil(emitter) {
		return nil, ErrNilEmitter
	}
	if ctx == nil {
		ctx = context.Background()
	}

	resp := &MultiStageResponse{
		id:      id,
		emitter: emitter,
		policy:  cfg.policy,
		ctx:     ctx,
		done:    make(chan struct{}),
	}
	if ctx.Done() != nil {
		resp.mu.Lock()
		resp.stopContext = context.AfterFunc(ctx, resp.Cancel)
		resp.mu.Unlock()
	}
	return resp, nil
}

func applyOptions(opts []Option) (responseConfig, error) {
	cfg := responseConfig{policy: DefaultHintPolicy()}
	for _, opt := range opts {
		if opt == nil {
			continue
		}
		if err := opt(&cfg); err != nil {
			return responseConfig{}, err
		}
	}
	return cfg, nil
}

// New is an alias for NewMultiStageResponse.
func New(ctx context.Context, id RequestID, emitter Emitter, opts ...Option) (*MultiStageResponse, error) {
	return NewMultiStageResponse(ctx, id, emitter, opts...)
}

// RequestID returns the request instance this handle is bound to.
func (r *MultiStageResponse) RequestID() RequestID {
	return r.id
}

// Hint filters h through the hint whitelist and emits a 103 stage. Fields
// outside the whitelist, including all authentication fields, are dropped. If
// no whitelisted fields remain, no stage is emitted and ErrNoHintFields is
// returned. A hint after the final response returns ErrLateHint.
func (r *MultiStageResponse) Hint(h http.Header) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	switch r.state {
	case StateFinalized:
		return ErrLateHint
	case StateCanceled:
		return ErrCanceled
	}
	if r.ctx.Err() != nil {
		r.cancelLocked()
		return ErrCanceled
	}

	header, _ := r.policy.Filter(h)
	if len(header) == 0 {
		return ErrNoHintFields
	}

	r.sequence++
	stage := Stage{
		RequestID: r.id,
		Kind:      StageHint,
		Status:    StatusEarlyHints,
		Header:    header,
		Sequence:  r.sequence,
	}
	if err := r.emitter.Emit(stage); err != nil {
		return fmt.Errorf("netsec: emit early hint: %w", err)
	}
	return nil
}

// WriteHint is an alias for Hint.
func (r *MultiStageResponse) WriteHint(h http.Header) error {
	return r.Hint(h)
}

// Final emits the one final response and closes the handle. Header is copied
// verbatim; hint headers are never merged into it. Once accepted, the final
// response is terminal even if the emitter reports an error.
func (r *MultiStageResponse) Final(status int, h http.Header) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	switch r.state {
	case StateFinalized:
		return ErrFinalized
	case StateCanceled:
		return ErrCanceled
	}
	if r.ctx.Err() != nil {
		r.cancelLocked()
		return ErrCanceled
	}
	if status < 200 || status > 599 {
		return ErrInvalidStatus
	}

	header, err := cloneHeader(h)
	if err != nil {
		return err
	}

	r.sequence++
	r.finalizeLocked()
	stage := Stage{
		RequestID: r.id,
		Kind:      StageFinal,
		Status:    status,
		Header:    header,
		Sequence:  r.sequence,
	}
	if err := r.emitter.Emit(stage); err != nil {
		return fmt.Errorf("netsec: emit final response: %w", err)
	}
	return nil
}

// WriteFinal is an alias for Final.
func (r *MultiStageResponse) WriteFinal(status int, h http.Header) error {
	return r.Final(status, h)
}

// Cancel closes the handle and rejects all later hints and finals. It is
// idempotent and does nothing after a final response. Canceling one handle
// never affects another handle.
func (r *MultiStageResponse) Cancel() {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.state == StateOpen {
		r.cancelLocked()
	}
}

// State returns the current lifecycle state.
func (r *MultiStageResponse) State() ResponseState {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.state
}

// Done is closed when the handle is finalized or canceled.
func (r *MultiStageResponse) Done() <-chan struct{} {
	return r.done
}

func (r *MultiStageResponse) finalizeLocked() {
	r.state = StateFinalized
	r.closeDoneLocked()
}

func (r *MultiStageResponse) cancelLocked() {
	r.state = StateCanceled
	r.closeDoneLocked()
}

func (r *MultiStageResponse) closeDoneLocked() {
	r.doneOnce.Do(func() { close(r.done) })
	if r.stopContext != nil {
		r.stopContext()
		r.stopContext = nil
	}
}
