// Package gooseadmit implements an IEC 61850 GOOSE admission control layer for
// layer-2 subscriber callbacks in protection devices.
//
// The controller is stateless with respect to the network stack: callers hand
// every raw Ethernet frame to Controller.HandleFrame. Only frames that pass
// EtherType, publisher registration, simulation, stNum/sqNum freshness and
// timeAllowedToLive checks are delivered as new events or retransmissions.
// Replayed, forged and expired data never reaches the application state.
package gooseadmit

import (
	"sync"
	"time"
)

// PublisherKey identifies a GOOSE publisher. A frame is only accepted when the
// exact tuple (source MAC, gocbRef, datSet, goID) has been registered; this
// prevents an attacker from injecting frames under an unregistered identity or
// from reusing a legitimate goID with a different source MAC.
type PublisherKey struct {
	SourceMAC [6]byte
	GoCBRef   string
	DatSet    string
	GoID      string
}

// NewPublisherKey builds a PublisherKey from a 6-byte MAC. A MAC whose length
// is not 6 produces the zero MAC so the resulting key cannot match a frame.
func NewPublisherKey(mac []byte, gocbRef, datSet, goID string) PublisherKey {
	key := PublisherKey{GoCBRef: gocbRef, DatSet: datSet, GoID: goID}
	if len(mac) == 6 {
		copy(key.SourceMAC[:], mac)
	}
	return key
}

// Verdict classifies the outcome of HandleFrame.
type Verdict int

const (
	// VerdictReject means the frame failed parsing or policy and must be
	// discarded without touching application data.
	VerdictReject Verdict = iota
	// VerdictDuplicate means an exact retransmission (same stNum and sqNum)
	// was seen again; it is harmless and is ignored, not applied.
	VerdictDuplicate
	// VerdictRetransmission means a fresh re-send of the current state
	// (same stNum, strictly greater sqNum); the payload may refresh the
	// current data but carries no new state change.
	VerdictRetransmission
	// VerdictNewEvent means stNum advanced and sqNum restarted at zero; the
	// payload represents a genuine state change and must be applied.
	VerdictNewEvent
)

// DropReason explains why a frame was rejected.
type DropReason int

const (
	DropNone DropReason = iota
	DropMalformed
	DropNotGOOSE
	DropUnregistered
	DropSimulation
	DropStNumWithoutSqZero
	DropSqReplay
	DropSqWrap
	DropStNumDecreased
)

// Result is returned for every frame handed to the controller.
type Result struct {
	Verdict Verdict
	Reason  DropReason
	// Key is the publisher identity parsed from the frame, valid even when
	// the frame is rejected for being unregistered.
	Key PublisherKey
	// StNum and SqNum are the sequence numbers carried by the frame.
	StNum uint32
	SqNum uint32
}

// Config tunes a Controller. The zero value uses the real wall clock and runs
// as a production (non-test) subscriber.
type Config struct {
	// TestMode makes the subscriber accept frames whose simulation bit is
	// set. Production subscribers must leave this false: simulated GOOSE is
	// then always rejected.
	TestMode bool
	// Now returns the current time. It is a field rather than a parameter so
	// tests can drive timeAllowedToLive expiry deterministically.
	Now func() time.Time
}

// publisherState tracks the freshness bookkeeping of one registered publisher.
type publisherState struct {
	lastStNum      uint32
	lastSqNum      uint32
	lastSeen       time.Time
	ttl            time.Duration
	initialized    bool
	expired        bool
	restartPending bool
}

// Controller holds the admission table. All methods are safe for concurrent
// use because a layer-2 callback may run on several receive goroutines.
type Controller struct {
	mu       sync.Mutex
	pubs     map[PublisherKey]*publisherState
	testMode bool
	now      func() time.Time
}

// New creates a Controller. A nil Config is equivalent to the zero Config.
func New(cfg *Config) *Controller {
	c := &Controller{
		pubs: make(map[PublisherKey]*publisherState),
		now:  time.Now,
	}
	if cfg != nil {
		c.testMode = cfg.TestMode
		if cfg.Now != nil {
			c.now = cfg.Now
		}
	}
	return c
}

// Register enables a publisher tuple. Registering an existing key is a no-op;
// use NotifyRestart to announce a device restart for an existing publisher.
func (c *Controller) Register(key PublisherKey) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, ok := c.pubs[key]; ok {
		return
	}
	c.pubs[key] = &publisherState{}
}

// Unregister removes a publisher tuple. Frames from it are subsequently
// rejected as unregistered.
func (c *Controller) Unregister(key PublisherKey) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.pubs, key)
}

// NotifyRestart arms a one-shot restart announcement for a registered
// publisher. The next accepted frame from that publisher is allowed to carry
// a smaller stNum and reinitializes the sequence baseline; the arm is then
// consumed. It models an out-of-band signal that the publishing IED rebooted.
func (c *Controller) NotifyRestart(key PublisherKey) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	state, ok := c.pubs[key]
	if !ok {
		return false
	}
	state.restartPending = true
	return true
}

// SetTestMode switches simulation-bit acceptance at runtime, for example when
// an engineering workstation puts the bay into test mode.
func (c *Controller) SetTestMode(enabled bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.testMode = enabled
}

