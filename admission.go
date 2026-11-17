package netsec

import (
	"context"
	"fmt"
	"math"
	"sort"
	"sync"
	"time"
)

// ParentState is the lifecycle state of a registered parent request.
type ParentState uint8

const (
	ParentActive ParentState = iota + 1
	ParentCanceled
	ParentEnded
)

func (s ParentState) String() string {
	switch s {
	case ParentActive:
		return "active"
	case ParentCanceled:
		return "canceled"
	case ParentEnded:
		return "ended"
	default:
		return "unknown"
	}
}

// PushState is the lifecycle state of a promised push stream.
type PushState uint8

const (
	// PushPending means the promise has passed protocol and authorization
	// checks and is currently being allocated by the host.
	PushPending PushState = iota + 1
	PushActive
	PushCanceled
	PushClosed
)

func (s PushState) String() string {
	switch s {
	case PushPending:
		return "pending"
	case PushActive:
		return "active"
	case PushCanceled:
		return "canceled"
	case PushClosed:
		return "closed"
	default:
		return "unknown"
	}
}

// Allocator is a host hook invoked only after a promise has passed stream ID,
// parent, connection-generation, pseudo-header, and source authorization
// checks. It must honor ctx; returning an error prevents the business handler
// from being called.
type Allocator func(context.Context, *Push) error

// PushHandler is the business callback. It is invoked only for pushes that were
// admitted under an active parent context and whose allocation succeeded.
type PushHandler func(*Push)

// Option configures an Admission.
type Option func(*admissionConfig)

type admissionConfig struct {
	allocator     Allocator
	handler       PushHandler
	eventCapacity int
}

// WithAllocator installs the post-validation resource allocation hook.
func WithAllocator(allocator Allocator) Option {
	return func(config *admissionConfig) {
		config.allocator = allocator
	}
}

// WithPushHandler installs the business callback.
func WithPushHandler(handler PushHandler) Option {
	return func(config *admissionConfig) {
		config.handler = handler
	}
}

// WithEventCapacity sets the maximum number of retained lifecycle events.
// Zero disables event retention; counters remain available in Stats.
func WithEventCapacity(capacity int) Option {
	return func(config *admissionConfig) {
		config.eventCapacity = capacity
	}
}

// Admission is the per-connection-generation push admission state machine. A
// new connection generation must use a new Admission; this prevents stale
// parents and promised stream IDs from an old connection from being reused.
type Admission struct {
	generation uint64
	allocator  Allocator
	handler    PushHandler

	mu           sync.Mutex
	closed       bool
	parents      map[uint32]*Parent
	pushes       map[uint32]*Push
	seenParents  map[uint32]struct{}
	seenPushes   map[uint32]struct{}
	lastPromised uint32
	events       eventLog
	stats        Stats
}

// ParentSpec registers one client request whose authentication context may
// authorize pushes. AuthorizedSources is snapshotted at registration time.
type ParentSpec struct {
	StreamID             uint32
	ConnectionGeneration uint64
	AuthorizedSources    []Source
	Context              context.Context
}

// PromiseSpec describes one received PUSH_PROMISE on a parent stream.
// ConnectionGeneration is the generation claimed by the frame's connection;
// it must match the Admission exactly.
type PromiseSpec struct {
	ParentStreamID       uint32
	PromisedStreamID     uint32
	ConnectionGeneration uint64
	Headers              []HeaderField
}

// Parent is a registered parent request and its authorization snapshot.
type Parent struct {
	admission *Admission

	id            uint32
	generation    uint64
	ctx           context.Context
	cancel        context.CancelCauseFunc
	authorized    []Source
	authorizedSet map[Source]struct{}

	// Guarded by admission.mu.
	state  ParentState
	reason string
	pushes map[uint32]*Push
}

// Push is one admitted or in-flight promised stream.
type Push struct {
	admission *Admission
	parent    *Parent

	id         uint32
	generation uint64
	request    PromisedRequest
	ctx        context.Context
	cancel     context.CancelCauseFunc

	// Guarded by admission.mu.
	state  PushState
	reason string
}

type pendingCancellation struct {
	parent *Parent
	pushes []*Push
	cause  error
}

