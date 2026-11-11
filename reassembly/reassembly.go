// Package reassembly reassembles segmented TCP payloads into ordered byte
// streams for a detection engine.
//
// A Pool enforces a single global memory limit on bytes buffered inside
// loss gaps across all connections. Each Conn tracks the confirmed prefix
// of one direction of a flow: bytes advance the prefix in first-arrival
// order, retransmissions and partial overlaps are folded in without
// disturbing bytes that arrived earlier. When the pool is over its limit,
// the oldest buffered segments are evicted and the affected connections
// record the exact lost interval; once the confirmed prefix reaches it,
// subscribers receive an EventGap so that bytes on opposite sides of the
// gap are never spliced into a spurious match.
package reassembly

import (
	"container/list"
	"slices"
	"sync"
)

// segment is one buffered run of out-of-order bytes.
type segment struct {
	conn  *Conn
	elem  *list.Element // position in the pool's eviction FIFO
	start uint32
	data  []byte
}

func (s *segment) end() uint32 { return s.start + uint32(len(s.data)) }

// hole is an evicted interval that will never be delivered.
type hole struct{ start, end uint32 }

// Pool shares a global memory budget for gap-buffered bytes between all
// connections created from it.
type Pool struct {
	mu       sync.Mutex
	limit    int64
	buffered int64
	fifo     *list.List // *segment, oldest at the front
	conns    map[*Conn]struct{}
}

// NewPool returns a Pool that buffers at most limitBytes of out-of-order
// payload across all of its connections.
func NewPool(limitBytes int64) *Pool {
	if limitBytes < 0 {
		limitBytes = 0
	}
	return &Pool{
		limit: limitBytes,
		fifo:  list.New(),
		conns: make(map[*Conn]struct{}),
	}
}

// Buffered reports the number of payload bytes currently held in loss gaps
// across the whole pool. It never exceeds the pool limit after any call
// returns.
func (p *Pool) Buffered() int64 {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.buffered
}

// NewConn creates a connection whose confirmed prefix starts at base (the
// sequence number of the next expected byte, e.g. ISS+1 after the SYN).
func (p *Pool) NewConn(base uint32) *Conn {
	c := &Conn{pool: p, base: base}
	c.queue.init()
	p.mu.Lock()
	p.conns[c] = struct{}{}
	p.mu.Unlock()
	go c.dispatch()
	return c
}

// evictLocked drops oldest buffered segments until the pool is back within
// its limit, recording the exact lost interval on each victim connection.
func (p *Pool) evictLocked() {
	for p.buffered > p.limit {
		front := p.fifo.Front()
		if front == nil {
			return
		}
		seg := front.Value.(*segment)
		p.fifo.Remove(front)
		p.buffered -= int64(len(seg.data))
		c := seg.conn
		c.removeSegLocked(seg)
		c.advanceLocked()
	}
}

// Conn reassembles one direction of a TCP flow. It is safe for concurrent
// use; subscribers always observe events in stream order.
type Conn struct {
	pool *Pool

	// The fields below are guarded by pool.mu.
	base   uint32     // sequence of the next undelivered byte
	segs   []*segment // sorted by start, non-overlapping, all after base
	holes  []hole     // sorted, non-overlapping lost intervals after base
	closed bool

	queue ctrlQueue
}

// Write feeds one received TCP payload into the stream. seq is the
// sequence number of payload[0]. Retransmitted or partially overlapping
// bytes are folded in: on any conflict the bytes that arrived first win.
// Bytes falling into a previously evicted (lost) interval are discarded.
func (c *Conn) Write(seq uint32, payload []byte) {
	if len(payload) == 0 {
		return
	}
	p := c.pool
	p.mu.Lock()
	defer p.mu.Unlock()
	if c.closed {
		return
	}
	segs := c.insertLocked(seq, payload)
	for _, s := range segs {
		p.buffered += int64(len(s.data))
		s.elem = p.fifo.PushBack(s)
	}
	c.advanceLocked()
	p.evictLocked()
}

// Subscribe registers a consumer and returns a channel on which stream
// events arrive in order, plus an unsubscribe function. buf is the
// channel's buffer size. The consumer must keep receiving (or call the
// unsubscribe function) so the dispatcher is never blocked; the channel is
// closed after the EventClosed event.
func (c *Conn) Subscribe(buf int) (<-chan Event, func()) {
	ch := make(chan Event, buf)
	p := c.pool
	p.mu.Lock()
	if c.closed {
		p.mu.Unlock()
		close(ch)
		return ch, func() {}
	}
	c.queue.push(ctrl{kind: ctrlSubscribe, ch: ch})
	p.mu.Unlock()
	var once sync.Once
	unsubscribe := func() {
		once.Do(func() {
			p.mu.Lock()
			defer p.mu.Unlock()
			if !c.closed {
				c.queue.push(ctrl{kind: ctrlUnsubscribe, ch: ch})
			}
		})
	}
	return ch, unsubscribe
}

// Close releases all buffered state, delivers EventClosed to subscribers
// and closes their channels.
func (c *Conn) Close() {
	p := c.pool
	p.mu.Lock()
	defer p.mu.Unlock()
	if c.closed {
		return
	}
	c.closed = true
	for _, s := range c.segs {
		p.buffered -= int64(len(s.data))
		p.fifo.Remove(s.elem)
	}
	c.segs = nil
	c.holes = nil
	c.queue.push(ctrl{kind: ctrlEvent, ev: Event{Kind: EventClosed}})
	c.queue.close()
	delete(p.conns, c)
}

