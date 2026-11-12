package netsec

import (
	"errors"
	"testing"
)

func ev(gen uint64, dir Direction, typ FrameType, id uint32) Event {
	return Event{Generation: gen, Dir: dir, Type: typ, StreamID: id}
}

func es(ev Event) Event { ev.EndStream = true; return ev }

func mustDeliver(t *testing.T, c *Conn, e Event) {
	t.Helper()
	if v, err := c.Dispatch(e); v != Deliver || err != nil {
		t.Fatalf("Dispatch(%v %v stream %d) = %v, %v; want deliver, nil",
			e.Dir, e.Type, e.StreamID, v, err)
	}
}

func mustReject(t *testing.T, c *Conn, e Event, want error) {
	t.Helper()
	v, err := c.Dispatch(e)
	if v != Reject {
		t.Fatalf("Dispatch(%v %v stream %d) verdict = %v; want reject",
			e.Dir, e.Type, e.StreamID, v)
	}
	if !errors.Is(err, want) {
		t.Fatalf("Dispatch(%v %v stream %d) err = %v; want %v",
			e.Dir, e.Type, e.StreamID, err, want)
	}
}

// The proxy must reject data sent on an already half-closed direction,
// in both directions.
func TestHalfClosedDirectionRejectsData(t *testing.T) {
	c := NewConn(Client)

	// Local half-close: we sent END_STREAM, so we must not send more.
	mustDeliver(t, c, ev(0, Send, FrameHeaders, 1))
	mustDeliver(t, c, es(ev(0, Send, FrameData, 1)))
	if got := c.State(1); got != StateHalfClosedLocal {
		t.Fatalf("state = %v; want %v", got, StateHalfClosedLocal)
	}
	mustReject(t, c, ev(0, Send, FrameData, 1), ErrHalfClosedSend)
	mustReject(t, c, ev(0, Send, FrameHeaders, 1), ErrHalfClosedSend)

	// Peer finishes its side; stream closes and resources are released.
	mustDeliver(t, c, ev(0, Recv, FrameHeaders, 1))
	mustDeliver(t, c, es(ev(0, Recv, FrameData, 1)))
	if got := c.State(1); got != StateClosed {
		t.Fatalf("state = %v; want %v", got, StateClosed)
	}
	if n := c.LiveStreams(); n != 0 {
		t.Fatalf("LiveStreams = %d; want 0", n)
	}

	// Remote half-close: peer sent END_STREAM, so peer must not send more.
	mustDeliver(t, c, ev(0, Send, FrameHeaders, 3))
	mustDeliver(t, c, es(ev(0, Recv, FrameData, 3)))
	if got := c.State(3); got != StateHalfClosedRemote {
		t.Fatalf("state = %v; want %v", got, StateHalfClosedRemote)
	}
	mustReject(t, c, ev(0, Recv, FrameData, 3), ErrHalfClosedRecv)
	mustReject(t, c, ev(0, Recv, FrameHeaders, 3), ErrHalfClosedRecv)

	// Our side may still finish, closing the stream.
	mustDeliver(t, c, es(ev(0, Send, FrameData, 3)))
	if got := c.State(3); got != StateClosed {
		t.Fatalf("state = %v; want %v", got, StateClosed)
	}
}

// Simultaneous close: both sides send END_STREAM without having seen the
// peer's. Both interleavings must reach closed and release resources.
func TestSimultaneousClose(t *testing.T) {
	orders := []struct {
		name  string
		first Direction
	}{
		{"send-then-recv", Send},
		{"recv-then-send", Recv},
	}
	for _, o := range orders {
		t.Run(o.name, func(t *testing.T) {
			c := NewConn(Client)
			mustDeliver(t, c, ev(0, Send, FrameHeaders, 1))
			mustDeliver(t, c, es(ev(0, o.first, FrameData, 1)))
			other := Recv
			if o.first == Recv {
				other = Send
			}
			mustDeliver(t, c, es(ev(0, other, FrameData, 1)))
			if got := c.State(1); got != StateClosed {
				t.Fatalf("state = %v; want %v", got, StateClosed)
			}
			if n := c.LiveStreams(); n != 0 {
				t.Fatalf("LiveStreams = %d; want 0", n)
			}
		})
	}
}

