package sim

import "math/big"

// Segment is a data packet carrying bytes [Seq, Seq+Len).
type Segment struct {
	Seq *big.Int
	Len *big.Int
}

// AckPacket carries a cumulative ACK plus SACK blocks.
type AckPacket struct {
	Ack   *big.Int
	SACKs []Interval
}

// SentRecord is one emitted segment, tagged as first transmission or retx.
type SentRecord struct {
	Seg  Segment
	Retx bool
}

// Sender tracks sent byte intervals and the SACK set, and retransmits only
// the gaps. All sequence numbers are extended integers (big.Int).
type Sender struct {
	q      *Queue
	toPeer *Link
	log    *Logger

	streamEnd *big.Int // absolute end sequence number (isn + streamLen)
	mss       *big.Int
	window    *big.Int // advertised receiver window in bytes; may shrink
	rto       int64    // retransmission timeout in ticks

	sndUna *big.Int    // oldest unacknowledged byte
	sndNxt *big.Int    // next byte to send for the first time
	sacked IntervalSet // union of SACKed bytes above sndUna
	retx   IntervalSet // retransmitted bytes still in flight

	timerSet bool

	// OnData is the delivery callback into the peer (set by the caller).
	OnData func(Segment)

	// Stats / observability.
	RejectedAcks  int          // ACKs for never-sent bytes
	Retransmitted *big.Int     // total retransmitted bytes
	SentTotal     *big.Int     // total bytes put on the wire
	SentSegs      []SentRecord // full emission log, in order
}

func NewSender(q *Queue, link *Link, isn, streamLen, mss, window *big.Int, rto int64, log *Logger) *Sender {
	return &Sender{
		q:             q,
		toPeer:        link,
		log:           log,
		streamEnd:     new(big.Int).Add(isn, streamLen),
		mss:           new(big.Int).Set(mss),
		window:        new(big.Int).Set(window),
		rto:           rto,
		sndUna:        new(big.Int).Set(isn),
		sndNxt:        new(big.Int).Set(isn),
		Retransmitted: new(big.Int),
		SentTotal:     new(big.Int),
	}
}

// SndUna returns a copy of the oldest unacknowledged sequence number.
func (s *Sender) SndUna() *big.Int { return new(big.Int).Set(s.sndUna) }

// SndNxt returns a copy of the next never-sent sequence number.
func (s *Sender) SndNxt() *big.Int { return new(big.Int).Set(s.sndNxt) }

// SackedBytes returns the number of bytes in the SACK union (overlapping
// blocks counted once).
func (s *Sender) SackedBytes() *big.Int { return s.sacked.Measure() }

// SetWindow updates the send window; shrinking takes effect immediately and
// clamps both new transmissions and the retransmission range.
func (s *Sender) SetWindow(w *big.Int) {
	s.log.Log(s.q.Now(), "WINDOW %s -> %s", s.window, w)
	s.window = new(big.Int).Set(w)
}

// Start begins the transfer.
func (s *Sender) Start() {
	s.maybeSendNew()
	s.armTimer()
}

func (s *Sender) emit(seq, n *big.Int, isRetx bool) {
	seg := Segment{Seq: new(big.Int).Set(seq), Len: new(big.Int).Set(n)}
	s.SentSegs = append(s.SentSegs, SentRecord{Seg: seg, Retx: isRetx})
	s.SentTotal.Add(s.SentTotal, n)
	kind := "TX"
	if isRetx {
		kind = "TX-RETX"
		s.Retransmitted.Add(s.Retransmitted, n)
	}
	end := new(big.Int).Add(seg.Seq, seg.Len)
	s.log.Log(s.q.Now(), "%s seg[%s,%s)", kind, seg.Seq, end)
	s.toPeer.Transmit(func() {
		if s.OnData != nil {
			s.OnData(seg)
		}
	})
}

// emitChunks splits a gap into MSS-sized retransmission segments.
func (s *Sender) emitChunks(iv Interval) {
	for lo := new(big.Int).Set(iv.Lo); lo.Cmp(iv.Hi) < 0; {
		n := new(big.Int).Set(s.mss)
		if rem := new(big.Int).Sub(iv.Hi, lo); rem.Cmp(n) < 0 {
			n = rem
		}
		s.emit(lo, n, true)
		lo.Add(lo, n)
	}
}

