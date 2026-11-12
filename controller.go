package netsec

import (
	"context"
	"fmt"
	"sync"
)

// Handler is the host's business callback. It is invoked only for push
// resources that passed every admission check against the authorization
// context of their parent request; it is never invoked for a rejected,
// canceled, or generation-stale promise.
type Handler func(resource PushResource)

// PushResource is the immutable, unforgeable description handed to the
// business layer. It carries back the parent stream, connection generation,
// and the exact authorization set that authorized the push, so downstream
// consumers can attribute every byte they receive.
type PushResource struct {
	ParentStreamID   uint32
	PromisedStreamID uint32
	Generation       uint64
	Authorized       []Origin
	Request          ValidatedRequest
}

// ParentRequest binds an incoming client request stream to the authorization
// context the host established for it. PUSH_PROMISE frames are only accepted
// when they reference a bound, still-open parent stream.
type ParentRequest struct {
	StreamID   uint32
	Authorized []Origin
}

// PushPromise is a raw PUSH_PROMISE frame as observed on the wire. The
// controller never trusts its contents until validation succeeds.
type PushPromise struct {
	ParentStreamID   uint32
	PromisedStreamID uint32
	Headers          []HeaderField
}

// Admission is a validated, bound promise that has not yet been delivered.
// Values are only returned by Controller.AdmitPromise, so the host cannot
// forge one with someone else's authorization context.
type Admission struct {
	controller *Controller
	key        promiseKey
	resource   PushResource
}

// Resource returns the authorized resource description.
func (a Admission) Resource() PushResource { return a.resource }

type parentKey struct {
	generation uint64
	streamID   uint32
}

type promiseKey struct {
	generation uint64
	streamID   uint32
}

type parentState struct {
	authorized map[Origin]bool
	open       bool
	canceled   bool
}

type pushState struct {
	parent     uint32
	generation uint64
	delivered  bool
	canceled   bool
	resource   PushResource
}

type config struct {
	safeMethods map[string]bool
	schemes     map[string]bool
}

// Option customizes Controller admission policy.
type Option func(*config)

// WithAllowedMethods replaces the default safe-method set (GET, HEAD, OPTIONS,
// TRACE) used when validating promised requests.
func WithAllowedMethods(methods ...string) Option {
	return func(c *config) {
		c.safeMethods = make(map[string]bool, len(methods))
		for _, method := range methods {
			c.safeMethods[method] = true
		}
	}
}

// WithAllowedSchemes restricts accepted :scheme values. Without this option
// any well-formed scheme whose port can be resolved is accepted.
func WithAllowedSchemes(schemes ...string) Option {
	return func(c *config) {
		c.schemes = make(map[string]bool, len(schemes))
		for _, scheme := range schemes {
			c.schemes[scheme] = true
		}
	}
}

// Controller enforces push admission for one logical HTTP/2 session. A
// session survives physical connection re-establishment; every incarnation is
// distinguished by its generation, so promises and stream identifiers from a
// dead connection can never be replayed against the new one.
//
// A Controller is safe for concurrent use by the frame-dispatch goroutine and
// the host's business goroutines.
type Controller struct {
	mu         sync.Mutex
	handler    Handler
	generation uint64
	parents    map[parentKey]*parentState
	promises   map[promiseKey]*pushState
	usedIDs    map[promiseKey]bool
	events     []Event
	stats      Stats
	cfg        config
}

// NewController creates a session-scoped admission controller. handler is the
// single business sink through which every authorized push is delivered.
func NewController(handler Handler, opts ...Option) *Controller {
	cfg := config{safeMethods: defaultSafeMethods}
	for _, opt := range opts {
		opt(&cfg)
	}
	return &Controller{
		handler:    handler,
		generation: 1,
		parents:    make(map[parentKey]*parentState),
		promises:   make(map[promiseKey]*pushState),
		usedIDs:    make(map[promiseKey]bool),
		cfg:        cfg,
	}
}

// Generation returns the current connection incarnation.
func (c *Controller) Generation() uint64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.generation
}

