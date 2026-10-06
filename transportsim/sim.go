package main

import (
	"fmt"
	"sort"
)

// dupAckFastRetxThreshold: consecutive duplicate ACKs that trigger a fast
// retransmit of the oldest unacknowledged in-flight packet.
const dupAckFastRetxThreshold = 3

// Packet is tracked by sequence number for its whole lifetime. A packet
// occupies exactly one unit of in-flight credit no matter how many times
// it is physically (re)transmitted.
type Packet struct {
	Seq           int
	SentCount     int  // physical transmissions, >= 1
	FirstSend     int  // time of first transmission (RTT base)
	LastSend      int  // time of latest (re)transmission
	Acked         bool // logically acknowledged
	Released      bool // in-flight credit released; must happen exactly once
	Retransmitted bool // ever retransmitted -> Karn: ACK gives no RTT sample
	Dropped       bool // dropped by the network at least once
}

// Action is one structured entry in the state trace, used both for output
// and for the loss-replay audit.
type Action struct {
	Time   int
	Kind   string // SEND ACK DUPACK DROP TIMEOUT RETX RTT-SAMPLE RTT-SKIP BLOCKED FAST-RETX
	Seq    int    // -1 when not applicable
	Detail string
}

// Sim is the transport simulator state.
type Sim struct {
	Now      int
	NextSeq  int
	Cwnd     float64
	SsThresh float64

	Srtt, Rttvar, Rto float64
	HasRtt            bool

	Packets  map[int]*Packet
	InFlight map[int]bool // logical in-flight set, keyed by seq

	Sent, Acked, Retx, DupAcks, CreditReleased int
	DupStreak                                  int // consecutive duplicate ACKs

	Actions []Action
}

func NewSim() *Sim {
	return &Sim{
		NextSeq:  1,
		Cwnd:     4,
		SsThresh: 64,
		Rto:      100,
		Packets:  map[int]*Packet{},
		InFlight: map[int]bool{},
	}
}

func (s *Sim) act(kind string, seq int, format string, args ...interface{}) {
	s.Actions = append(s.Actions, Action{
		Time:   s.Now,
		Kind:   kind,
		Seq:    seq,
		Detail: fmt.Sprintf(format, args...),
	})
}

// Send transmits up to n new packets, limited by the congestion window.
func (s *Sim) Send(n int) {
	for i := 0; i < n; i++ {
		if len(s.InFlight) >= int(s.Cwnd) {
			s.act("BLOCKED", -1, "send blocked: inflight=%d >= cwnd=%.2f", len(s.InFlight), s.Cwnd)
			return
		}
		p := &Packet{Seq: s.NextSeq, SentCount: 1, FirstSend: s.Now, LastSend: s.Now}
		s.Packets[p.Seq] = p
		s.InFlight[p.Seq] = true
		s.NextSeq++
		s.Sent++
		s.act("SEND", p.Seq, "tx#1")
	}
}

// OnAck processes an ACK for seq. Whether it is a new ACK or a duplicate is
// decided by packet state, never by the event label: an already-acked packet
// must not release in-flight credit a second time.
func (s *Sim) OnAck(seq int) {
	p, ok := s.Packets[seq]
	if !ok {
		s.act("DUPACK", seq, "ACK for unknown seq, ignored")
		return
	}
	if p.Acked {
		s.DupAcks++
		s.DupStreak++
		s.act("DUPACK", seq, "duplicate ACK: credit already released, no window change (streak=%d)", s.DupStreak)
		if s.DupStreak >= dupAckFastRetxThreshold {
			s.fastRetransmit()
		}
		return
	}

	s.DupStreak = 0
	p.Acked = true
	p.Released = true
	delete(s.InFlight, seq)
	s.Acked++
	s.CreditReleased++
	s.act("ACK", seq, "credit released (inflight=%d)", len(s.InFlight))

	// Karn's algorithm: an ACK for a retransmitted packet is ambiguous and
	// must not feed the RTT estimator.
	if p.Retransmitted {
		s.act("RTT-SKIP", seq, "retransmitted packet: ACK not used for RTT sampling")
	} else {
		sample := float64(s.Now - p.FirstSend)
		s.updateRtt(sample)
		s.act("RTT-SAMPLE", seq, "sample=%.1f srtt=%.2f rttvar=%.2f rto=%.2f", sample, s.Srtt, s.Rttvar, s.Rto)
	}

	// Additive increase: slow start below ssthresh, congestion avoidance above.
	if s.Cwnd < s.SsThresh {
		s.Cwnd++
	} else {
		s.Cwnd += 1 / s.Cwnd
	}
}

func (s *Sim) updateRtt(sample float64) {
	if !s.HasRtt {
		s.Srtt = sample
		s.Rttvar = sample / 2
		s.HasRtt = true
	} else {
		d := s.Srtt - sample
		if d < 0 {
			d = -d
		}
		s.Rttvar = 0.75*s.Rttvar + 0.25*d
		s.Srtt = 0.875*s.Srtt + 0.125*sample
	}
	s.Rto = s.Srtt + 4*s.Rttvar
	if s.Rto < 1 {
		s.Rto = 1
	}
}

// Drop models the network dropping an in-flight packet. The sender is not
// aware; the loss must later be repaired by timeout or fast retransmit.
func (s *Sim) Drop(seq int) {
	p, ok := s.Packets[seq]
	if !ok || p.Acked {
		s.act("DROP", seq, "drop of unknown/acked seq, ignored")
		return
	}
	p.Dropped = true
	s.act("DROP", seq, "network dropped packet (sender unaware)")
}

