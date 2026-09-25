package sim

// Network models an unreliable link inside the simulator: messages can be
// delayed, dropped or duplicated. When Healed is set, delivery is guaranteed
// within one tick — this is the fairness assumption under which liveness is
// proven.
type Network struct {
	sim      *Simulator
	DropRate float64 // probability a message is lost
	DupRate  float64 // probability an extra copy is emitted
	MaxDelay int64   // max extra delivery delay in ticks
	Healed   bool
}

// Send enqueues delivery of m according to the network model.
func (n *Network) Send(m Message) {
	s := n.sim
	if n.Healed {
		s.Schedule(s.Now+1, DeliverEvent{Msg: m})
		return
	}
	if s.rng.Float64() < n.DropRate {
		s.logf("t=%d NET DROP %s -> R%d", s.Now, m.ID, m.To)
		return
	}
	s.Schedule(s.Now+1+s.rng.Int63n(n.MaxDelay+1), DeliverEvent{Msg: m})
	if s.rng.Float64() < n.DupRate {
		s.logf("t=%d NET DUP %s -> R%d", s.Now, m.ID, m.To)
		s.Schedule(s.Now+1+s.rng.Int63n(n.MaxDelay+1), DeliverEvent{Msg: m})
	}
}
