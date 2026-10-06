package main

import (
	"fmt"
	"sort"
)

// Packet is the per-sequence-number state kept by the sender.
type Packet struct {
	Seq           int
	Sent          bool
	Acked         bool
	Retransmitted bool // Karn's rule: ACKs of retransmitted packets are not RTT samples
	Transmissions int  // how many copies were put on the wire
	Dropped       int  // how many copies the network dropped
	LastSendTime  int
}

// Config carries the given window increase/decrease rules and timer bounds.
type Config struct {
	InitCwnd   int // initial congestion window (packets)
	MinRTO     int // ms
	DefaultRTO int // ms, used before the first RTT sample
	MaxRTO     int // ms, backoff cap
}

// Sim is the transport simulator. Time is a logical clock in milliseconds.
type Sim struct {
	cfg         Config
	Now         int
	Cwnd        int
	NextSeq     int
	SRTT        int // -1 means "no sample yet"
	RTO         int
	DupAcks     int
	Retransmits int
	Packets     map[int]*Packet
	trace       []string
	violations  []string
}

func NewSim(cfg Config) *Sim {
	return &Sim{
		cfg:     cfg,
		Cwnd:    cfg.InitCwnd,
		NextSeq: 1,
		SRTT:    -1,
		RTO:     cfg.DefaultRTO,
		Packets: make(map[int]*Packet),
	}
}

// InFlight is the logical in-flight count: distinct sent, unacked sequence
// numbers. Retransmissions never increase it.
func (s *Sim) InFlight() int {
	n := 0
	for _, p := range s.Packets {
		if p.Sent && !p.Acked {
			n++
		}
	}
	return n
}

func (s *Sim) sent() int {
	n := 0
	for _, p := range s.Packets {
		if p.Sent {
			n++
		}
	}
	return n
}

func (s *Sim) acked() int {
	n := 0
	for _, p := range s.Packets {
		if p.Acked {
			n++
		}
	}
	return n
}

func (s *Sim) oldestUnacked() *Packet {
	var best *Packet
	for _, p := range s.Packets {
		if p.Sent && !p.Acked {
			if best == nil || p.LastSendTime < best.LastSendTime {
				best = p
			}
		}
	}
	return best
}

func (s *Sim) record(ev, note string) {
	srtt := "n/a"
	if s.SRTT >= 0 {
		srtt = fmt.Sprintf("%d", s.SRTT)
	}
	line := fmt.Sprintf("t=%06d ev=%-7s cwnd=%d inflight=%d sent=%d acked=%d srtt=%s rto=%d",
		s.Now, ev, s.Cwnd, s.InFlight(), s.sent(), s.acked(), srtt, s.RTO)
	if note != "" {
		line += " | " + note
	}
	s.trace = append(s.trace, line)
	s.checkInvariants(ev)
}

// checkInvariants enforces the conservation laws after every event:
//  1. acked + inflight == sent            (every sent packet is accounted for)
//  2. cwnd >= 1                           (window never collapses to zero)
//  3. dropped <= transmissions            (cannot drop more copies than sent)
//  4. acked => transmissions > dropped    (the ACKed copy really reached the peer)
func (s *Sim) checkInvariants(ev string) {
	if got := s.acked() + s.InFlight(); got != s.sent() {
		s.violations = append(s.violations, fmt.Sprintf(
			"t=%d ev=%s: conservation broken: acked+inflight=%d != sent=%d",
			s.Now, ev, got, s.sent()))
	}
	if s.Cwnd < 1 {
		s.violations = append(s.violations, fmt.Sprintf(
			"t=%d ev=%s: cwnd collapsed to %d", s.Now, ev, s.Cwnd))
	}
	for _, p := range s.Packets {
		if p.Dropped > p.Transmissions {
			s.violations = append(s.violations, fmt.Sprintf(
				"t=%d ev=%s: seq=%d dropped=%d > transmissions=%d",
				s.Now, ev, p.Seq, p.Dropped, p.Transmissions))
		}
		if p.Acked && p.Transmissions <= p.Dropped {
			s.violations = append(s.violations, fmt.Sprintf(
				"t=%d ev=%s: seq=%d acked but every copy was dropped",
				s.Now, ev, p.Seq))
		}
	}
}

// Tick advances the clock by dt ms, firing retransmission timeouts as they
// come due.
func (s *Sim) Tick(dt int) {
	for i := 0; i < dt; i++ {
		s.Now++
		if p := s.oldestUnacked(); p != nil && s.Now-p.LastSendTime >= s.RTO {
			s.timeout(p, "timer")
		}
	}
	s.record("tick", fmt.Sprintf("dt=%d", dt))
}