// NewAdmission creates admission state for one connection generation.
func NewAdmission(generation uint64, opts ...Option) *Admission {
	config := admissionConfig{eventCapacity: 1024}
	for _, opt := range opts {
		if opt != nil {
			opt(&config)
		}
	}
	return &Admission{
		generation:  generation,
		allocator:   config.allocator,
		handler:     config.handler,
		parents:     make(map[uint32]*Parent),
		pushes:      make(map[uint32]*Push),
		seenParents: make(map[uint32]struct{}),
		seenPushes:  make(map[uint32]struct{}),
		events:      newEventLog(config.eventCapacity),
	}
}

// Generation returns the connection generation bound to this admission.
func (a *Admission) Generation() uint64 { return a.generation }

// RegisterParent binds a parent request, its connection generation, and a
// snapshot of the sources authorized by its authentication context.
func (a *Admission) RegisterParent(spec ParentSpec) (*Parent, error) {
	if spec.ConnectionGeneration != a.generation {
		return nil, fmt.Errorf("%w: got %d, want %d", ErrGenerationMismatch, spec.ConnectionGeneration, a.generation)
	}
	if !validParentStreamID(spec.StreamID) {
		return nil, fmt.Errorf("%w: parent stream %d", ErrInvalidStreamID, spec.StreamID)
	}
	authorized, authorizedSet, err := canonicalSourceSet(spec.AuthorizedSources)
	if err != nil {
		return nil, err
	}

	hostCtx := spec.Context
	if hostCtx == nil {
		hostCtx = context.Background()
	}
	parentCtx, cancel := context.WithCancelCause(hostCtx)

	a.mu.Lock()
	if a.closed {
		a.mu.Unlock()
		cancel(ErrAdmissionClosed)
		return nil, ErrAdmissionClosed
	}
	if _, exists := a.seenParents[spec.StreamID]; exists {
		a.mu.Unlock()
		cancel(fmt.Errorf("%w: stream %d", ErrParentStreamReused, spec.StreamID))
		return nil, fmt.Errorf("%w: stream %d", ErrParentStreamReused, spec.StreamID)
	}
	parent := &Parent{
		admission:     a,
		id:            spec.StreamID,
		generation:    a.generation,
		ctx:           parentCtx,
		cancel:        cancel,
		authorized:    authorized,
		authorizedSet: authorizedSet,
		state:         ParentActive,
		pushes:        make(map[uint32]*Push),
	}
	a.parents[parent.id] = parent
	a.seenParents[parent.id] = struct{}{}
	a.stats.ParentsRegistered++
	a.addEventLocked(Event{
		Kind:                 EventParentRegistered,
		ConnectionGeneration: a.generation,
		ParentStreamID:       parent.id,
		Reason:               "parent bound to authorized source snapshot",
	})
	a.mu.Unlock()

	go a.watchParent(parent)

	if hostCtx.Err() != nil {
		a.mu.Lock()
		pending := a.refreshParentLocked(parent)
		a.mu.Unlock()
		a.finishCancellation(pending)
	}
	return parent, nil
}