// Reserved stream activation: a pushed stream goes reserved -> half-closed
// when the pushing side sends HEADERS, from both endpoints' perspectives.
func TestReservedStreamActivation(t *testing.T) {
	t.Run("server-sends-push", func(t *testing.T) {
		c := NewConn(Server)
		mustDeliver(t, c, es(ev(0, Recv, FrameHeaders, 1))) // client request
		pp := ev(0, Send, FramePushPromise, 1)
		pp.PromisedID = 2
		mustDeliver(t, c, pp)
		if got := c.State(2); got != StateReservedLocal {
			t.Fatalf("state = %v; want %v", got, StateReservedLocal)
		}
		// DATA on a reserved stream is illegal.
		mustReject(t, c, ev(0, Send, FrameData, 2), ErrReservedStream)
		// Activation: server HEADERS open the promised stream.
		mustDeliver(t, c, ev(0, Send, FrameHeaders, 2))
		if got := c.State(2); got != StateHalfClosedRemote {
			t.Fatalf("state = %v; want %v", got, StateHalfClosedRemote)
		}
		mustDeliver(t, c, es(ev(0, Send, FrameData, 2)))
		if got := c.State(2); got != StateClosed {
			t.Fatalf("state = %v; want %v", got, StateClosed)
		}
	})

	t.Run("client-receives-push", func(t *testing.T) {
		c := NewConn(Client)
		mustDeliver(t, c, es(ev(0, Send, FrameHeaders, 1)))
		pp := ev(0, Recv, FramePushPromise, 1)
		pp.PromisedID = 2
		mustDeliver(t, c, pp)
		if got := c.State(2); got != StateReservedRemote {
			t.Fatalf("state = %v; want %v", got, StateReservedRemote)
		}
		mustReject(t, c, ev(0, Recv, FrameData, 2), ErrReservedStream)
		mustDeliver(t, c, ev(0, Recv, FrameHeaders, 2))
		if got := c.State(2); got != StateHalfClosedLocal {
			t.Fatalf("state = %v; want %v", got, StateHalfClosedLocal)
		}
		// A pushed stream is receive-only for the client.
		mustReject(t, c, ev(0, Send, FrameData, 2), ErrHalfClosedSend)
		mustDeliver(t, c, es(ev(0, Recv, FrameData, 2)))
		if got := c.State(2); got != StateClosed {
			t.Fatalf("state = %v; want %v", got, StateClosed)
		}
	})
}

// Late RST: both sides reset at the same time; the second RST finds the
// stream already closed and must be dropped silently, while other frames
// on a closed stream are rejected.
func TestLateRST(t *testing.T) {
	c := NewConn(Client)
	mustDeliver(t, c, ev(0, Send, FrameHeaders, 1))
	mustDeliver(t, c, ev(0, Send, FrameRSTStream, 1))
	if got := c.State(1); got != StateClosed {
		t.Fatalf("state = %v; want %v", got, StateClosed)
	}
	if n := c.LiveStreams(); n != 0 {
		t.Fatalf("LiveStreams = %d; want 0", n)
	}

	v, err := c.Dispatch(ev(0, Recv, FrameRSTStream, 1))
	if v != Drop || err != nil {
		t.Fatalf("late RST = %v, %v; want drop, nil", v, err)
	}
	if c.FirstError() != nil {
		t.Fatalf("FirstError = %v; want nil after tolerated late RST", c.FirstError())
	}

	mustReject(t, c, ev(0, Recv, FrameData, 1), ErrClosedStream)
	mustReject(t, c, ev(0, Recv, FrameRSTStream, 99), ErrIdleStream)
}

// Connection generation reuse: after Reset, old-generation frames are
// rejected, resources are released, and stream IDs may be reused.
func TestConnectionGenerationReuse(t *testing.T) {
	c := NewConn(Client)
	mustDeliver(t, c, ev(0, Send, FrameHeaders, 1))
	mustDeliver(t, c, ev(0, Send, FrameHeaders, 3))
	mustDeliver(t, c, ev(0, Send, FrameRSTStream, 3))
	if n := c.LiveStreams(); n != 1 {
		t.Fatalf("LiveStreams = %d; want 1", n)
	}

	gen := c.Reset()
	if gen != 1 || c.Generation() != 1 {
		t.Fatalf("generation = %d; want 1", gen)
	}
	if n := c.LiveStreams(); n != 0 {
		t.Fatalf("LiveStreams after Reset = %d; want 0", n)
	}
	if got := c.State(1); got != StateIdle {
		t.Fatalf("State(1) after Reset = %v; want idle", got)
	}

	// Straggler from the old connection incarnation.
	mustReject(t, c, ev(0, Recv, FrameData, 1), ErrStaleGeneration)

	// The new generation reuses stream ID 1.
	mustDeliver(t, c, ev(1, Send, FrameHeaders, 1))
	if got := c.State(1); got != StateOpen {
		t.Fatalf("state = %v; want %v", got, StateOpen)
	}
}

