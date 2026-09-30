package streamsim

import (
	"fmt"
	"sort"
)

// Conflict records a byte position where an overlapping segment carried a
// different byte than the one already accepted. The first-seen byte wins;
// the conflict is reported, never silently merged.
type Conflict struct {
	Offset   uint64 // stream-relative offset
	Existing byte
	Incoming byte
}

// Gap is a half-open range [Start, End) of stream-relative offsets for
// which no data was ever observed.
type Gap struct {
	Start uint64
	End   uint64
}

func (g Gap) String() string { return fmt.Sprintf("GAP[%d:%d]", g.Start, g.End) }

// Chunk is one item of finalized stream output: either a Gap or Data.
type Chunk struct {
	Gap  *Gap
	Data []byte
}

// segment is a non-overlapping run of received bytes at stream offset off.
type segment struct {
	off  uint64
	data []byte
}

func (s segment) end() uint64 { return s.off + uint64(len(s.data)) }

// stream is one direction of a connection: an independent sequence space.
type stream struct {
	base      uint64 // absolute sequence number of stream offset 0
	started   bool
	segs      []segment // sorted by off, non-overlapping
	next      uint64    // next offset to deliver
	fin       *uint64   // offset of FIN (== end of data), once seen
	conflicts []Conflict
	afterFin  uint64 // payload bytes dropped because they fell beyond FIN
}

// begin anchors the sequence space. SYN consumes one sequence number, so
// data begins at isn+1.
func (st *stream) begin(base uint64) {
	st.base = base
	st.started = true
}

// add inserts payload at stream offset off. Overlapping identical bytes are
// retransmissions; overlapping differing bytes are recorded as conflicts and
// the first-seen bytes are kept.
func (st *stream) add(off uint64, payload []byte) {
	if len(payload) == 0 {
		return
	}
	end := off + uint64(len(payload))
	if st.fin != nil && end > *st.fin {
		// Bytes beyond FIN are protocol violations: drop them.
		cut := end - *st.fin
		if off >= *st.fin {
			st.afterFin += uint64(len(payload))
			return
		}
		st.afterFin += cut
		payload = payload[:len(payload)-int(cut)]
		end = *st.fin
	}
	if end <= st.next {
		return // already delivered
	}
	if off < st.next {
		payload = payload[st.next-off:]
		off = st.next
	}

	// First segment whose end is past off: overlaps start here at the
	// earliest.
	i := sort.Search(len(st.segs), func(i int) bool { return st.segs[i].end() > off })

	// Check overlaps against existing segments; first-seen bytes win.
	for j := i; j < len(st.segs) && st.segs[j].off < end; j++ {
		ex := st.segs[j]
		lo := max(off, ex.off)
		hi := min(end, ex.end())
		for p := lo; p < hi; p++ {
			got := payload[p-off]
			have := ex.data[p-ex.off]
			if got != have {
				st.conflicts = append(st.conflicts, Conflict{Offset: p, Existing: have, Incoming: got})
			}
		}
	}

	// Insert only the parts of payload not already covered.
	type piece struct{ off, end uint64 }
	var pieces []piece
	cursor := off
	for j := i; j < len(st.segs) && st.segs[j].off < end; j++ {
		ex := st.segs[j]
		if ex.off > cursor {
			pieces = append(pieces, piece{cursor, min(ex.off, end)})
		}
		cursor = max(cursor, ex.end())
	}
	if cursor < end {
		pieces = append(pieces, piece{cursor, end})
	}
	for _, pc := range pieces {
		seg := segment{off: pc.off, data: append([]byte(nil), payload[pc.off-off:pc.end-off]...)}
		pos := sort.Search(len(st.segs), func(k int) bool { return st.segs[k].off > seg.off })
		st.segs = append(st.segs, segment{})
		copy(st.segs[pos+1:], st.segs[pos:])
		st.segs[pos] = seg
	}
	st.merge()
}

// merge coalesces adjacent segments.
func (st *stream) merge() {
	out := st.segs[:0]
	for _, s := range st.segs {
		if n := len(out); n > 0 && out[n-1].end() == s.off {
			out[n-1].data = append(out[n-1].data, s.data...)
			continue
		}
		out = append(out, s)
	}
	st.segs = out
}

// deliver returns the contiguous bytes starting at the delivery cursor and
// advances it. It returns nil while a gap sits at the cursor: gaps are never
// filled with fabricated data.
func (st *stream) deliver() []byte {
	var out []byte
	for len(st.segs) > 0 && st.segs[0].off <= st.next {
		s0 := st.segs[0]
		if s0.end() > st.next {
			out = append(out, s0.data[st.next-s0.off:]...)
			st.next = s0.end()
		}
		st.segs = st.segs[1:]
	}
	return out
}