// HandleFrame is the entry point for every raw Ethernet frame delivered by the
// layer-2 subscription callback. It never panics on attacker-controlled input.
func (c *Controller) HandleFrame(raw []byte) Result {
	frame, err := parseFrame(raw)
	if err != nil {
		return Result{Verdict: VerdictReject, Reason: mapParseError(err)}
	}

	key := PublisherKey{
		SourceMAC: frame.srcMAC,
		GoCBRef:   frame.gocbRef,
		DatSet:    frame.datSet,
		GoID:      frame.goID,
	}
	result := Result{Key: key, StNum: frame.stNum, SqNum: frame.sqNum}

	c.mu.Lock()
	defer c.mu.Unlock()

	state, ok := c.pubs[key]
	if !ok {
		result.Verdict = VerdictReject
		result.Reason = DropUnregistered
		return result
	}

	if frame.simulation && !c.testMode {
		result.Verdict = VerdictReject
		result.Reason = DropSimulation
		return result
	}

	now := c.now()

	// A restart announcement permits the next frame to reset the baseline with
	// an arbitrary stNum, including a smaller one after an IED reboot.
	if state.restartPending {
		state.restartPending = false
		resetBaseline(state, frame, now)
		result.Verdict = VerdictNewEvent
		return result
	}

	// First frame from a registered publisher establishes the baseline; there
	// is no prior value to compare against.
	if !state.initialized {
		resetBaseline(state, frame, now)
		result.Verdict = VerdictNewEvent
		return result
	}

	switch {
	case frame.stNum > state.lastStNum:
		// A state change must restart sqNum at zero per IEC 61850-8-1. A frame
		// that bumps stNum but keeps sqNum non-zero is malformed or forged and
		// is dropped even though stNum looks "newer".
		if frame.sqNum != 0 {
			result.Verdict = VerdictReject
			result.Reason = DropStNumWithoutSqZero
			return result
		}
		accept(state, frame, now)
		result.Verdict = VerdictNewEvent

	case frame.stNum == state.lastStNum:
		switch {
		case frame.sqNum > state.lastSqNum:
			accept(state, frame, now)
			result.Verdict = VerdictRetransmission
		case frame.sqNum == state.lastSqNum:
			// Exact duplicate: ignore without refreshing liveness, so a
			// captured frame replayed in a loop cannot extend the TTL.
			result.Verdict = VerdictDuplicate
		default:
			// sqNum moved backwards. The only legal backward step is a
			// 32-bit wrap from 2^32-1 to 0.
			if state.lastSqNum == ^uint32(0) && frame.sqNum == 0 {
				accept(state, frame, now)
				result.Verdict = VerdictRetransmission
			} else {
				result.Verdict = VerdictReject
				result.Reason = DropSqReplay
			}
		}

	default:
		// stNum decreased: an old frame replayed, or a reboot that the caller
		// has not announced. Sequence state is deliberately retained.
		result.Verdict = VerdictReject
		result.Reason = DropStNumDecreased
	}

	return result
}

// Sweep re-evaluates timeAllowedToLive for every registered publisher and marks
// those that have not sent an accepted frame within their TTL as invalid. Call
// it periodically (for example from a timer) or consult IsValid on demand.
// It returns the keys whose data just transitioned to invalid.
func (c *Controller) Sweep() []PublisherKey {
	c.mu.Lock()
	defer c.mu.Unlock()

	now := c.now()
	var expired []PublisherKey
	for key, state := range c.pubs {
		if !state.initialized || state.expired {
			continue
		}
		if now.Sub(state.lastSeen) >= state.ttl {
			state.expired = true
			expired = append(expired, key)
		}
	}
	return expired
}

// IsValid reports whether the publisher's data is currently considered live.
// Data becomes invalid once timeAllowedToLive elapses without a fresh accepted
// frame; the next accepted frame clears the flag. Stale sequence numbers are
// preserved across expiry so replayed frames cannot revalidate dead data.
func (c *Controller) IsValid(key PublisherKey) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	state, ok := c.pubs[key]
	if !ok || !state.initialized || state.expired {
		return false
	}
	return c.now().Sub(state.lastSeen) < state.ttl
}

// Status is an observability snapshot of one publisher.
type Status struct {
	Key            PublisherKey
	Registered     bool
	Initialized    bool
	Expired        bool
	RestartPending bool
	LastStNum      uint32
	LastSqNum      uint32
	LastSeen       time.Time
	TTL            time.Duration
}

// StatusOf returns a point-in-time snapshot for diagnostics and tests.
func (c *Controller) StatusOf(key PublisherKey) Status {
	c.mu.Lock()
	defer c.mu.Unlock()
	state, ok := c.pubs[key]
	if !ok {
		return Status{Key: key}
	}
	return Status{
		Key:            key,
		Registered:     true,
		Initialized:    state.initialized,
		Expired:        state.expired,
		RestartPending: state.restartPending,
		LastStNum:      state.lastStNum,
		LastSqNum:      state.lastSqNum,
		LastSeen:       state.lastSeen,
		TTL:            state.ttl,
	}
}

func accept(state *publisherState, frame *gooseFrame, now time.Time) {
	state.lastStNum = frame.stNum
	state.lastSqNum = frame.sqNum
	state.ttl = time.Duration(frame.ttl) * time.Millisecond
	state.lastSeen = now
	state.expired = false
}

func resetBaseline(state *publisherState, frame *gooseFrame, now time.Time) {
	state.initialized = true
	accept(state, frame, now)
}

func mapParseError(err error) DropReason {
	switch err {
	case errNotGOOSE:
		return DropNotGOOSE
	default:
		return DropMalformed
	}
}
