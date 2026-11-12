package netsec

import "fmt"

// Conn tracks the stream states of one logical connection for a
// frame-dispatch host. It is not safe for concurrent use; the host is
// expected to serialize frame dispatch per connection.
type Conn struct {
	role       Role
	generation uint64
	streams    map[uint32]StreamState
	closed     map[uint32]struct{} // tombstones for late-frame detection
	nextLocal  uint32
	nextRemote uint32
	firstErr   error
}

// NewConn returns a Conn for the given local endpoint role, at
// generation 0 with no streams.
func NewConn(role Role) *Conn {
	c := &Conn{role: role}
	c.release()
	return c
}

// Generation returns the current connection generation. The host stamps
// every Event with the generation of the connection incarnation it
// arrived on.
func (c *Conn) Generation() uint64 { return c.generation }

// Reset moves the connection to a new generation and releases all stream
// resources (live streams and closed tombstones). Stream ID numbering
// restarts from the role's initial IDs, so IDs may be reused by the new
// generation. The first recorded error is kept for diagnostics.
func (c *Conn) Reset() uint64 {
	c.generation++
	c.release()
	return c.generation
}

func (c *Conn) release() {
	c.streams = make(map[uint32]StreamState)
	c.closed = make(map[uint32]struct{})
	c.nextLocal = c.role.firstLocalID()
	c.nextRemote = c.role.firstRemoteID()
}

// FirstError returns the first state violation observed since the Conn
// was created, or nil if none occurred.
func (c *Conn) FirstError() error { return c.firstErr }

// LiveStreams reports how many streams currently hold resources.
func (c *Conn) LiveStreams() int { return len(c.streams) }

// State returns the current state of a stream ID: StateClosed for IDs
// that were closed during this generation, StateIdle for IDs never used.
func (c *Conn) State(id uint32) StreamState {
	if st, ok := c.streams[id]; ok {
		return st
	}
	if _, ok := c.closed[id]; ok {
		return StateClosed
	}
	return StateIdle
}

// NextLocalStreamID returns the smallest stream ID the local endpoint
// may open next. IDs increase by two, keeping the role's parity.
func (c *Conn) NextLocalStreamID() uint32 { return c.nextLocal }

// Dispatch validates one frame against the stream state machine and
// applies the resulting transition. The host must only invoke its
// business callback when the verdict is Deliver. The first rejection is
// recorded and available via FirstError.
func (c *Conn) Dispatch(ev Event) (Verdict, error) {
	v, err := c.dispatch(ev)
	if err != nil && c.firstErr == nil {
		c.firstErr = err
	}
	return v, err
}

func (c *Conn) dispatch(ev Event) (Verdict, error) {
	if ev.Generation != c.generation {
		return Reject, fmt.Errorf("stream %d: %w (frame gen %d, conn gen %d)",
			ev.StreamID, ErrStaleGeneration, ev.Generation, c.generation)
	}
	switch ev.Type {
	case FrameHeaders:
		return c.onHeaders(ev)
	case FrameData:
		return c.onData(ev)
	case FrameRSTStream:
		return c.onRST(ev)
	case FramePushPromise:
		return c.onPushPromise(ev)
	}
	return Reject, fmt.Errorf("stream %d: unknown frame type %d", ev.StreamID, ev.Type)
}

func (c *Conn) onHeaders(ev Event) (Verdict, error) {
	switch c.State(ev.StreamID) {
	case StateIdle:
		if ev.Dir == Send {
			if c.role != Client {
				return Reject, fmt.Errorf("stream %d: %w: servers open streams via PUSH_PROMISE",
					ev.StreamID, ErrRoleViolation)
			}
			if err := c.checkNewID(ev.StreamID, c.role.localParity(), &c.nextLocal); err != nil {
				return Reject, err
			}
		} else {
			if c.role != Server {
				return Reject, fmt.Errorf("stream %d: %w: servers do not open streams with HEADERS",
					ev.StreamID, ErrRoleViolation)
			}
			if err := c.checkNewID(ev.StreamID, c.role.remoteParity(), &c.nextRemote); err != nil {
				return Reject, err
			}
		}
		c.openStream(ev.StreamID, ev.EndStream, ev.Dir)
		return Deliver, nil
	case StateOpen:
		c.applyEndStream(ev)
		return Deliver, nil
	case StateHalfClosedLocal:
		if ev.Dir == Send {
			return Reject, fmt.Errorf("stream %d: %w", ev.StreamID, ErrHalfClosedSend)
		}
		c.applyEndStream(ev)
		return Deliver, nil
	case StateHalfClosedRemote:
		if ev.Dir == Recv {
			return Reject, fmt.Errorf("stream %d: %w", ev.StreamID, ErrHalfClosedRecv)
		}
		c.applyEndStream(ev)
		return Deliver, nil
	case StateReservedLocal:
		if ev.Dir != Send {
			return Reject, fmt.Errorf("stream %d: %w: reserved(local) awaits local HEADERS",
				ev.StreamID, ErrReservedStream)
		}
		c.streams[ev.StreamID] = StateHalfClosedRemote
		return Deliver, nil
	case StateReservedRemote:
		if ev.Dir != Recv {
			return Reject, fmt.Errorf("stream %d: %w: reserved(remote) awaits peer HEADERS",
				ev.StreamID, ErrReservedStream)
		}
		c.streams[ev.StreamID] = StateHalfClosedLocal
		return Deliver, nil
	}
	return Reject, fmt.Errorf("stream %d: %w", ev.StreamID, ErrClosedStream)
}

