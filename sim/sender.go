package sim

import "fmt"

// Sender is a reliable byte-stream sender. It tracks the sent-but-unacked
// byte interval [sndUna, sndNxt), maintains a SACK set of acknowledged
// ranges, and on timeout retransmits only the gaps in that set. It tolerates
// out-of-order, duplicate and overlapping ACKs, honours window shrink, and
// rejects ACKs that cover bytes never sent.
type Sender struct {
	Sched *Scheduler
	Out   *Pipe

	MSS  uint64 // max segment size in bytes
	Cwnd uint64 // congestion window in bytes (may shrink at runtime)
	RTO  uint64 // retransmission timeout in ticks
	Rwnd uint64 // last advertised receive window

	sndUna Seq     // oldest unacknowledged byte; buf is anchored here
	sndNxt Seq     // next sequence number to send for the first time
	buf    []byte  // bytes from sndUna up to the end of enqueued data
	sack   SackSet // union of everything ever acknowledged
	sent   SackSet // union of everything ever put on the wire

	timerPending bool

	// Statistics.
	BytesSent          uint64 // first-transmission bytes
	BytesRetransmitted uint64 // retransmitted bytes (gaps only)
	RejectedAcks       uint64 // ACKs covering unsent bytes
	StaleAcks          uint64 // cumulative ACKs older than sndUna
	Timeouts           uint64

	Log func(msg string)
}

// NewSender creates a sender whose sequence space starts at base.
func NewSender(sched *Scheduler, base Seq, mss, cwnd, rto uint64) *Sender {
	return &Sender{
		Sched:  sched,
		MSS:    mss,
		Cwnd:   cwnd,
		RTO:    rto,
		Rwnd:   ^uint64(0),
		sndUna: base,
		sndNxt: base,
	}
}

func (s *Sender) logf(format string, args ...interface{}) {
	if s.Log != nil {
		s.Log(fmt.Sprintf(format, args...))
	}
}

// Enqueue appends application data to the send buffer and transmits as much
// as the window allows.
func (s *Sender) Enqueue(data []byte) {
	s.buf = append(s.buf, data...)
	s.trySend()
}

// SetCwnd changes the congestion window (grow or shrink) at runtime.
func (s *Sender) SetCwnd(w uint64) {
	s.Cwnd = w
	s.logf("CWND -> %d", w)
	s.trySend()
}

// SndUna returns the oldest unacknowledged sequence number.
func (s *Sender) SndUna() Seq { return s.sndUna }

// SndNxt returns the next never-sent sequence number.
func (s *Sender) SndNxt() Seq { return s.sndNxt }

// Sacked returns the acknowledged byte-ranges union.
func (s *Sender) Sacked() []Range { return s.sack.Ranges() }

// SentRanges returns the union of all byte ranges ever transmitted.
func (s *Sender) SentRanges() []Range { return s.sent.Ranges() }

func (s *Sender) window() uint64 {
	if s.Rwnd < s.Cwnd {
		return s.Rwnd
	}
	return s.Cwnd
}

func (s *Sender) trySend() {
	win := s.window()
	for {
		inflight := s.sndNxt.Diff(s.sndUna)
		unsent := uint64(len(s.buf)) - s.sndNxt.Diff(s.sndUna)
		if unsent == 0 {
			return
		}
		n := s.MSS
		if unsent < n {
			n = unsent
		}
		if inflight+n > win {
			return
		}
		s.transmit(s.sndNxt, s.sndNxt.Add(n), false)
		s.sndNxt = s.sndNxt.Add(n)
		s.BytesSent += n
		s.armTimer()
	}
}

func (s *Sender) transmit(start, end Seq, retx bool) {
	off := start.Diff(s.sndUna)
	n := end.Diff(start)
	payload := append([]byte(nil), s.buf[int(off):int(off+n)]...)
	s.Out.Send(Packet{
		Kind:       DataPacket,
		Start:      start,
		End:        end,
		Payload:    payload,
		Retransmit: retx,
	})
	s.sent.Add(Range{Start: start, End: end})
	if retx {
		s.logf("RETX [%s,%s) %dB (gap retransmit)", start, end, n)
	} else {
		s.logf("SEND [%s,%s) %dB", start, end, n)
	}
}

// Receive processes an incoming ACK packet.
func (s *Sender) Receive(p Packet) {
	if p.Kind != AckPacket {
		return
	}
	// Reject ACKs that confirm bytes never sent.
	if p.Ack.Cmp(s.sndNxt) > 0 {
		s.RejectedAcks++
		s.logf("REJECT ack=%s: beyond sndNxt=%s (unsent bytes)", p.Ack, s.sndNxt)
		return
	}
	for _, b := range p.Sack {
		if b.Start.Cmp(b.End) >= 0 || b.End.Cmp(s.sndNxt) > 0 {
			s.RejectedAcks++
			s.logf("REJECT sack [%s,%s): outside sent data (sndNxt=%s)", b.Start, b.End, s.sndNxt)
			return
		}
	}

	switch {
	case p.Ack.Cmp(s.sndUna) < 0:
		// Stale cumulative ACK: never move sndUna backwards, never
		// retransmit data that is already acknowledged.
		s.StaleAcks++
		s.logf("STALE ack=%s < sndUna=%s: ignored", p.Ack, s.sndUna)
	case p.Ack.Cmp(s.sndUna) > 0:
		s.sack.Add(Range{Start: s.sndUna, End: p.Ack})
		trim := p.Ack.Diff(s.sndUna)
		s.buf = s.buf[int(trim):]
		s.sndUna = p.Ack
		s.logf("ACK cum=%s (sndUna advanced)", p.Ack)
	}
	for _, b := range p.Sack {
		s.sack.Add(b) // union: overlapping blocks counted once
	}
	s.Rwnd = p.Rwnd

	if len(s.sack.Gaps(s.sndUna, s.sndNxt)) > 0 {
		s.armTimer()
	}
	s.trySend()
}

func (s *Sender) armTimer() {
	if s.timerPending {
		return
	}
	s.timerPending = true
	s.Sched.After(s.RTO, func() {
		s.timerPending = false
		s.onTimeout()
	})
}

// onTimeout retransmits only the gaps: sent-but-unacknowledged ranges.
// Data already covered by the SACK set is never resent, so stale or
// duplicate ACKs cannot trigger redundant retransmissions.
func (s *Sender) onTimeout() {
	gaps := s.sack.Gaps(s.sndUna, s.sndNxt)
	if len(gaps) == 0 {
		return
	}
	s.Timeouts++
	for _, g := range gaps {
		for start := g.Start; start.Less(g.End); {
			n := s.MSS
			if rem := g.End.Diff(start); rem < n {
				n = rem
			}
			end := start.Add(n)
			s.transmit(start, end, true)
			s.BytesRetransmitted += n
			start = end
		}
	}
	s.armTimer()
}