// piece is a not-yet-buffered run of new payload bytes.
type piece struct {
	start uint32
	data  []byte
}

// subtract removes the interval [s, e) from every piece, splitting pieces
// as needed. Interval arithmetic uses serial comparisons so it is correct
// across the 32-bit wrap boundary.
func subtract(pieces []piece, s, e uint32) []piece {
	if !seqLess(s, e) {
		return pieces
	}
	out := make([]piece, 0, len(pieces))
	for _, pc := range pieces {
		pend := pc.start + uint32(len(pc.data))
		if !seqLess(pc.start, e) || !seqLess(s, pend) {
			out = append(out, pc) // no overlap
			continue
		}
		if seqLess(pc.start, s) {
			n := s - pc.start
			out = append(out, piece{start: pc.start, data: pc.data[:n]})
		}
		if seqLess(e, pend) {
			off := e - pc.start
			out = append(out, piece{start: e, data: pc.data[off:]})
		}
	}
	return out
}

// insertLocked folds payload into the reassembly buffer and returns the
// newly created segments. Bytes before the confirmed prefix, inside lost
// intervals, or already buffered are dropped, so first-arrival bytes are
// never disturbed.
func (c *Conn) insertLocked(seq uint32, payload []byte) []*segment {
	if seqLess(seq, c.base) {
		cut := c.base - seq
		if uint32(len(payload)) <= cut {
			return nil
		}
		payload = payload[cut:]
		seq = c.base
	}
	pieces := []piece{{start: seq, data: payload}}
	for _, h := range c.holes {
		pieces = subtract(pieces, h.start, h.end)
	}
	for _, s := range c.segs {
		pieces = subtract(pieces, s.start, s.end())
	}
	if len(pieces) == 0 {
		return nil
	}
	out := make([]*segment, 0, len(pieces))
	for _, pc := range pieces {
		seg := &segment{conn: c, start: pc.start, data: pc.data}
		out = append(out, seg)
		c.segs = append(c.segs, seg)
	}
	slices.SortFunc(c.segs, func(a, b *segment) int {
		switch {
		case a.start == b.start:
			return 0
		case seqLess(a.start, b.start):
			return -1
		default:
			return 1
		}
	})
	return out
}

// removeSegLocked drops a buffered segment (eviction) and records its exact
// extent as a lost interval.
func (c *Conn) removeSegLocked(seg *segment) {
	for i, s := range c.segs {
		if s == seg {
			c.segs = slices.Delete(c.segs, i, i+1)
			break
		}
	}
	c.addHoleLocked(seg.start, seg.end())
}

func (c *Conn) addHoleLocked(start, end uint32) {
	c.holes = append(c.holes, hole{start: start, end: end})
	slices.SortFunc(c.holes, func(a, b hole) int {
		switch {
		case a.start == b.start:
			return 0
		case seqLess(a.start, b.start):
			return -1
		default:
			return 1
		}
	})
	merged := c.holes[:0]
	for _, h := range c.holes {
		if n := len(merged); n > 0 && seqLEQ(h.start, merged[n-1].end) {
			if seqLess(merged[n-1].end, h.end) {
				merged[n-1].end = h.end
			}
			continue
		}
		merged = append(merged, h)
	}
	c.holes = merged
}

// advanceLocked moves the confirmed prefix forward over any contiguous
// buffered bytes and lost intervals, emitting EventData / EventGap to
// subscribers. A gap is always reported as its own event, so data from
// opposite sides of a loss is never concatenated into one delivery.
func (c *Conn) advanceLocked() {
	p := c.pool
	for {
		if len(c.holes) > 0 && !seqLess(c.base, c.holes[0].start) {
			h := c.holes[0]
			c.holes = c.holes[1:]
			if !seqLess(c.base, h.end) {
				continue // hole entirely behind the prefix
			}
			c.queue.push(ctrl{kind: ctrlEvent, ev: Event{Kind: EventGap, Seq: c.base, End: h.end}})
			c.base = h.end
			continue
		}
		if len(c.segs) > 0 && c.segs[0].start == c.base {
			var buf []byte
			end := c.base
			n := 0
			for n < len(c.segs) && c.segs[n].start == end {
				buf = append(buf, c.segs[n].data...)
				end += uint32(len(c.segs[n].data))
				p.buffered -= int64(len(c.segs[n].data))
				p.fifo.Remove(c.segs[n].elem)
				n++
			}
			c.segs = slices.Delete(c.segs, 0, n)
			c.queue.push(ctrl{kind: ctrlEvent, ev: Event{Kind: EventData, Seq: c.base, Data: buf}})
			c.base = end
			continue
		}
		return
	}
}

// dispatch is the per-connection event loop. It is the only sender on
// subscriber channels, which keeps per-connection ordering strict and
// writers non-blocking.
func (c *Conn) dispatch() {
	subs := make(map[chan Event]struct{})
	for {
		m, ok := c.queue.pop()
		if !ok {
			break
		}
		switch m.kind {
		case ctrlEvent:
			for ch := range subs {
				ch <- m.ev
			}
		case ctrlSubscribe:
			subs[m.ch] = struct{}{}
		case ctrlUnsubscribe:
			if _, ok := subs[m.ch]; ok {
				delete(subs, m.ch)
				close(m.ch)
			}
		}
	}
	for ch := range subs {
		close(ch)
	}
}