func (c *Conn) onData(ev Event) (Verdict, error) {
	switch c.State(ev.StreamID) {
	case StateOpen:
		c.applyEndStream(ev)
		return Deliver, nil
	case StateHalfClosedRemote:
		if ev.Dir == Recv {
			return Reject, fmt.Errorf("stream %d: %w", ev.StreamID, ErrHalfClosedRecv)
		}
		c.applyEndStream(ev)
		return Deliver, nil
	case StateHalfClosedLocal:
		if ev.Dir == Send {
			return Reject, fmt.Errorf("stream %d: %w", ev.StreamID, ErrHalfClosedSend)
		}
		c.applyEndStream(ev)
		return Deliver, nil
	case StateIdle:
		return Reject, fmt.Errorf("stream %d: %w", ev.StreamID, ErrIdleStream)
	case StateClosed:
		return Reject, fmt.Errorf("stream %d: %w", ev.StreamID, ErrClosedStream)
	}
	return Reject, fmt.Errorf("stream %d: %w", ev.StreamID, ErrReservedStream)
}

func (c *Conn) onRST(ev Event) (Verdict, error) {
	switch c.State(ev.StreamID) {
	case StateIdle:
		return Reject, fmt.Errorf("stream %d: %w", ev.StreamID, ErrIdleStream)
	case StateClosed:
		// Late RST: the peer sent it before seeing our close. Tolerate
		// it without delivering or raising an error.
		return Drop, nil
	}
	c.close(ev.StreamID)
	return Deliver, nil
}

func (c *Conn) onPushPromise(ev Event) (Verdict, error) {
	st := c.State(ev.StreamID)
	if ev.Dir == Send {
		if c.role != Server {
			return Reject, fmt.Errorf("stream %d: %w: clients cannot push",
				ev.StreamID, ErrRoleViolation)
		}
		if st != StateOpen && st != StateHalfClosedRemote {
			return Reject, fmt.Errorf("stream %d: %w: PUSH_PROMISE on %s stream",
				ev.StreamID, ErrClosedStream, st)
		}
		if err := c.checkNewID(ev.PromisedID, c.role.localParity(), &c.nextLocal); err != nil {
			return Reject, err
		}
		c.streams[ev.PromisedID] = StateReservedLocal
		return Deliver, nil
	}
	if c.role != Client {
		return Reject, fmt.Errorf("stream %d: %w: servers cannot be pushed to",
			ev.StreamID, ErrRoleViolation)
	}
	if st != StateOpen && st != StateHalfClosedLocal {
		return Reject, fmt.Errorf("stream %d: %w: PUSH_PROMISE on %s stream",
			ev.StreamID, ErrClosedStream, st)
	}
	if err := c.checkNewID(ev.PromisedID, c.role.remoteParity(), &c.nextRemote); err != nil {
		return Reject, err
	}
	c.streams[ev.PromisedID] = StateReservedRemote
	return Deliver, nil
}

// checkNewID validates a newly opened stream ID and advances the
// per-direction ID counter.
func (c *Conn) checkNewID(id uint32, parity uint32, next *uint32) error {
	if id == 0 || id&1 != parity {
		return fmt.Errorf("stream %d: %w", id, ErrStreamIDParity)
	}
	if id < *next {
		return fmt.Errorf("stream %d: %w (next is %d)", id, ErrStreamIDOrder, *next)
	}
	*next = id + 2
	return nil
}

// openStream registers a freshly opened stream, honoring an END_STREAM
// flag carried by the opening HEADERS.
func (c *Conn) openStream(id uint32, endStream bool, dir Direction) {
	st := StateOpen
	if endStream {
		if dir == Send {
			st = StateHalfClosedLocal
		} else {
			st = StateHalfClosedRemote
		}
	}
	c.streams[id] = st
}

// applyEndStream folds an END_STREAM flag into the current state.
func (c *Conn) applyEndStream(ev Event) {
	if !ev.EndStream {
		return
	}
	switch c.streams[ev.StreamID] {
	case StateOpen:
		if ev.Dir == Send {
			c.streams[ev.StreamID] = StateHalfClosedLocal
		} else {
			c.streams[ev.StreamID] = StateHalfClosedRemote
		}
	case StateHalfClosedLocal:
		if ev.Dir == Recv {
			c.close(ev.StreamID)
		}
	case StateHalfClosedRemote:
		if ev.Dir == Send {
			c.close(ev.StreamID)
		}
	}
}

// close releases the stream's resources and records a tombstone so that
// late frames are recognized as belonging to a closed stream rather than
// an idle one.
func (c *Conn) close(id uint32) {
	delete(c.streams, id)
	c.closed[id] = struct{}{}
}
