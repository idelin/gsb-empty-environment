package causalslice

import "math/rand"

// Network is the adversary/controller for communication. In one place it
// decides delay, reordering, loss and partitioning; all three are controlled
// inputs of the experiment, never real sockets.
type Network struct {
	rng       *rand.Rand
	minDelay  int
	maxDelay  int
	dropRule  func(env Envelope) bool
	dropped   int
	delivered int
}

// NewNetwork creates a FIFO-ish network with uniform 1..4 tick delays.
func NewNetwork() *Network {
	return &Network{rng: rand.New(rand.NewSource(1)), minDelay: 1, maxDelay: 4}
}

// Seed makes the delay/loss randomness reproducible.
func (n *Network) Seed(seed int64) *Network {
	n.rng = rand.New(rand.NewSource(seed))
	return n
}

// Delay sets the inclusive range of delivery delays.
func (n *Network) Delay(min, max int) *Network {
	if min < 1 || max < min {
		panic("bad delay range")
	}
	n.minDelay, n.maxDelay = min, max
	return n
}

// Drop installs a predicate evaluated for every send: true means the message
// is lost forever (callers may retry on their own timers).
func (n *Network) Drop(rule func(env Envelope) bool) *Network {
	n.dropRule = rule
	return n
}

// Dropped reports how many messages the policy discarded.
func (n *Network) Dropped() int { return n.dropped }

// Delivered reports how many messages were scheduled.
func (n *Network) Delivered() int { return n.delivered }

func (n *Network) route(sim *Sim, env Envelope) {
	if n.dropRule != nil && n.dropRule(env) {
		n.dropped++
		sim.Log("network drops %s -> %s (%T)", env.From, env.To, env.Msg)
		return
	}
	n.delivered++
	span := n.maxDelay - n.minDelay + 1
	at := sim.Time() + n.minDelay + n.rng.Intn(span)
	sim.deliver(env, at)
}