// Timeout models RTO expiry for seq: retransmit and multiplicative decrease.
func (s *Sim) Timeout(seq int) {
	p, ok := s.Packets[seq]
	if !ok || p.Acked || !s.InFlight[seq] {
		s.act("TIMEOUT", seq, "timeout for unknown/acked seq, ignored")
		return
	}
	s.DupStreak = 0
	s.SsThresh = s.Cwnd / 2
	if s.SsThresh < 2 {
		s.SsThresh = 2
	}
	s.Cwnd = 1
	s.act("TIMEOUT", seq, "RTO expired: ssthresh=%.2f cwnd=1", s.SsThresh)
	s.retransmit(p, "timeout")
}

// fastRetransmit retransmits the oldest unacked in-flight packet after 3
// consecutive duplicate ACKs. Retransmission does not change the logical
// in-flight count.
func (s *Sim) fastRetransmit() {
	seq := s.oldestUnacked()
	if seq < 0 {
		return
	}
	s.DupStreak = 0
	s.SsThresh = s.Cwnd / 2
	if s.SsThresh < 2 {
		s.SsThresh = 2
	}
	s.Cwnd = s.SsThresh
	s.act("FAST-RETX", seq, "3 dup ACKs: ssthresh=%.2f cwnd=%.2f", s.SsThresh, s.Cwnd)
	s.retransmit(s.Packets[seq], "fast-retransmit")
}

// retransmit physically resends p. The logical in-flight count is untouched:
// the packet keeps occupying its single unit of credit.
func (s *Sim) retransmit(p *Packet, reason string) {
	p.SentCount++
	p.Retransmitted = true
	p.LastSend = s.Now
	s.Retx++
	s.act("RETX", p.Seq, "tx#%d (%s), inflight unchanged=%d", p.SentCount, reason, len(s.InFlight))
}

func (s *Sim) oldestUnacked() int {
	best := -1
	for seq := range s.InFlight {
		if best < 0 || seq < best {
			best = seq
		}
	}
	return best
}

// Check verifies the conservation invariants. It must hold after every event:
//  1. sent == acked + inflight            (no packet created or lost logically)
//  2. creditReleased == acked             (credit released exactly once per ACK)
//  3. acked <=> released for every packet (no double release, no leak)
//  4. no acked packet is still in flight
func (s *Sim) Check() error {
	if s.Sent != s.Acked+len(s.InFlight) {
		return fmt.Errorf("sent(%d) != acked(%d) + inflight(%d)", s.Sent, s.Acked, len(s.InFlight))
	}
	if s.CreditReleased != s.Acked {
		return fmt.Errorf("creditReleased(%d) != acked(%d)", s.CreditReleased, s.Acked)
	}
	for _, seq := range s.sortedSeqs() {
		p := s.Packets[seq]
		if p.Acked != p.Released {
			return fmt.Errorf("seq=%d acked=%v but released=%v", seq, p.Acked, p.Released)
		}
		if p.Acked && s.InFlight[seq] {
			return fmt.Errorf("seq=%d acked but still in flight", seq)
		}
	}
	return nil
}

func (s *Sim) sortedSeqs() []int {
	seqs := make([]int, 0, len(s.Packets))
	for seq := range s.Packets {
		seqs = append(seqs, seq)
	}
	sort.Ints(seqs)
	return seqs
}

func (s *Sim) State() string {
	srtt := "-"
	if s.HasRtt {
		srtt = fmt.Sprintf("%.2f", s.Srtt)
	}
	return fmt.Sprintf("cwnd=%.2f ssthresh=%.2f inflight=%d sent=%d acked=%d retx=%d dup=%d srtt=%s rto=%.2f",
		s.Cwnd, s.SsThresh, len(s.InFlight), s.Sent, s.Acked, s.Retx, s.DupAcks, srtt, s.Rto)
}

// Audit replays the action log to cross-check loss handling:
//   - a dropped/timed-out packet must be retransmitted before it can be acked
//   - no RTT sample may be taken after a packet's first retransmission
//   - every credit release corresponds to a first-time ACK
// Returns violations (hard errors) and notes (unrepaired losses at end).
func (s *Sim) Audit() (violations, notes []string) {
	type st struct {
		lostPending bool
		retxSeen    bool
		acked       bool
	}
	state := map[int]*st{}
	get := func(seq int) *st {
		if _, ok := state[seq]; !ok {
			state[seq] = &st{}
		}
		return state[seq]
	}
	for _, a := range s.Actions {
		switch a.Kind {
		case "DROP", "TIMEOUT":
			get(a.Seq).lostPending = true
		case "RETX":
			get(a.Seq).lostPending = false
			get(a.Seq).retxSeen = true
		case "ACK":
			p := get(a.Seq)
			if p.lostPending {
				violations = append(violations, fmt.Sprintf("t=%d seq=%d acked while loss still unrepaired", a.Time, a.Seq))
			}
			if p.acked {
				violations = append(violations, fmt.Sprintf("t=%d seq=%d credit released twice", a.Time, a.Seq))
			}
			p.acked = true
		case "RTT-SAMPLE":
			if get(a.Seq).retxSeen {
				violations = append(violations, fmt.Sprintf("t=%d seq=%d RTT sampled after retransmission", a.Time, a.Seq))
			}
		}
	}
	for _, seq := range s.sortedSeqs() {
		p := s.Packets[seq]
		if p.Dropped && !p.Retransmitted {
			notes = append(notes, fmt.Sprintf("seq=%d dropped but never retransmitted", seq))
		}
	}
	return violations, notes
}