// AdmitPromise validates a PUSH_PROMISE and, only after every check passes,
// runs the allocator and business handler. A syntactically valid promised
// stream ID is consumed even when a later check rejects the promise.
func (a *Admission) AdmitPromise(spec PromiseSpec) (*Push, error) {
	request, parseErr := parsePromisedRequest(spec.Headers)
	var source Source
	if parseErr == nil {
		source = request.Source
	}

	a.mu.Lock()
	if a.closed {
		a.mu.Unlock()
		return nil, ErrAdmissionClosed
	}
	if spec.ConnectionGeneration != a.generation {
		err := fmt.Errorf("%w: got %d, want %d", ErrGenerationMismatch, spec.ConnectionGeneration, a.generation)
		a.rejectPromiseLocked(spec, source, err)
		a.mu.Unlock()
		return nil, err
	}
	if !validPromisedStreamID(spec.PromisedStreamID) {
		err := fmt.Errorf("%w: promised stream %d", ErrInvalidStreamID, spec.PromisedStreamID)
		a.rejectPromiseLocked(spec, source, err)
		a.mu.Unlock()
		return nil, err
	}
	if _, exists := a.seenPushes[spec.PromisedStreamID]; exists {
		err := fmt.Errorf("%w: stream %d", ErrStreamIDReused, spec.PromisedStreamID)
		a.rejectPromiseLocked(spec, source, err)
		a.mu.Unlock()
		return nil, err
	}
	if a.lastPromised != 0 && spec.PromisedStreamID < a.lastPromised {
		a.seenPushes[spec.PromisedStreamID] = struct{}{}
		err := fmt.Errorf("%w: stream %d after %d", ErrStreamIDOutOfOrder, spec.PromisedStreamID, a.lastPromised)
		a.rejectPromiseLocked(spec, source, err)
		a.mu.Unlock()
		return nil, err
	}
	a.seenPushes[spec.PromisedStreamID] = struct{}{}
	if spec.PromisedStreamID > a.lastPromised {
		a.lastPromised = spec.PromisedStreamID
	}

	if parseErr != nil {
		a.rejectPromiseLocked(spec, source, parseErr)
		a.mu.Unlock()
		return nil, parseErr
	}
	if !validParentStreamID(spec.ParentStreamID) {
		err := fmt.Errorf("%w: parent stream %d", ErrInvalidStreamID, spec.ParentStreamID)
		a.rejectPromiseLocked(spec, source, err)
		a.mu.Unlock()
		return nil, err
	}
	parent := a.parents[spec.ParentStreamID]
	if parent == nil {
		err := fmt.Errorf("%w: parent stream %d", ErrParentNotFound, spec.ParentStreamID)
		a.rejectPromiseLocked(spec, source, err)
		a.mu.Unlock()
		return nil, err
	}

	pending := a.refreshParentLocked(parent)
	switch parent.state {
	case ParentCanceled:
		err := fmt.Errorf("%w: %s", ErrParentCanceled, parent.reason)
		a.rejectPromiseLocked(spec, source, err)
		a.mu.Unlock()
		a.finishCancellation(pending)
		return nil, err
	case ParentEnded:
		err := fmt.Errorf("%w: %s", ErrParentEnded, parent.reason)
		a.rejectPromiseLocked(spec, source, err)
		a.mu.Unlock()
		a.finishCancellation(pending)
		return nil, err
	}
	if _, ok := parent.authorizedSet[source]; !ok {
		err := fmt.Errorf("%w: source %s, parent stream %d", ErrUnauthorizedSource, source, parent.id)
		a.rejectPromiseLocked(spec, source, err)
		a.mu.Unlock()
		a.finishCancellation(pending)
		return nil, err
	}

	pushCtx, pushCancel := context.WithCancelCause(parent.ctx)
	push := &Push{
		admission:  a,
		parent:     parent,
		id:         spec.PromisedStreamID,
		generation: a.generation,
		request:    request,
		ctx:        pushCtx,
		cancel:     pushCancel,
		state:      PushPending,
	}
	a.pushes[push.id] = push
	parent.pushes[push.id] = push
	a.mu.Unlock()

	a.finishCancellation(pending)

	if a.allocator != nil {
		if err := a.allocator(pushCtx, push); err != nil {
			a.mu.Lock()
			if push.state == PushPending {
				push.state = PushCanceled
				push.reason = "allocation failed: " + err.Error()
				a.stats.AllocationFailures++
				a.addEventLocked(Event{
					Kind:                 EventPushCanceled,
					ConnectionGeneration: a.generation,
					ParentStreamID:       parent.id,
					PushStreamID:         push.id,
					Source:               push.request.Source,
					Reason:               push.reason,
					Err:                  err,
				})
			}
			a.mu.Unlock()
			pushCancel(fmt.Errorf("%w: %v", ErrAllocationFailed, err))
			return nil, fmt.Errorf("%w: %v", ErrAllocationFailed, err)
		}
	}

	a.mu.Lock()
	pending = a.refreshParentLocked(parent)
	state := push.state
	if state == PushPending {
		push.state = PushActive
		a.stats.PushesAdmitted++
		a.addEventLocked(Event{
			Kind:                 EventPushAdmitted,
			ConnectionGeneration: a.generation,
			ParentStreamID:       parent.id,
			PushStreamID:         push.id,
			Source:               push.request.Source,
			Reason:               "promise passed admission",
		})
	}
	reason := push.reason
	a.mu.Unlock()
	a.finishCancellation(pending)

	if state != PushPending {
		return nil, fmt.Errorf("%w before activation: %s", ErrPushCanceled, reason)
	}
	if a.handler != nil {
		a.mu.Lock()
		active := push.state == PushActive
		a.mu.Unlock()
		if active {
			a.handler(push)
		}
	}
	return push, nil
}