// BindParent records the authorization context of a client request stream.
// It must be called before any PUSH_PROMISE referencing the stream is
// admitted. A client-initiated stream must have an odd identifier.
func (c *Controller) BindParent(req ParentRequest) error {
	if req.StreamID == 0 || req.StreamID%2 == 0 {
		return ErrInvalidStreamID
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	key := parentKey{generation: c.generation, streamID: req.StreamID}
	if _, exists := c.parents[key]; exists {
		return ErrStreamInUse
	}
	authorized := make(map[Origin]bool, len(req.Authorized))
	for _, origin := range req.Authorized {
		if !origin.Valid() {
			return ErrInvalidOrigin
		}
		authorized[origin] = true
	}
	c.parents[key] = &parentState{authorized: authorized, open: true}
	c.recordLocked(EventParentBound, req.StreamID, 0, c.generation, nil)
	return nil
}

// AdmitPromise validates a PUSH_PROMISE and, only after every check passes,
// reserves its promised stream and binds it to the parent's generation and
// authorization set. No state is retained on any failure, which is what makes
// "pseudo-header first, allocation second" enforceable.
func (c *Controller) AdmitPromise(p PushPromise) (Admission, error) {
	if p.PromisedStreamID == 0 || p.PromisedStreamID%2 != 0 {
		return Admission{}, ErrInvalidStreamID
	}

	// Wire validation runs without touching any controller state.
	validated, err := validateHeaderBlock(p.Headers, c.cfg.safeMethods, c.cfg.schemes)
	if err != nil {
		c.reject(p, err)
		return Admission{}, err
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	pKey := parentKey{generation: c.generation, streamID: p.ParentStreamID}
	parent, ok := c.parents[pKey]
	if !ok {
		// Distinguish a parent bound on an earlier incarnation (a late
		// promise from a dead connection) from a parent that never existed.
		if c.parentExistsAnyGeneration(p.ParentStreamID) {
			err = ErrGenerationMismatch
		} else {
			err = ErrUnknownParent
		}
		c.rejectLocked(p, err)
		return Admission{}, err
	}
	if parent.canceled {
		err = ErrParentCanceled
		c.rejectLocked(p, err)
		return Admission{}, err
	}
	if !parent.open {
		err = ErrParentCompleted
		c.rejectLocked(p, err)
		return Admission{}, err
	}

	sKey := promiseKey{generation: c.generation, streamID: p.PromisedStreamID}
	if c.usedIDs[sKey] {
		err = ErrStreamInUse
		c.rejectLocked(p, err)
		return Admission{}, err
	}
	if !parent.authorized[validated.Origin] {
		err = fmt.Errorf("%w: origin %s not authorized for parent stream %d",
			ErrOriginNotAuthorized, validated.Origin, p.ParentStreamID)
		c.rejectLocked(p, err)
		return Admission{}, err
	}

	authorized := make([]Origin, 0, len(parent.authorized))
	for origin := range parent.authorized {
		authorized = append(authorized, origin)
	}
	resource := PushResource{
		ParentStreamID:   p.ParentStreamID,
		PromisedStreamID: p.PromisedStreamID,
		Generation:       c.generation,
		Authorized:       authorized,
		Request:          validated,
	}
	c.usedIDs[sKey] = true
	c.promises[sKey] = &pushState{
		parent:     p.ParentStreamID,
		generation: c.generation,
		resource:   resource,
	}
	c.stats.Admitted++
	c.recordLocked(EventPromiseAdmitted, p.ParentStreamID, p.PromisedStreamID, c.generation, nil)

	return Admission{controller: c, key: sKey, resource: resource}, nil
}

// Deliver hands an admitted resource to the business handler. Delivery is
// refused if the parent request or the push stream was canceled in the
// meantime, so the handler can never observe a resource whose authorization
// context has been withdrawn.
func (a Admission) Deliver(ctx context.Context) error {
	if a.controller == nil {
		return ErrUnknownAdmission
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	c := a.controller

	c.mu.Lock()
	state, ok := c.promises[a.key]
	if !ok {
		c.mu.Unlock()
		return ErrUnknownAdmission
	}
	if state.canceled {
		c.mu.Unlock()
		return ErrPushCanceled
	}
	if state.generation != c.generation {
		c.mu.Unlock()
		return ErrGenerationMismatch
	}
	parent := c.parents[parentKey{generation: a.key.generation, streamID: state.parent}]
	if parent == nil || parent.canceled {
		c.mu.Unlock()
		return ErrParentCanceled
	}
	if !parent.open {
		c.mu.Unlock()
		return ErrParentCompleted
	}
	if state.delivered {
		c.mu.Unlock()
		return ErrAlreadyDelivered
	}
	state.delivered = true
	c.stats.Delivered++
	c.recordLocked(EventResourceDelivered, state.parent, a.key.streamID, a.key.generation, nil)
	handler := c.handler
	resource := state.resource
	c.mu.Unlock()

	// The host callback runs outside the lock so a slow handler cannot block
	// frame processing and cannot deadlock back into the controller.
	if handler != nil {
		handler(resource)
	}
	return ctx.Err()
}

// CancelParent records cancellation of a client (parent) request stream. It is
// accounted separately from push-stream cancellation. The operation is
// idempotent: repeat RST_STREAM frames do not inflate the counters.
func (c *Controller) CancelParent(streamID uint32, reason CancelReason) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	pKey := parentKey{generation: c.generation, streamID: streamID}
	state, ok := c.parents[pKey]
	if !ok {
		if c.parentExistsAnyGeneration(streamID) {
			return ErrGenerationMismatch
		}
		return ErrUnknownParent
	}
	if !state.canceled {
		state.canceled = true
		state.open = false
		c.stats.ParentCanceled++
		c.recordCancelLocked(EventParentCanceled, streamID, 0, reason)
	}
	return nil
}

// CompleteParent marks a parent stream as normally finished (end of stream).
// Promises arriving afterwards are rejected as late.
func (c *Controller) CompleteParent(streamID uint32) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	pKey := parentKey{generation: c.generation, streamID: streamID}
	state, ok := c.parents[pKey]
	if !ok {
		if c.parentExistsAnyGeneration(streamID) {
			return ErrGenerationMismatch
		}
		return ErrUnknownParent
	}
	if state.canceled {
		return ErrParentCanceled
	}
	state.open = false
	c.recordLocked(EventParentCompleted, streamID, 0, c.generation, nil)
	return nil
}

// CancelPush records cancellation of a promised (server-initiated) stream,
// separately from any parent cancellation. It is idempotent.
func (c *Controller) CancelPush(promisedStreamID uint32, reason CancelReason) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	sKey := promiseKey{generation: c.generation, streamID: promisedStreamID}
	state, ok := c.promises[sKey]
	if !ok {
		if c.promiseExistsAnyGeneration(promisedStreamID) {
			return ErrGenerationMismatch
		}
		return ErrUnknownAdmission
	}
	if !state.canceled {
		state.canceled = true
		c.stats.PushCanceled++
		c.recordCancelLocked(EventPushCanceled, state.parent, promisedStreamID, reason)
	}
	return nil
}