// The first state violation is recorded and never overwritten.
func TestFirstErrorRetained(t *testing.T) {
	c := NewConn(Client)
	mustReject(t, c, ev(0, Recv, FrameData, 5), ErrIdleStream)
	mustReject(t, c, ev(0, Send, FrameData, 7), ErrIdleStream)
	first := c.FirstError()
	if !errors.Is(first, ErrIdleStream) {
		t.Fatalf("FirstError = %v; want ErrIdleStream", first)
	}
	// A later, different error must not replace the first one.
	mustDeliver(t, c, ev(0, Send, FrameHeaders, 1))
	mustDeliver(t, c, es(ev(0, Send, FrameData, 1)))
	mustReject(t, c, ev(0, Send, FrameData, 1), ErrHalfClosedSend)
	if c.FirstError() != first {
		t.Fatalf("FirstError changed: got %v, want %v", c.FirstError(), first)
	}
}

// Stream IDs follow the initiator's parity and increase by two.
func TestStreamIDParityAndOrder(t *testing.T) {
	c := NewConn(Client)
	for i, want := range []uint32{1, 3, 5} {
		if got := c.NextLocalStreamID(); got != want {
			t.Fatalf("step %d: NextLocalStreamID = %d; want %d", i, got, want)
		}
		mustDeliver(t, c, ev(0, Send, FrameHeaders, want))
	}
	// Wrong parity for a locally opened stream.
	mustReject(t, c, ev(0, Send, FrameHeaders, 8), ErrStreamIDParity)
	// Stream 0 is connection-level, never a stream.
	mustReject(t, c, ev(0, Send, FrameHeaders, 0), ErrStreamIDParity)

	// IDs must not go backwards: after skipping ahead to 5, opening 1
	// is an idle-but-regressed ID.
	c2 := NewConn(Client)
	mustDeliver(t, c2, ev(0, Send, FrameHeaders, 5))
	mustReject(t, c2, ev(0, Send, FrameHeaders, 1), ErrStreamIDOrder)

	s := NewConn(Server)
	if got := s.NextLocalStreamID(); got != 2 {
		t.Fatalf("server NextLocalStreamID = %d; want 2", got)
	}
	mustDeliver(t, s, ev(0, Recv, FrameHeaders, 1))
	mustReject(t, s, ev(0, Recv, FrameHeaders, 2), ErrStreamIDParity)
}

// Role rules: only clients open streams with HEADERS, only servers push.
func TestRoleViolations(t *testing.T) {
	s := NewConn(Server)
	mustReject(t, s, ev(0, Send, FrameHeaders, 2), ErrRoleViolation)

	c := NewConn(Client)
	mustReject(t, c, ev(0, Recv, FrameHeaders, 2), ErrRoleViolation)
	mustDeliver(t, c, ev(0, Send, FrameHeaders, 1))
	pp := ev(0, Send, FramePushPromise, 1)
	pp.PromisedID = 4
	mustReject(t, c, pp, ErrRoleViolation)
}

// Host-side gating: only Deliver frames may reach the business callback.
func TestBusinessCallbackGating(t *testing.T) {
	c := NewConn(Client)
	callbacks := 0
	host := func(e Event) {
		if v, _ := c.Dispatch(e); v == Deliver {
			callbacks++
		}
	}

	host(ev(0, Send, FrameHeaders, 1))    // deliver
	host(es(ev(0, Recv, FrameData, 1)))   // deliver, remote half-closes
	host(ev(0, Recv, FrameData, 1))       // reject: data on half-closed direction
	host(es(ev(0, Send, FrameData, 1)))   // deliver, closes
	host(ev(0, Recv, FrameRSTStream, 1))  // drop: late RST
	host(ev(0, Recv, FrameData, 1))       // reject: closed
	host(ev(0, Send, FrameRSTStream, 42)) // reject: idle

	if callbacks != 3 {
		t.Fatalf("business callback invoked %d times; want 3", callbacks)
	}
	if c.FirstError() == nil {
		t.Fatal("FirstError = nil; want the first rejection recorded")
	}
	if n := c.LiveStreams(); n != 0 {
		t.Fatalf("LiveStreams = %d; want 0", n)
	}
}
