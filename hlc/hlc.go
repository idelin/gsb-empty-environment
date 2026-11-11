// Package hlc implements a persistent hybrid logical clock (HLC) for a
// multi-node audit service.
//
// A timestamp is the triple (physical, logical, nodeID): a signed 64-bit
// physical component in milliseconds, a 16-bit logical counter and the
// minting node's ID. Timestamps are totally ordered lexicographically, so
// concurrent events sharing a physical millisecond are still ordered.
//
// Causal ordering follows the standard HLC rules: local events take
// max(wall, last) and receive events take max(wall, last, remote), bumping
// the logical counter whenever the physical component does not advance.
// Every emitted timestamp is persisted to a durable Store before it is
// returned, which gives a recoverable monotonic lower bound: a restarted
// process cannot re-issue a value it had already returned, and a wall-clock
// rollback cannot move timestamps backwards.
//
// When the 16-bit counter is exhausted within one physical millisecond, the
// clock waits for the physical clock to advance. The wait happens without
// holding the clock lock, so other goroutines keep making progress.
//
// Remote timestamps whose physical component lies more than MaxDrift
// milliseconds ahead of the local wall clock are rejected, preventing a
// skewed or malicious peer from pulling the local clock into the future.
package hlc

import (
	"context"
	"errors"
	"fmt"
	"sync"
)

// DefaultMaxDrift is the default maximum tolerated remote clock lead, in
// milliseconds.
const DefaultMaxDrift int64 = 1000

// ErrRemoteAhead is returned by Receive when a remote timestamp is more than
// MaxDrift milliseconds ahead of the local wall clock. The local clock is
// left untouched in that case.
var ErrRemoteAhead = errors.New("hlc: remote timestamp too far in the future")

// opIDKey is the context key used to tag an operation with an externally
// chosen identity, mainly so test schedulers can correlate hook visits with
// their goroutines.
type opIDKey struct{}

// Hook step names. A Hook, if configured, is invoked at deterministic points
// of every clock operation so tests can interleave goroutines and inject
// crashes. Steps marked "outside the lock" run while Clock.mu is not held.
const (
	// StepEnter fires once when an operation starts, before any locking.
	StepEnter = "enter"
	// StepLocked fires immediately after acquiring the clock lock.
	StepLocked = "locked"
	// StepBeforeWait fires outside the lock when the logical counter has
	// overflowed and the clock is about to wait for physical time to advance.
	StepBeforeWait = "before_wait"
	// StepAfterWait fires outside the lock once the wait returns.
	StepAfterWait = "after_wait"
	// StepBeforeSave fires while holding the lock, immediately before the
	// candidate state is durably persisted.
	StepBeforeSave = "before_save"
	// StepSaved fires while holding the lock after persistence succeeded and
	// before the value is returned. A crash simulated at this point models the
	// window between durable write and caller-visible return.
	StepSaved = "saved"
	// StepRejected fires while holding the lock when a remote timestamp is
	// rejected for being too far ahead; no state is mutated.
	StepRejected = "rejected"
)

// Hook is an optional instrumentation callback. The same operation keeps one
// opID across all its steps. Hooks must not block indefinitely unless the
// test scheduler intends to park the calling goroutine.
type Hook func(ctx context.Context, opID uint64, step string)

// Config configures a Clock.
type Config struct {
	// NodeID stamps every timestamp minted by this clock.
	NodeID uint64
	// Physical reads (and waits on) the physical clock. Required.
	Physical PhysicalClock
	// Store durably persists the monotonic lower bound. Required.
	Store Store
	// MaxDrift is the maximum accepted lead of a remote timestamp over the
	// local wall clock, in milliseconds. Zero or negative uses DefaultMaxDrift.
	MaxDrift int64
	// Hook is an optional test instrumentation callback.
	Hook Hook
}

// Clock is a persistent hybrid logical clock. It is safe for concurrent use.
type Clock struct {
	mu       sync.Mutex
	state    State
	physical PhysicalClock
	store    Store
	nodeID   uint64
	maxDrift int64
	hook     Hook
	nextOp   uint64
}

// New loads the persisted lower bound from the store and returns a ready
// clock. Persisted state always wins over the physical clock on startup.
func New(cfg Config) (*Clock, error) {
	if cfg.Physical == nil {
		return nil, errors.New("hlc: Config.Physical is required")
	}
	if cfg.Store == nil {
		return nil, errors.New("hlc: Config.Store is required")
	}
	state, err := cfg.Store.Load()
	if err != nil {
		return nil, fmt.Errorf("hlc: load persisted state: %w", err)
	}
	maxDrift := cfg.MaxDrift
	if maxDrift <= 0 {
		maxDrift = DefaultMaxDrift
	}
	return &Clock{
		state:    state,
		physical: cfg.Physical,
		store:    cfg.Store,
		nodeID:   cfg.NodeID,
		maxDrift: maxDrift,
		hook:     cfg.Hook,
	}, nil
}

// NodeID returns the ID stamped on locally minted timestamps.
func (c *Clock) NodeID() uint64 { return c.nodeID }

