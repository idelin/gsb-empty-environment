package tcpsim

import "fmt"

// Congestion control phases.
const (
	PhaseSlowStart = "slow-start"
	PhaseCongAvoid = "congestion-avoidance"
)

// Sender is a TCP-style congestion-controlled sender.
//
// The design keeps two mechanisms strictly separate:
//
//  1. ACK deduplication and window accounting (Receive): duplicate or
//     stale ACKs are detected here and never grow the congestion
//     window; only ACKs that advance the cumulative left edge do.
//  2. Retransmission timing (armTimer/onTimeout): an independent
//     generation-protected timer on the event engine. ACK processing
//     only ever reschedules or cancels it; duplicate-ACK handling never
//     touches it, and the timer never mutates ACK dedup state.
type Sender struct {
	Eng   *Engine
	Out   *Link
	MSS   int
	Total int // total payload bytes to send

	cwnd     int // congestion window, bytes
	ssthresh int // slow-start threshold, bytes
	sndUna   int // oldest unacknowledged byte
	sndNxt   int // next byte to send for the first time

	rtoBase int // base retransmission timeout, ticks
	rto     int // current RTO, doubles on each timeout

	timerGen   int  // generation counter invalidating stale timer events
	timerArmed bool // whether a live timer event exists

	dupAcks    int // consecutive duplicate ACKs for sndUna
	recoverSeq int // sndNxt at last timeout; RTO resets once una passes it
	phase      string

	// Statistics.
	Timeouts        int
	FastRetransmits int
	Retransmits     int
	DupAcks         int
	StaleAcks       int

	// Log, if non-nil, receives human-readable trace lines.
	Log func(tick int, msg string)
}

// NewSender creates a sender with cwnd = 1 MSS in slow start.
func NewSender(eng *Engine, total, mss, initSsthresh, rtoBase int) *Sender {
	return &Sender{
		Eng:      eng,
		Total:    total,
		MSS:      mss,
		cwnd:     mss,
		ssthresh: initSsthresh,
		rtoBase:  rtoBase,
		rto:      rtoBase,
		phase:    PhaseSlowStart,
	}
}

// Cwnd returns the current congestion window in bytes.
func (s *Sender) Cwnd() int { return s.cwnd }

// Ssthresh returns the current slow-start threshold in bytes.
func (s *Sender) Ssthresh() int { return s.ssthresh }

// RTO returns the current retransmission timeout in ticks.
func (s *Sender) RTO() int { return s.rto }

// SndUna returns the oldest unacknowledged byte.
func (s *Sender) SndUna() int { return s.sndUna }

// Done reports whether all bytes have been acknowledged.
func (s *Sender) Done() bool { return s.sndUna >= s.Total }

// Usable returns the currently usable window in bytes. It is clamped at
// zero: stale state must never produce a negative effective window.
func (s *Sender) Usable() int {
	u := s.cwnd - (s.sndNxt - s.sndUna)
	if u < 0 {
		u = 0
	}
	return u
}

// Start begins transmission.
func (s *Sender) Start() { s.maybeSend() }

// Receive processes an incoming packet; only ACKs are relevant here.
func (s *Sender) Receive(p Packet) {
	if p.Kind != AckPacket {
		return
	}
	ack := p.Seq
	if ack > s.sndNxt {
		// A cumulative ACK can never credit bytes that were never
		// sent; clamp the jump to what is actually outstanding.
		ack = s.sndNxt
	}
	if ack <= s.sndUna {
		s.onDupAck(ack)
		return
	}
	s.onNewAck(ack)
}

// onDupAck handles duplicate and stale ACKs. This path never modifies
// cwnd, never moves sndUna and never touches the retransmission timer,
// so repeated ACKs cannot inflate the effective window.
func (s *Sender) onDupAck(ack int) {
	if ack < s.sndUna {
		// Stale ACK for already-acknowledged data: ignore entirely.
		s.StaleAcks++
		return
	}
	s.DupAcks++
	if s.sndUna >= s.sndNxt {
		return // nothing outstanding
	}
	s.dupAcks++
	if s.dupAcks == 3 {
		s.fastRetransmit()
	}
}

// onNewAck handles an ACK that advances the cumulative left edge.
func (s *Sender) onNewAck(ack int) {
	newly := ack - s.sndUna
	s.sndUna = ack
	s.dupAcks = 0

	if newly > s.MSS {
		s.logf("cumulative ACK jump: ack=%d newly-acked=%d bytes", ack, newly)
	}

	// Window growth happens only here, credited solely by bytes that
	// were genuinely unacknowledged.
	if s.cwnd < s.ssthresh {
		n := newly
		if n > s.MSS {
			n = s.MSS
		}
		s.cwnd += n
		s.logf("%s ack=%d cwnd=%d ssthresh=%d", s.phase, ack, s.cwnd, s.ssthresh)
		if s.cwnd >= s.ssthresh {
			s.phase = PhaseCongAvoid
			s.logf("phase -> %s (cwnd=%d ssthresh=%d)", s.phase, s.cwnd, s.ssthresh)
		}
	} else {
		inc := s.MSS * s.MSS / s.cwnd
		if inc < 1 {
			inc = 1
		}
		s.cwnd += inc
		s.logf("%s ack=%d cwnd=%d ssthresh=%d", s.phase, ack, s.cwnd, s.ssthresh)
	}

	// Recovery complete: restore the base RTO once everything sent
	// before the timeout has been acknowledged.
	if s.rto != s.rtoBase && s.sndUna >= s.recoverSeq {
		s.rto = s.rtoBase
		s.logf("recovery complete: rto restored to %d", s.rto)
	}

	// Restart the timer for the new oldest byte, or cancel it when
	// nothing is outstanding. This is the only ACK-path interaction
	// with the timer.
	if s.sndUna < s.sndNxt {
		s.armTimer()
	} else {
		s.timerGen++
		s.timerArmed = false
	}
	s.maybeSend()
}