// Send lets the application push n new packets, bounded by the window.
func (s *Sim) Send(n int) {
	sentNow := 0
	for i := 0; i < n; i++ {
		if s.InFlight() >= s.Cwnd {
			break
		}
		p := &Packet{Seq: s.NextSeq, Sent: true, Transmissions: 1, LastSendTime: s.Now}
		s.Packets[p.Seq] = p
		s.NextSeq++
		sentNow++
	}
	note := fmt.Sprintf("want=%d sent=%d", n, sentNow)
	if sentNow < n {
		note += " (window full, app blocked)"
	}
	s.record("send", note)
}

// Ack processes an ACK for seq. A duplicate ACK releases no credit and does
// not open the window a second time.
func (s *Sim) Ack(seq int) {
	p, ok := s.Packets[seq]
	if !ok || !p.Sent {
		s.record("ack", fmt.Sprintf("seq=%d IGNORED: unknown sequence number", seq))
		s.violations = append(s.violations, fmt.Sprintf("t=%d: ack for unsent seq=%d", s.Now, seq))
		return
	}
	if p.Acked {
		s.DupAcks++
		s.record("ack", fmt.Sprintf("seq=%d DUP: no credit released, cwnd unchanged", seq))
		return
	}
	p.Acked = true
	s.Cwnd++ // additive increase: +1 per new ACK
	note := fmt.Sprintf("seq=%d new-ack: credit released, cwnd+1", seq)
	if p.Retransmitted {
		note += "; karn: RTT sample skipped (retransmitted)"
	} else {
		r := s.Now - p.LastSendTime
		s.sampleRTT(r)
		note += fmt.Sprintf("; rtt sample=%d", r)
	}
	s.record("ack", note)
}

func (s *Sim) sampleRTT(r int) {
	if s.SRTT < 0 {
		s.SRTT = r
	} else {
		s.SRTT = (7*s.SRTT + r) / 8
	}
	s.RTO = s.cfg.MinRTO
	if 2*s.SRTT > s.RTO {
		s.RTO = 2 * s.SRTT
	}
}

// Drop makes the network lose the outstanding copy of seq. Sender state is
// untouched; the loss is only discovered via timeout.
func (s *Sim) Drop(seq int) {
	p, ok := s.Packets[seq]
	if !ok || !p.Sent || p.Acked || p.Dropped >= p.Transmissions {
		s.record("drop", fmt.Sprintf("seq=%d IGNORED: no live copy in the network", seq))
		return
	}
	p.Dropped++
	s.record("drop", fmt.Sprintf("seq=%d lost in network (sender unaware)", seq))
}

// Timeout forces a retransmission timeout on the oldest unacked packet.
func (s *Sim) Timeout() {
	if p := s.oldestUnacked(); p != nil {
		s.timeout(p, "forced")
	} else {
		s.record("timeout", "nothing in flight")
	}
}

// timeout applies the decrease rule, then retransmits. The retransmission
// reuses the same sequence number, so the logical in-flight count is
// unchanged.
func (s *Sim) timeout(p *Packet, cause string) {
	s.Cwnd /= 2 // multiplicative decrease
	if s.Cwnd < 1 {
		s.Cwnd = 1
	}
	p.Retransmitted = true
	p.Transmissions++
	p.LastSendTime = s.Now
	s.Retransmits++
	s.RTO *= 2 // exponential backoff
	if s.RTO > s.cfg.MaxRTO {
		s.RTO = s.cfg.MaxRTO
	}
	s.record("timeout", fmt.Sprintf("%s: seq=%d retransmitted (tx=%d), cwnd/2, inflight unchanged",
		cause, p.Seq, p.Transmissions))
}

// LossAudit is the loss-replay check: every dropped packet must have been
// recovered by retransmission (or still be in flight), never ACKed from a
// dropped copy.
func (s *Sim) LossAudit() []string {
	var report []string
	var seqs []int
	for seq, p := range s.Packets {
		if p.Dropped > 0 {
			seqs = append(seqs, seq)
		}
	}
	sort.Ints(seqs)
	for _, seq := range seqs {
		p := s.Packets[seq]
		switch {
		case p.Acked && p.Transmissions > p.Dropped:
			report = append(report, fmt.Sprintf(
				"seq=%d dropped=%d recovered via retransmission tx=%d acked: OK",
				seq, p.Dropped, p.Transmissions))
		case !p.Acked:
			report = append(report, fmt.Sprintf(
				"seq=%d dropped=%d still in flight (tx=%d): OK",
				seq, p.Dropped, p.Transmissions))
		default:
			report = append(report, fmt.Sprintf(
				"seq=%d dropped=%d tx=%d acked without surviving copy: VIOLATION",
				seq, p.Dropped, p.Transmissions))
		}
	}
	if len(report) == 0 {
		report = append(report, "no losses to replay")
	}
	return report
}

func (s *Sim) Trace() []string      { return s.trace }
func (s *Sim) Violations() []string { return s.violations }