// maybeSendNew fills the window with never-sent bytes.
func (s *Sender) maybeSendNew() {
	limit := new(big.Int).Add(s.sndUna, s.window)
	if limit.Cmp(s.streamEnd) > 0 {
		limit.Set(s.streamEnd)
	}
	sent := false
	for s.sndNxt.Cmp(limit) < 0 {
		n := new(big.Int).Set(s.mss)
		if rem := new(big.Int).Sub(s.streamEnd, s.sndNxt); rem.Cmp(n) < 0 {
			n = rem
		}
		s.emit(s.sndNxt, n, false)
		s.sndNxt.Add(s.sndNxt, n)
		sent = true
	}
	if sent {
		s.armTimer()
	}
}

// OnAck processes an incoming ACK packet. It validates the cumulative ACK,
// merges SACK blocks into the SACK set, and retransmits only the gaps.
func (s *Sender) OnAck(p AckPacket) {
	// Reject ACKs for bytes that were never sent.
	if p.Ack.Cmp(s.sndNxt) > 0 {
		s.RejectedAcks++
		s.log.Log(s.q.Now(), "REJECT ack=%s (unsent byte, sndNxt=%s)", p.Ack, s.sndNxt)
		return
	}
	if p.Ack.Cmp(s.sndUna) > 0 {
		s.log.Log(s.q.Now(), "ACK una %s -> %s", s.sndUna, p.Ack)
		s.sndUna.Set(p.Ack)
	} else {
		// Stale or duplicate cumulative ACK: never moves state backwards
		// and never revives already-acked bytes.
		s.log.Log(s.q.Now(), "ACK stale/dup ack=%s (una=%s)", p.Ack, s.sndUna)
	}
	// Merge SACK blocks, clipped to [sndUna, sndNxt): already-acked data
	// below sndUna is discarded, so old ACKs cannot trigger its retx.
	for _, b := range p.SACKs {
		lo := b.Lo
		if lo.Cmp(s.sndUna) < 0 {
			lo = s.sndUna
		}
		hi := b.Hi
		if hi.Cmp(s.sndNxt) > 0 {
			hi = s.sndNxt
		}
		if lo.Cmp(hi) < 0 {
			s.sacked.Add(lo, hi)
		}
	}
	zero := new(big.Int)
	s.sacked.Subtract(zero, s.sndUna)
	s.retx.Subtract(zero, s.sndUna)
	for _, iv := range s.sacked.Intervals() {
		s.retx.Subtract(iv.Lo, iv.Hi)
	}
	s.retransmitGaps(false)
	s.maybeSendNew()
}

// retransmitGaps retransmits only the missing byte ranges. On a SACK-driven
// pass the range is [sndUna, highest SACKed byte); on an RTO it is the whole
// outstanding window. Both are clamped to the (possibly shrunk) window, and
// ranges already retransmitted and still in flight are skipped.
func (s *Sender) retransmitGaps(fromRTO bool) {
	limit := new(big.Int).Set(s.sndUna)
	if fromRTO {
		limit.Set(s.sndNxt)
	} else {
		for _, iv := range s.sacked.Intervals() {
			if iv.Hi.Cmp(limit) > 0 {
				limit.Set(iv.Hi)
			}
		}
	}
	if wnd := new(big.Int).Add(s.sndUna, s.window); limit.Cmp(wnd) > 0 {
		limit.Set(wnd)
	}
	if limit.Cmp(s.sndNxt) > 0 {
		limit.Set(s.sndNxt)
	}
	for _, gap := range s.sacked.Gaps(s.sndUna, limit) {
		for _, miss := range s.retx.Gaps(gap.Lo, gap.Hi) {
			s.log.Log(s.q.Now(), "GAP %s -> retransmit", miss)
			s.emitChunks(miss)
			s.retx.Add(miss.Lo, miss.Hi)
		}
	}
}

// armTimer schedules the retransmission timeout.
func (s *Sender) armTimer() {
	if s.timerSet || s.rto <= 0 {
		return
	}
	s.timerSet = true
	s.q.Schedule(s.rto, func() {
		s.timerSet = false
		if s.sndUna.Cmp(s.sndNxt) >= 0 {
			return // nothing outstanding
		}
		s.log.Log(s.q.Now(), "RTO timeout: rescanning gaps")
		s.retx = IntervalSet{}
		s.retransmitGaps(true)
		s.armTimer()
	})
}