// CancelParent records cancellation of a parent request and cancels every
// still-pending or active push belonging to it. It returns false if the parent
// is unknown or no longer active.
func (a *Admission) CancelParent(streamID uint32, reason string) bool {
	a.mu.Lock()
	parent := a.parents[streamID]
	if parent == nil {
		a.mu.Unlock()
		return false
	}
	pending := a.refreshParentLocked(parent)
	if pending != nil {
		a.mu.Unlock()
		a.finishCancellation(pending)
		return false
	}
	if parent.state != ParentActive {
		a.mu.Unlock()
		return false
	}
	if reason == "" {
		reason = "parent request canceled"
	}
	cause := fmt.Errorf("%w: %s", ErrParentCanceled, reason)
	pending = a.retireParentLocked(parent, ParentCanceled, reason, cause)
	a.mu.Unlock()
	a.finishCancellation(pending)
	return true
}

// EndParent records normal completion of a parent request. A late
// PUSH_PROMISE is rejected afterward, and live dependent pushes are canceled
// because the parent's authorization lifetime has ended.
func (a *Admission) EndParent(streamID uint32, reason string) bool {
	a.mu.Lock()
	parent := a.parents[streamID]
	if parent == nil {
		a.mu.Unlock()
		return false
	}
	pending := a.refreshParentLocked(parent)
	if pending != nil {
		a.mu.Unlock()
		a.finishCancellation(pending)
		return false
	}
	if parent.state != ParentActive {
		a.mu.Unlock()
		return false
	}
	if reason == "" {
		reason = "parent request completed"
	}
	cause := fmt.Errorf("%w: %s", ErrParentEnded, reason)
	pending = a.retireParentLocked(parent, ParentEnded, reason, cause)
	a.mu.Unlock()
	a.finishCancellation(pending)
	return true
}

// CancelPush records cancellation of one push without canceling its parent.
func (a *Admission) CancelPush(streamID uint32, reason string) bool {
	a.mu.Lock()
	push := a.pushes[streamID]
	if push == nil {
		a.mu.Unlock()
		return false
	}
	pending := a.refreshParentLocked(push.parent)
	if pending != nil {
		a.mu.Unlock()
		a.finishCancellation(pending)
		return false
	}
	if push.state != PushPending && push.state != PushActive {
		a.mu.Unlock()
		return false
	}
	if reason == "" {
		reason = "push canceled"
	}
	push.state = PushCanceled
	push.reason = reason
	a.stats.PushCancellations++
	a.addEventLocked(Event{
		Kind:                 EventPushCanceled,
		ConnectionGeneration: a.generation,
		ParentStreamID:       push.parent.id,
		PushStreamID:         push.id,
		Source:               push.request.Source,
		Reason:               reason,
	})
	a.mu.Unlock()
	push.cancel(fmt.Errorf("%w: %s", ErrPushCanceled, reason))
	return true
}

// CompletePush records normal completion of an active push. The stream ID
// remains tombstoned and cannot be reused.
func (a *Admission) CompletePush(streamID uint32, reason string) bool {
	a.mu.Lock()
	push := a.pushes[streamID]
	if push == nil {
		a.mu.Unlock()
		return false
	}
	pending := a.refreshParentLocked(push.parent)
	if pending != nil {
		a.mu.Unlock()
		a.finishCancellation(pending)
		return false
	}
	if push.state != PushActive {
		a.mu.Unlock()
		return false
	}
	if reason == "" {
		reason = "push completed"
	}
	push.state = PushClosed
	push.reason = reason
	a.stats.PushesCompleted++
	a.addEventLocked(Event{
		Kind:                 EventPushCompleted,
		ConnectionGeneration: a.generation,
		ParentStreamID:       push.parent.id,
		PushStreamID:         push.id,
		Source:               push.request.Source,
		Reason:               reason,
	})
	a.mu.Unlock()
	push.cancel(ErrPushClosed)
	return true
}

// Close shuts down admission for the connection generation. All active parents
// and pushes are retired and later calls are rejected.
func (a *Admission) Close(reason string) {
	if reason == "" {
		reason = "connection closed"
	}
	a.mu.Lock()
	if a.closed {
		a.mu.Unlock()
		return
	}
	a.closed = true
	a.stats.AdmissionClosures++
	a.addEventLocked(Event{
		Kind:                 EventAdmissionClosed,
		ConnectionGeneration: a.generation,
		Reason:               reason,
	})
	cause := fmt.Errorf("%w: %s", ErrParentCanceled, reason)
	var pending []*pendingCancellation
	for _, parent := range a.parents {
		if parent.state == ParentActive {
			pending = append(pending, a.retireParentLocked(parent, ParentCanceled, reason, cause))
		}
	}
	a.mu.Unlock()
	for _, cancellation := range pending {
		a.finishCancellation(cancellation)
	}
}

