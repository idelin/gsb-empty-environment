package sim

import "math/rand"

// Network delivers messages between actors with simulated latency and
// fault injection. Delivery is fully deterministic for a given seed and
// fault configuration.
type Network struct {
	sim        *Sim
	MinLatency int64
	MaxLatency int64
	rand       *rand.Rand
	// Drop, when non-nil and returning true, drops the message (fault
	// injection point for lost writes/reads/repairs).
	Drop func(from, to, kind string) bool
	// Latency, when non-nil and returning ok=true, overrides the random
	// latency for a message (used to race repairs against writes).
	Latency func(from, to, kind string) (d int64, ok bool)

	Sent      int
	Delivered int
	Dropped   int
}

func NewNetwork(s *Sim, seed int64, minLatency, maxLatency int64) *Network {
	return &Network{
		sim:        s,
		MinLatency: minLatency,
		MaxLatency: maxLatency,
		rand:       rand.New(rand.NewSource(seed)),
	}
}

// Send queues delivery of a message; deliver runs on the receiver when the
// message arrives (unless dropped by the injected fault rule).
func (n *Network) Send(from, to, kind string, deliver func()) {
	n.Sent++
	if n.Drop != nil && n.Drop(from, to, kind) {
		n.Dropped++
		n.sim.Logf("net: DROP %s -> %s (%s)", from, to, kind)
		return
	}
	d := n.MinLatency
	if n.MaxLatency > n.MinLatency {
		d += n.rand.Int63n(n.MaxLatency - n.MinLatency + 1)
	}
	if n.Latency != nil {
		if v, ok := n.Latency(from, to, kind); ok {
			d = v
		}
	}
	n.Delivered++
	n.sim.After(d, deliver)
}