// finalize reports the remaining stream content as an ordered list of data
// chunks and gaps, from the delivery cursor up to fin (or the last received
// byte if no FIN was seen). Missing ranges are reported as Gap chunks; no
// bytes are invented.
func (st *stream) finalize() []Chunk {
	limit := uint64(0)
	if st.fin != nil {
		limit = *st.fin
	}
	for _, s := range st.segs {
		limit = max(limit, s.end())
	}
	var chunks []Chunk
	cursor := st.next
	for _, s := range st.segs {
		if s.end() <= cursor {
			continue
		}
		if s.off > cursor {
			chunks = append(chunks, Chunk{Gap: &Gap{Start: cursor, End: min(s.off, limit)}})
		}
		from := max(cursor, s.off)
		chunks = append(chunks, Chunk{Data: append([]byte(nil), s.data[from-s.off:]...)})
		cursor = s.end()
	}
	if cursor < limit {
		chunks = append(chunks, Chunk{Gap: &Gap{Start: cursor, End: limit}})
	}
	return chunks
}

// Conn is one connection incarnation: a 4-tuple plus an epoch.
type Conn struct {
	Tuple Tuple // client->server orientation
	Epoch uint32
	dir   [2]stream
}

func (c *Conn) handle(p Packet) {
	var d Direction
	switch p.Tuple {
	case c.Tuple:
		d = ClientToServer
	case c.Tuple.Reverse():
		d = ServerToClient
	default:
		return
	}
	st := &c.dir[d]
	if !st.started {
		if p.Syn {
			st.begin(uint64(p.Seq) + 1)
		} else {
			st.begin(uint64(p.Seq))
		}
	}
	off := uint64(p.Seq) - st.base
	if len(p.Data) > 0 {
		st.add(off, p.Data)
	}
	if p.Fin && st.fin == nil {
		fin := off + uint64(len(p.Data))
		st.fin = &fin
	}
}

// Deliver returns newly contiguous bytes for direction d, or nil if a gap
// (or no data) sits at the delivery cursor.
func (c *Conn) Deliver(d Direction) []byte { return c.dir[d].deliver() }

// Finalize reports remaining data and gaps for direction d.
func (c *Conn) Finalize(d Direction) []Chunk { return c.dir[d].finalize() }

// Conflicts lists overlap conflicts recorded for direction d.
func (c *Conn) Conflicts(d Direction) []Conflict { return c.dir[d].conflicts }

// FinReceived reports whether a FIN was seen in direction d (half-close
// state: the other direction may still carry data).
func (c *Conn) FinReceived(d Direction) bool { return c.dir[d].fin != nil }

// DataAfterFin counts payload bytes dropped for arriving beyond FIN.
func (c *Conn) DataAfterFin(d Direction) uint64 { return c.dir[d].afterFin }

// Closed reports whether both directions have seen FIN.
func (c *Conn) Closed() bool { return c.FinReceived(ClientToServer) && c.FinReceived(ServerToClient) }

type connKey struct {
	flow  string
	epoch uint32
}

// Table tracks all connection incarnations and routes packets to the right
// one. A SYN carrying a newer epoch starts a fresh connection; packets from
// older epochs are dropped as stale and can never mix into the new stream.
type Table struct {
	conns   map[connKey]*Conn
	current map[string]uint32 // flowKey -> current epoch

	StaleDrops int // packets from an older epoch than the current connection
}

func NewTable() *Table {
	return &Table{conns: make(map[connKey]*Conn), current: make(map[string]uint32)}
}

// Handle feeds a packet into the table. It returns the connection the
// packet was applied to, or nil if the packet was dropped as stale.
func (t *Table) Handle(p Packet) *Conn {
	flow := p.Tuple.flowKey()
	cur, known := t.current[flow]
	if !known {
		cur = p.Epoch
		t.current[flow] = cur
	}
	if p.Syn && p.Epoch > cur {
		// New incarnation of the same 4-tuple.
		cur = p.Epoch
		t.current[flow] = cur
	}
	if p.Epoch != cur {
		t.StaleDrops++
		return nil
	}
	key := connKey{flow: flow, epoch: cur}
	c := t.conns[key]
	if c == nil {
		c = &Conn{Tuple: p.Tuple, Epoch: cur}
		t.conns[key] = c
	}
	c.handle(p)
	return c
}

// Conns returns all known connections sorted by epoch then tuple.
func (t *Table) Conns() []*Conn {
	out := make([]*Conn, 0, len(t.conns))
	for _, c := range t.conns {
		out = append(out, c)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Epoch != out[j].Epoch {
			return out[i].Epoch < out[j].Epoch
		}
		return out[i].Tuple.String() < out[j].Tuple.String()
	})
	return out
}