// Parent returns the lifecycle record for a parent stream ID.
func (a *Admission) Parent(streamID uint32) (*Parent, bool) {
	a.mu.Lock()
	parent := a.parents[streamID]
	var pending *pendingCancellation
	if parent != nil {
		pending = a.refreshParentLocked(parent)
	}
	a.mu.Unlock()
	a.finishCancellation(pending)
	return parent, parent != nil
}

// Push returns the lifecycle record for a promised stream ID.
func (a *Admission) Push(streamID uint32) (*Push, bool) {
	a.mu.Lock()
	push := a.pushes[streamID]
	var pending *pendingCancellation
	if push != nil {
		pending = a.refreshParentLocked(push.parent)
	}
	a.mu.Unlock()
	a.finishCancellation(pending)
	return push, push != nil
}

// Events returns retained lifecycle events in chronological order. Parent
// cancellation and push cancellation are separate event kinds.
func (a *Admission) Events() []Event {
	a.mu.Lock()
	pending := a.refreshAllParentsLocked()
	events := a.events.snapshot()
	a.mu.Unlock()
	a.finishAllCancellations(pending)
	return events
}

// Stats returns monotonic counters.
func (a *Admission) Stats() Stats {
	a.mu.Lock()
	pending := a.refreshAllParentsLocked()
	stats := a.stats
	a.mu.Unlock()
	a.finishAllCancellations(pending)
	return stats
}

func (a *Admission) rejectPromiseLocked(spec PromiseSpec, source Source, err error) {
	a.stats.PromiseRejections++
	a.addEventLocked(Event{
		Kind:                 EventPromiseRejected,
		ConnectionGeneration: a.generation,
		ParentStreamID:       spec.ParentStreamID,
		PushStreamID:         spec.PromisedStreamID,
		Source:               source,
		Reason:               err.Error(),
		Err:                  err,
	})
}

func (a *Admission) addEventLocked(event Event) {
	event.Time = time.Now()
	a.events.add(event)
}

func (a *Admission) watchParent(parent *Parent) {
	<-parent.ctx.Done()
	a.mu.Lock()
	pending := a.refreshParentLocked(parent)
	a.mu.Unlock()
	a.finishCancellation(pending)
}

func (a *Admission) refreshParentLocked(parent *Parent) *pendingCancellation {
	if parent.state != ParentActive {
		return nil
	}
	if err := parent.ctx.Err(); err != nil {
		cause := context.Cause(parent.ctx)
		if cause == nil {
			cause = err
		}
		wrapped := fmt.Errorf("%w: %v", ErrParentCanceled, cause)
		return a.retireParentLocked(parent, ParentCanceled, "parent request context ended: "+cause.Error(), wrapped)
	}
	return nil
}

func (a *Admission) refreshAllParentsLocked() []*pendingCancellation {
	var pending []*pendingCancellation
	for _, parent := range a.parents {
		if cancellation := a.refreshParentLocked(parent); cancellation != nil {
			pending = append(pending, cancellation)
		}
	}
	return pending
}

func (a *Admission) retireParentLocked(parent *Parent, state ParentState, reason string, cause error) *pendingCancellation {
	parent.state = state
	parent.reason = reason
	eventKind := EventParentCanceled
	childReason := "parent request canceled: " + reason
	if state == ParentEnded {
		eventKind = EventParentEnded
		a.stats.ParentCompletions++
		childReason = "parent request ended: " + reason
	} else {
		a.stats.ParentCancellations++
	}
	a.addEventLocked(Event{
		Kind:                 eventKind,
		ConnectionGeneration: a.generation,
		ParentStreamID:       parent.id,
		Reason:               reason,
	})

	cancellation := &pendingCancellation{parent: parent, cause: cause}
	for _, push := range parent.pushes {
		if push.state != PushPending && push.state != PushActive {
			continue
		}
		push.state = PushCanceled
		push.reason = childReason
		a.stats.PushesCanceledByParent++
		cancellation.pushes = append(cancellation.pushes, push)
		a.addEventLocked(Event{
			Kind:                 EventPushCanceled,
			ConnectionGeneration: a.generation,
			ParentStreamID:       parent.id,
			PushStreamID:         push.id,
			Source:               push.request.Source,
			Reason:               childReason,
		})
	}
	return cancellation
}

