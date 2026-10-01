package main

import (
	"fmt"
	"sort"
)

type connState int

const (
	stateOpen    connState = iota
	stateHalfC2S           // c->s sent FIN, s->c still open
	stateHalfS2C           // s->c sent FIN, c->s still open
	stateClosed            // both directions FIN, or RST: in TIME_WAIT
)

// Conn is one epoch (incarnation) of a 4-tuple connection.
type Conn struct {
	ID       string
	Tuple    Tuple
	Epoch    int
	Index    int // creation order, for deterministic reports
	Streams  [2]*Stream
	state    connState
	rst      bool
	closedAt int // tick at which it entered stateClosed
}

func (c *Conn) stateString() string {
	switch c.state {
	case stateOpen:
		return "open"
	case stateHalfC2S:
		return "half-closed (c->s closed by FIN)"
	case stateHalfS2C:
		return "half-closed (s->c closed by FIN)"
	case stateClosed:
		if c.rst {
			return "closed (RST)"
		}
		return "closed (both directions FIN)"
	}
	return "unknown"
}

// Drop records a packet the observer refused, with the reason.
type Drop struct {
	Tick   int
	Packet Packet
	Reason string
}

// Observer is the passive reassembler. It keys connections by 4-tuple,
// keeps the two directions in independent sequence spaces, enforces epoch
// isolation, and holds closed connections in TIME_WAIT before reuse.
type Observer struct {
	TimeWaitTicks int
	active        map[Tuple]*Conn
	history       []*Conn
	Drops         []Drop
	connSeq       int
}

func NewObserver(timeWait int) *Observer {
	return &Observer{TimeWaitTicks: timeWait, active: make(map[Tuple]*Conn)}
}

// expire moves connections whose TIME_WAIT has elapsed into history,
// freeing the 4-tuple for a new epoch.
func (o *Observer) expire(tick int) {
	for k, c := range o.active {
		if c.state == stateClosed && tick-c.closedAt >= o.TimeWaitTicks {
			o.history = append(o.history, c)
			delete(o.active, k)
		}
	}
}

// Handle processes one packet observed at the given integer tick.
func (o *Observer) Handle(tick int, p Packet) {
	o.expire(tick)
	c := o.active[p.Tuple]

	// A client SYN opens a new epoch of the 4-tuple.
	if p.SYN && p.Dir == C2S {
		if c != nil {
			reason := "duplicate SYN (ignored)"
			if c.state == stateClosed {
				reason = "SYN during TIME_WAIT"
			}
			o.Drops = append(o.Drops, Drop{tick, p, reason})
			return
		}
		o.connSeq++
		nc := &Conn{
			ID:      p.ConnID,
			Tuple:   p.Tuple,
			Epoch:   p.Epoch,
			Index:   o.connSeq,
			Streams: [2]*Stream{{}, {}},
		}
		nc.Streams[C2S].SetBase(p.Seq + 1)
		o.active[p.Tuple] = nc
		return
	}

	if c == nil {
		o.Drops = append(o.Drops, Drop{tick, p, "no active connection"})
		return
	}
	// Epoch guard: segments from an old incarnation of this 4-tuple must
	// never mix into the current connection.
	if p.Epoch != c.Epoch {
		o.Drops = append(o.Drops, Drop{tick, p,
			fmt.Sprintf("stale epoch (packet=%d conn=%d)", p.Epoch, c.Epoch)})
		return
	}
	if c.state == stateClosed {
		o.Drops = append(o.Drops, Drop{tick, p, "connection in TIME_WAIT"})
		return
	}

	if p.SYN { // server side SYN(-ACK): establish the s->c base
		c.Streams[S2C].SetBase(p.Seq + 1)
		return
	}
	if p.RST {
		c.state = stateClosed
		c.rst = true
		c.closedAt = tick
		return
	}

	st := c.Streams[p.Dir]
	payload := p.Payload
	if st.hasFin && p.Seq+len(payload) > st.finSeq {
		if p.Seq >= st.finSeq {
			if len(payload) > 0 {
				o.Drops = append(o.Drops, Drop{tick, p, "data beyond FIN"})
			}
			payload = nil
		} else {
			o.Drops = append(o.Drops, Drop{tick, p, "data truncated at FIN"})
			payload = payload[:st.finSeq-p.Seq]
		}
	}
	if len(payload) > 0 {
		st.Insert(p.Seq, payload)
	}
	if p.FIN {
		st.SetFin(p.Seq)
		switch {
		case p.Dir == C2S && c.state == stateOpen:
			c.state = stateHalfC2S
		case p.Dir == S2C && c.state == stateOpen:
			c.state = stateHalfS2C
		case p.Dir == C2S && c.state == stateHalfS2C,
			p.Dir == S2C && c.state == stateHalfC2S:
			c.state = stateClosed
			c.closedAt = tick
		}
	}
}

// Finish archives all remaining connections so the report is complete.
func (o *Observer) Finish() {
	for k, c := range o.active {
		o.history = append(o.history, c)
		delete(o.active, k)
	}
	sort.Slice(o.history, func(i, j int) bool {
		return o.history[i].Index < o.history[j].Index
	})
}

// History returns all finalized connections in creation order.
func (o *Observer) History() []*Conn { return o.history }