// AdvanceGeneration marks the start of a new physical connection incarnation
// while keeping the same logical session. Parents and promises from prior
// incarnations remain retained for audit and rejection but can never admit or
// deliver again; stream identifiers may be reused only on the new generation.
func (c *Controller) AdvanceGeneration() uint64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.generation++
	c.recordLocked(EventGenerationAdvanced, 0, 0, c.generation, nil)
	return c.generation
}

// Events returns a snapshot of the ordered audit trail.
func (c *Controller) Events() []Event {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]Event, len(c.events))
	copy(out, c.events)
	return out
}

// Stats returns a snapshot of the admission counters.
func (c *Controller) Stats() Stats {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.stats
}

func (c *Controller) reject(p PushPromise, err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.rejectLocked(p, err)
}

func (c *Controller) rejectLocked(p PushPromise, err error) {
	c.stats.Rejected++
	c.recordLocked(EventPromiseRejected, p.ParentStreamID, p.PromisedStreamID, c.generation, err)
}

func (c *Controller) recordLocked(kind EventKind, parentID, pushID uint32, generation uint64, err error) {
	c.events = append(c.events, Event{
		Kind:       kind,
		ParentID:   parentID,
		PromisedID: pushID,
		Generation: generation,
		Err:        err,
	})
}

func (c *Controller) recordCancelLocked(kind EventKind, parentID, pushID uint32, reason CancelReason) {
	c.events = append(c.events, Event{
		Kind:       kind,
		ParentID:   parentID,
		PromisedID: pushID,
		Generation: c.generation,
		Reason:     reason,
	})
}

func (c *Controller) parentExistsAnyGeneration(streamID uint32) bool {
	for key := range c.parents {
		if key.streamID == streamID {
			return true
		}
	}
	return false
}

func (c *Controller) promiseExistsAnyGeneration(streamID uint32) bool {
	for key := range c.promises {
		if key.streamID == streamID {
			return true
		}
	}
	return false
}