func (a *Admission) finishCancellation(cancellation *pendingCancellation) {
	if cancellation == nil {
		return
	}
	cancellation.parent.cancel(cancellation.cause)
	for _, push := range cancellation.pushes {
		push.cancel(cancellation.cause)
	}
}

func (a *Admission) finishAllCancellations(cancellations []*pendingCancellation) {
	for _, cancellation := range cancellations {
		a.finishCancellation(cancellation)
	}
}

func canonicalSourceSet(sources []Source) ([]Source, map[Source]struct{}, error) {
	set := make(map[Source]struct{}, len(sources))
	for _, source := range sources {
		if !source.valid() {
			return nil, nil, fmt.Errorf("%w: %q", ErrInvalidSource, source.String())
		}
		set[source] = struct{}{}
	}
	ordered := make([]Source, 0, len(set))
	for source := range set {
		ordered = append(ordered, source)
	}
	sort.Slice(ordered, func(i, j int) bool {
		return ordered[i].String() < ordered[j].String()
	})
	return ordered, set, nil
}

func validParentStreamID(id uint32) bool {
	return id != 0 && id <= math.MaxInt32 && id%2 == 1
}

func validPromisedStreamID(id uint32) bool {
	return id != 0 && id <= math.MaxInt32 && id%2 == 0
}

// ID returns the parent's client stream ID.
func (p *Parent) ID() uint32 { return p.id }

// ConnectionGeneration returns the generation to which the parent is bound.
func (p *Parent) ConnectionGeneration() uint64 { return p.generation }

// Context is canceled when the parent is canceled or its lifetime ends.
func (p *Parent) Context() context.Context { return p.ctx }

// AuthorizedSources returns a copy of the immutable source snapshot.
func (p *Parent) AuthorizedSources() []Source {
	out := make([]Source, len(p.authorized))
	copy(out, p.authorized)
	return out
}

// Authorizes reports whether source was in the parent's snapshot.
func (p *Parent) Authorizes(source Source) bool {
	_, ok := p.authorizedSet[source]
	return ok
}

// State returns the current parent state.
func (p *Parent) State() ParentState {
	p.admission.mu.Lock()
	pending := p.admission.refreshParentLocked(p)
	state := p.state
	p.admission.mu.Unlock()
	p.admission.finishCancellation(pending)
	return state
}

// Reason returns the state-transition reason.
func (p *Parent) Reason() string {
	p.admission.mu.Lock()
	pending := p.admission.refreshParentLocked(p)
	reason := p.reason
	p.admission.mu.Unlock()
	p.admission.finishCancellation(pending)
	return reason
}

// Cancel is a convenience wrapper around Admission.CancelParent.
func (p *Parent) Cancel(reason string) bool {
	return p.admission.CancelParent(p.id, reason)
}

// End is a convenience wrapper around Admission.EndParent.
func (p *Parent) End(reason string) bool {
	return p.admission.EndParent(p.id, reason)
}

// StreamID returns the promised server stream ID.
func (p *Push) StreamID() uint32 { return p.id }

// ConnectionGeneration returns the generation to which the push is bound.
func (p *Push) ConnectionGeneration() uint64 { return p.generation }

// Parent returns the bound parent request.
func (p *Push) Parent() *Parent { return p.parent }

// Source returns the validated promised origin.
func (p *Push) Source() Source { return p.request.Source }

// Request returns a defensive copy of the validated request.
func (p *Push) Request() PromisedRequest {
	request := p.request
	request.Headers = p.request.Headers.Clone()
	return request
}

// Context is canceled when the push or its parent is canceled or completed.
func (p *Push) Context() context.Context { return p.ctx }

// State returns the current push state.
func (p *Push) State() PushState {
	p.admission.mu.Lock()
	pending := p.admission.refreshParentLocked(p.parent)
	state := p.state
	p.admission.mu.Unlock()
	p.admission.finishCancellation(pending)
	return state
}

// Reason returns the state-transition reason.
func (p *Push) Reason() string {
	p.admission.mu.Lock()
	pending := p.admission.refreshParentLocked(p.parent)
	reason := p.reason
	p.admission.mu.Unlock()
	p.admission.finishCancellation(pending)
	return reason
}

// Cancel is a convenience wrapper around Admission.CancelPush.
func (p *Push) Cancel(reason string) bool {
	return p.admission.CancelPush(p.id, reason)
}

// Complete is a convenience wrapper around Admission.CompletePush.
func (p *Push) Complete(reason string) bool {
	return p.admission.CompletePush(p.id, reason)
}