// fastRetransmit retransmits the oldest unacknowledged segment after
// three duplicate ACKs and halves the window. Unlike classic fast
// recovery, further duplicate ACKs do NOT inflate cwnd.
func (s *Sender) fastRetransmit() {
	s.FastRetransmits++
	s.ssthresh = maxInt(s.cwnd/2, 2*s.MSS)
	s.cwnd = s.ssthresh
	s.phase = PhaseCongAvoid
	s.logf("fast retransmit seq=%d ssthresh=%d cwnd=%d", s.sndUna, s.ssthresh, s.cwnd)
	s.retransmit(s.sndUna)
}

// onTimeout handles RTO expiry: window collapse to 1 MSS, ssthresh
// halved, RTO doubled (exponential backoff), retransmit oldest byte.
func (s *Sender) onTimeout() {
	s.Timeouts++
	s.ssthresh = maxInt(s.cwnd/2, 2*s.MSS)
	s.cwnd = s.MSS
	s.phase = PhaseSlowStart
	old := s.rto
	s.rto *= 2
	s.recoverSeq = s.sndNxt
	s.logf("TIMEOUT seq=%d rto=%d->%d cwnd=%d ssthresh=%d",
		s.sndUna, old, s.rto, s.cwnd, s.ssthresh)
	s.retransmit(s.sndUna)
	s.armTimer()
}

// retransmit sends the segment starting at seq again. It deliberately
// does not alter sndNxt, cwnd or the ACK dedup state.
func (s *Sender) retransmit(seq int) {
	l := s.MSS
	if rem := s.Total - seq; rem < l {
		l = rem
	}
	if l <= 0 {
		return
	}
	s.Retransmits++
	s.Out.Send(Packet{Kind: DataPacket, Seq: seq, Len: l})
}

// maybeSend transmits new segments while the usable window allows.
func (s *Sender) maybeSend() {
	for s.sndNxt < s.Total {
		l := s.MSS
		if rem := s.Total - s.sndNxt; rem < l {
			l = rem
		}
		if s.Usable() < l {
			break
		}
		s.Out.Send(Packet{Kind: DataPacket, Seq: s.sndNxt, Len: l})
		s.sndNxt += l
		if !s.timerArmed {
			s.armTimer()
		}
	}
}

// armTimer (re)schedules the retransmission timer. The generation
// counter invalidates previously scheduled timer events, so at most one
// logical timer is ever live.
func (s *Sender) armTimer() {
	s.timerGen++
	gen := s.timerGen
	s.timerArmed = true
	rto := s.rto
	s.Eng.After(rto, func() {
		if gen != s.timerGen || !s.timerArmed {
			return
		}
		if s.sndUna >= s.sndNxt {
			return
		}
		s.onTimeout()
	})
}

func (s *Sender) logf(format string, args ...interface{}) {
	if s.Log != nil {
		s.Log(s.Eng.Now(), fmt.Sprintf(format, args...))
	}
}

// Receiver is a cumulative-ACK receiver with a reassembly buffer, so a
// retransmitted segment can make the cumulative ACK jump over buffered
// out-of-order data.
type Receiver struct {
	Out *Link

	RcvNxt   int // next in-order byte expected
	Received int // in-order payload bytes delivered
	Buffered int // payload bytes held out of order

	DupSegments int
	AcksSent    int

	buf map[int]int // seq -> payload length
}

// NewReceiver creates a receiver expecting byte 0.
func NewReceiver() *Receiver {
	return &Receiver{buf: make(map[int]int)}
}

// Receive processes a data segment and emits a cumulative ACK.
func (r *Receiver) Receive(p Packet) {
	if p.Kind != DataPacket {
		return
	}
	end := p.Seq + p.Len
	switch {
	case end <= r.RcvNxt:
		r.DupSegments++
	case p.Seq == r.RcvNxt:
		r.RcvNxt = end
		r.Received += p.Len
		r.drain()
	default:
		if _, ok := r.buf[p.Seq]; ok {
			r.DupSegments++
		} else {
			r.buf[p.Seq] = p.Len
			r.Buffered += p.Len
		}
	}
	r.AcksSent++
	r.Out.Send(Packet{Kind: AckPacket, Seq: r.RcvNxt})
}

// drain delivers contiguous buffered segments after a gap is filled.
func (r *Receiver) drain() {
	for {
		l, ok := r.buf[r.RcvNxt]
		if !ok {
			return
		}
		delete(r.buf, r.RcvNxt)
		r.RcvNxt += l
		r.Received += l
		r.Buffered -= l
	}
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