// LowerBound returns a copy of the current durable lower bound.
func (c *Clock) LowerBound() State {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.state
}

// Now mints a timestamp for a local event (send or any purely local action).
// The returned timestamp is guaranteed to be after every timestamp
// previously emitted or accepted by this clock instance, including across
// restarts, and to be durable before the call returns.
func (c *Clock) Now(ctx context.Context) (Timestamp, error) {
	opID := c.beginOp(ctx)
	for {
		c.fire(ctx, opID, StepLocked)
		c.mu.Lock()
		wall := c.physical.NowMillis()
		previous := c.state
		next := previous

		if wall > next.Physical {
			next.Physical = wall
			next.Logical = 0
		} else {
			// Same millisecond as the last event, or the wall clock moved
			// backwards: keep the old physical lower bound and tick logic.
			if next.Logical == MaxLogical {
				if err := c.waitForPhysical(ctx, opID, previous.Physical+1); err != nil {
					return Timestamp{}, err
				}
				continue
			}
			next.Logical++
		}

		ts, err := c.commit(ctx, opID, previous, next)
		if err != nil {
			return Timestamp{}, err
		}
		return ts, nil
	}
}

// Receive incorporates a timestamp from a remote node and returns the
// timestamp minted for the receive event. The returned timestamp is after
// both the remote timestamp and every previously local timestamp. A remote
// timestamp leading the local wall clock by more than MaxDrift is rejected
// with an error wrapping ErrRemoteAhead and leaves local state untouched.
func (c *Clock) Receive(ctx context.Context, remote Timestamp) (Timestamp, error) {
	opID := c.beginOp(ctx)
	for {
		c.fire(ctx, opID, StepLocked)
		c.mu.Lock()
		wall := c.physical.NowMillis()
		previous := c.state

		if remote.Physical > wall+c.maxDrift {
			c.fire(ctx, opID, StepRejected)
			c.mu.Unlock()
			return Timestamp{}, fmt.Errorf(
				"%w: physical=%d localWall=%d maxDriftMs=%d",
				ErrRemoteAhead, remote.Physical, wall, c.maxDrift)
		}

		// If neither the wall clock nor the remote timestamp can move the
		// physical component past the current lower bound and the logical
		// counter is already exhausted, wait for physical time to advance
		// (without holding the lock) before computing anything.
		if previous.Logical == MaxLogical &&
			wall <= previous.Physical && remote.Physical <= previous.Physical {
			if err := c.waitForPhysical(ctx, opID, previous.Physical+1); err != nil {
				return Timestamp{}, err
			}
			continue
		}

		next := previous
		switch {
		case wall > previous.Physical && wall > remote.Physical:
			next.Physical = wall
			next.Logical = 0
		case remote.Physical > previous.Physical:
			next.Physical = remote.Physical
			next.Logical = remote.Logical + 1
		case previous.Physical > remote.Physical:
			next.Logical++
		default: // previous.Physical == remote.Physical
			if remote.Logical > next.Logical {
				next.Logical = remote.Logical
			}
			next.Logical++
		}

		ts, err := c.commit(ctx, opID, previous, next)
		if err != nil {
			return Timestamp{}, err
		}
		return ts, nil
	}
}

// waitForPhysical releases the lock, blocks until the physical clock reaches
// target, then returns so the caller retries the whole computation. The lock
// is never held while waiting.
func (c *Clock) waitForPhysical(ctx context.Context, opID uint64, target int64) error {
	c.mu.Unlock()
	// Fired after the unlock so tests can assert (via TryLock) that no lock
	// is held while the clock waits for physical time to advance.
	c.fire(ctx, opID, StepBeforeWait)
	c.physical.WaitUntil(target)
	c.fire(ctx, opID, StepAfterWait)
	return nil
}

// commit durably persists next while holding the lock. On persistence
// failure the in-memory state is restored to previous and the candidate value
// is discarded, so an un-persisted timestamp can never be returned.
func (c *Clock) commit(ctx context.Context, opID uint64, previous, next State) (Timestamp, error) {
	c.fire(ctx, opID, StepBeforeSave)
	if err := c.store.Save(next); err != nil {
		c.state = previous
		c.mu.Unlock()
		return Timestamp{}, fmt.Errorf("hlc: persist timestamp: %w", err)
	}
	c.state = next
	c.fire(ctx, opID, StepSaved)
	c.mu.Unlock()
	return Timestamp{
		Physical: next.Physical,
		Logical:  next.Logical,
		NodeID:   c.nodeID,
	}, nil
}

func (c *Clock) beginOp(ctx context.Context) uint64 {
	c.mu.Lock()
	opID := c.nextOp
	c.nextOp++
	c.mu.Unlock()
	c.fire(ctx, opID, StepEnter)
	return opID
}

func (c *Clock) fire(ctx context.Context, opID uint64, step string) {
	if c.hook != nil {
		// Tests tag operations through the context so they can correlate hook
		// visits with their own goroutine identity; fall back to the internal
		// sequence number when no test tag is present.
		if external, ok := ctx.Value(opIDKey{}).(uint64); ok {
			opID = external
		}
		c.hook(ctx, opID, step)
	}
}
