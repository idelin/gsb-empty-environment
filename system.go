package kernel

const (
	// ackTimeoutTicks is the retransmission timer interval.
	ackTimeoutTicks = 30
	// lossModulus controls simulated packet loss: an arrival is lost
	// when the PRNG draw is congruent to 0 mod lossModulus.
	lossModulus = 20
)

// pendingPacket is a packet sitting in the NIC receive queue.
type pendingPacket struct {
	Packet
	ArrivalTick uint64
	Lost        bool
	Retries     uint32
}

// Stats are cumulative counters kept by the system.
type Stats struct {
	Arrivals       uint64
	Processed      uint64
	Timeouts       uint64
	ClockInterrupt uint64
	BytesProcessed uint64
	Retransmits    uint64
}

// System is the simulated machine under debug. It bundles every component
// whose memory invariants must be reproducible: the PRNG, the NIC receive
// queue, the retransmission timer table and the statistics block.
type System struct {
	rng       PRNG
	inbox     []pendingPacket
	timers    map[uint64]uint64 // packet ID -> deadline tick
	processed []uint64          // processed packet IDs, in order
	stats     Stats
}

// NewSystem returns a pristine system whose randomness derives from seed.
func NewSystem(seed uint64) *System {
	return &System{
		rng:    NewPRNG(seed),
		timers: make(map[uint64]uint64),
	}
}

// Stats returns a copy of the current counters.
func (s *System) Stats() Stats {
	return s.stats
}

// apply mutates the system for one event, possibly scheduling follow-up
// internal events (retransmission timeouts) on the scheduler.
func (s *System) apply(ev Event, sched *Scheduler) {
	switch ev.Kind {
	case KindPacketArrival:
		// The loss decision consumes exactly one PRNG draw per arrival,
		// so the recorded seed fully determines the loss pattern.
		lost := s.rng.Next()%lossModulus == 0
		s.inbox = append(s.inbox, pendingPacket{
			Packet:      ev.Packet,
			ArrivalTick: ev.Tick,
			Lost:        lost,
		})
		deadline := ev.Tick + ackTimeoutTicks
		s.timers[ev.Packet.ID] = deadline
		sched.Push(Event{Tick: deadline, Kind: KindTimeout, TimerID: ev.Packet.ID})
		s.stats.Arrivals++

	case KindClockInterrupt:
		s.stats.ClockInterrupt++
		kept := s.inbox[:0]
		for _, p := range s.inbox {
			if p.Lost {
				kept = append(kept, p)
				continue
			}
			delete(s.timers, p.ID)
			sched.CancelTimer(p.ID)
			s.processed = append(s.processed, p.ID)
			s.stats.Processed++
			s.stats.BytesProcessed += uint64(p.Size)
		}
		s.inbox = kept

	case KindTimeout:
		deadline, ok := s.timers[ev.TimerID]
		if !ok || deadline != ev.Tick {
			return // stale timer: packet was already processed
		}
		delete(s.timers, ev.TimerID)
		for i := range s.inbox {
			if s.inbox[i].ID == ev.TimerID {
				s.inbox[i].Lost = false
				s.inbox[i].Retries++
			}
		}
		s.stats.Timeouts++
		s.stats.Retransmits++
	}
}
