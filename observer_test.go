package main

import (
	"strings"
	"testing"
)

var testTuple = Tuple{ClientIP: "10.0.0.1", ClientPort: 1111, ServerIP: "10.0.0.2", ServerPort: 80}

func syn(id string, dir Dir, isn, epoch int) Packet {
	return Packet{ConnID: id, Tuple: testTuple, Dir: dir, Seq: isn, SYN: true, Epoch: epoch}
}

func data(id string, dir Dir, seq int, payload string, epoch int) Packet {
	return Packet{ConnID: id, Tuple: testTuple, Dir: dir, Seq: seq, Payload: []byte(payload), Epoch: epoch}
}

func fin(id string, dir Dir, seq, epoch int) Packet {
	return Packet{ConnID: id, Tuple: testTuple, Dir: dir, Seq: seq, FIN: true, Epoch: epoch}
}

func openConn(o *Observer, epoch int) {
	o.Handle(0, syn("c", C2S, 100, epoch))
	o.Handle(0, syn("c", S2C, 500, epoch))
}

func dropReasons(o *Observer) string {
	var parts []string
	for _, d := range o.Drops {
		parts = append(parts, d.Reason)
	}
	return strings.Join(parts, "; ")
}

func TestHalfCloseKeepsOtherDirectionOpen(t *testing.T) {
	o := NewObserver(10)
	openConn(o, 1)
	o.Handle(1, data("c", C2S, 101, "get", 1))
	o.Handle(2, fin("c", C2S, 104, 1)) // half-close c->s
	o.Handle(3, data("c", C2S, 104, "x", 1))
	o.Handle(4, data("c", S2C, 501, "ok", 1)) // s->c still open
	o.Handle(5, fin("c", S2C, 503, 1))
	o.Finish()

	if len(o.History()) != 1 {
		t.Fatalf("history = %d conns", len(o.History()))
	}
	c := o.History()[0]
	if c.state != stateClosed {
		t.Fatalf("state = %v", c.state)
	}
	if got := c.Streams[C2S].Render(); got != `"get"` {
		t.Fatalf("c->s = %s", got)
	}
	if got := c.Streams[S2C].Render(); got != `"ok"` {
		t.Fatalf("s->c = %s", got)
	}
	if !strings.Contains(dropReasons(o), "data beyond FIN") {
		t.Fatalf("expected data-beyond-FIN drop, got: %s", dropReasons(o))
	}
}

func TestStaleEpochCannotMixIntoNewConn(t *testing.T) {
	o := NewObserver(10)
	openConn(o, 1)
	o.Handle(1, data("c", C2S, 101, "old-bytes", 1))
	o.Handle(2, fin("c", C2S, 110, 1))
	o.Handle(3, fin("c", S2C, 501, 1)) // closed at tick 3, TIME_WAIT until 13

	o.Handle(20, syn("c", C2S, 900, 2)) // new epoch after TIME_WAIT
	o.Handle(20, syn("c", S2C, 300, 2))
	o.Handle(21, data("c", C2S, 101, "STALE", 1)) // old incarnation segment
	o.Handle(22, data("c", C2S, 901, "new", 2))
	o.Handle(23, fin("c", C2S, 904, 2))
	o.Finish()

	if len(o.History()) != 2 {
		t.Fatalf("history = %d conns", len(o.History()))
	}
	newConn := o.History()[1]
	if got := newConn.Streams[C2S].Render(); got != `"new"` {
		t.Fatalf("new epoch c->s = %s (must not contain old bytes)", got)
	}
	if !strings.Contains(dropReasons(o), "stale epoch") {
		t.Fatalf("expected stale-epoch drop, got: %s", dropReasons(o))
	}
}

func TestSynDuringTimeWaitRejected(t *testing.T) {
	o := NewObserver(10)
	openConn(o, 1)
	o.Handle(1, fin("c", C2S, 101, 1))
	o.Handle(2, fin("c", S2C, 501, 1)) // closed at 2, TIME_WAIT until 12
	o.Handle(5, syn("c", C2S, 900, 2)) // too early
	if !strings.Contains(dropReasons(o), "SYN during TIME_WAIT") {
		t.Fatalf("expected TIME_WAIT drop, got: %s", dropReasons(o))
	}
	o.Handle(12, syn("c", C2S, 900, 3)) // TIME_WAIT expired: accepted
	o.Finish()
	if len(o.History()) != 2 {
		t.Fatalf("history = %d conns", len(o.History()))
	}
	if o.History()[1].Epoch != 3 {
		t.Fatalf("new epoch = %d", o.History()[1].Epoch)
	}
}

func TestDataWithoutConnectionDropped(t *testing.T) {
	o := NewObserver(10)
	o.Handle(0, data("ghost", C2S, 1, "boo", 1))
	if !strings.Contains(dropReasons(o), "no active connection") {
		t.Fatalf("expected no-connection drop, got: %s", dropReasons(o))
	}
}

func TestRstClosesBothDirections(t *testing.T) {
	o := NewObserver(10)
	openConn(o, 1)
	o.Handle(1, data("c", C2S, 101, "partial", 1))
	o.Handle(2, Packet{ConnID: "c", Tuple: testTuple, Dir: S2C, RST: true, Epoch: 1})
	o.Handle(3, data("c", C2S, 108, "more", 1)) // after RST: TIME_WAIT
	o.Finish()

	c := o.History()[0]
	if c.state != stateClosed || !c.rst {
		t.Fatalf("state = %v rst=%v", c.state, c.rst)
	}
	if got := c.Streams[C2S].Render(); got != `"partial"` {
		t.Fatalf("c->s = %s", got)
	}
	if !strings.Contains(dropReasons(o), "TIME_WAIT") {
		t.Fatalf("expected TIME_WAIT drop, got: %s", dropReasons(o))
	}
}

func TestDirectionsHaveIndependentSeqSpaces(t *testing.T) {
	o := NewObserver(10)
	openConn(o, 1) // c->s base 101, s->c base 501
	o.Handle(1, data("c", C2S, 101, "req", 1))
	o.Handle(2, data("c", S2C, 501, "resp", 1))
	o.Finish()
	c := o.History()[0]
	if got := c.Streams[C2S].Render(); got != `"req"` {
		t.Fatalf("c->s = %s", got)
	}
	if got := c.Streams[S2C].Render(); got != `"resp"` {
		t.Fatalf("s->c = %s", got)
	}
}
