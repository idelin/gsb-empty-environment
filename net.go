package snaprepl

import "math/rand"

// Network delivers messages between nodes inside the simulator.
// Delay, loss, duplication, reordering, corruption and partitions are
// all injected here and driven by a seeded RNG, so runs are repeatable.
type Network struct {
	sim         *Sim
	rng         *rand.Rand
	MinLat      int64
	MaxLat      int64
	DropRate    float64 // probability a message is lost
	DupRate     float64 // probability a message is delivered twice
	CorruptRate float64 // probability a snapshot chunk is bit-flipped
	Blocked     map[int]bool
	Deliver     func(to int, m Msg)
}

func NewNetwork(sim *Sim, seed int64) *Network {
	return &Network{
		sim:     sim,
		rng:     rand.New(rand.NewSource(seed)),
		MinLat:  1,
		MaxLat:  5,
		Blocked: map[int]bool{},
	}
}

// Send queues m for delivery. Random per-message latency reorders
// messages; loss/duplication/corruption are applied per the rates.
func (n *Network) Send(from, to int, m Msg) {
	if n.Blocked[from] || n.Blocked[to] {
		return // partitioned
	}
	if n.rng.Float64() < n.DropRate {
		return // lost in flight
	}
	copies := 1
	if n.rng.Float64() < n.DupRate {
		copies = 2
	}
	for i := 0; i < copies; i++ {
		msg := m
		if msg.Kind == MsgSnapChunk && len(msg.Data) > 0 && n.rng.Float64() < n.CorruptRate {
			msg.Data = append([]byte(nil), m.Data...)
			msg.Data[0] ^= 0xff // bit-flip in transit
		}
		lat := n.MinLat + n.rng.Int63n(n.MaxLat-n.MinLat+1)
		n.sim.After(lat, func() { n.Deliver(to, msg) })
	}
}
