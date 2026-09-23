package sim

// DelayFunc computes the delivery delay (virtual nanoseconds) for a message.
type DelayFunc func(now int64, env Envelope) int64

// DropFunc reports whether a send must be silently discarded.
type DropFunc func(now int64, env Envelope) bool

// Replicas reports how many duplicate copies of a message are injected
// (0 = single delivery). Duplicates share the sender-assigned sequence
// number, modelling a link-layer retransmission whose two copies both arrive.
type Replicas func(now int64, env Envelope) int

// ObserveFunc is called once for every send (including dropped and
// duplicated messages); it is the test/demo tracing hook.
type ObserveFunc func(now int64, env Envelope, dropped bool, copies int)

// Network controls all communication.
type Network struct {
	Delay     DelayFunc
	Drop      DropFunc
	Duplicate Replicas
	Observe   ObserveFunc
	seq       int64
}

// NewNetwork returns a network with a fixed 5ms delay and clean links.
func NewNetwork() *Network {
	return &Network{
		Delay: func(int64, Envelope) int64 { return DefaultDelay },
	}
}

// DefaultDelay is the one-way propagation delay of the clean network.
const DefaultDelay = int64(5_000_000) // 5ms

// ConstantDelay replaces the delay function.
func (n *Network) ConstantDelay(d int64) *Network {
	n.Delay = func(int64, Envelope) int64 { return d }
	return n
}

// DropFromTo makes messages from->to (in either role as indicated) vanish.
// Use a negative limit to drop forever; otherwise only the first limit
// matching messages are dropped. Returns a pointer to the remaining counter
// so callers can inspect or lift the rule.
func (n *Network) DropFromTo(from, to string, limit int) *int {
	remaining := limit
	prev := n.Drop
	n.Drop = func(now int64, env Envelope) bool {
		if env.From == from && env.To == to && (remaining < 0 || remaining > 0) {
			if remaining > 0 {
				remaining--
			}
			return true
		}
		if prev != nil {
			return prev(now, env)
		}
		return false
	}
	return &remaining
}

func (n *Network) route(s *Scheduler, env Envelope) {
	n.seq++
	env.Seq = n.seq
	dropped := n.Drop != nil && n.Drop(s.Now(), env)
	copies := 0
	if !dropped {
		copies = 1
		if n.Duplicate != nil {
			if extra := n.Duplicate(s.Now(), env); extra > 0 {
				copies += extra
			}
		}
	}
	if n.Observe != nil {
		n.Observe(s.Now(), env, dropped, copies)
	}
	if dropped {
		return
	}
	delay := int64(0)
	if n.Delay != nil {
		delay = n.Delay(s.Now(), env)
	}
	if delay < 0 {
		delay = 0
	}
	for i := 0; i < copies; i++ {
		// Stagger duplicate copies by one virtual nanosecond so their
		// delivery order is fully deterministic.
		s.deliver(s.Now()+delay+int64(i), env)
	}
}
