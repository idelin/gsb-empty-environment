package antientropy

import (
	"math/rand"
	"sync"
)

// Gossiper piggybacks state updates on lightweight gossip packets.
// Packets may be lost in flight: ShouldDrop decides, per packet sequence
// number, whether the packet is dropped before reaching any peer.
type Gossiper struct {
	Local      *Store
	Peers      []*Store
	ShouldDrop func(seq uint64) bool

	mu      sync.Mutex
	seq     uint64
	Sent    int
	Dropped int
}

// Broadcast applies st locally and tries to piggyback it to all peers.
func (g *Gossiper) Broadcast(st NodeState) {
	g.Local.Apply(st)
	g.mu.Lock()
	seq := g.seq
	g.seq++
	drop := g.ShouldDrop != nil && g.ShouldDrop(seq)
	if drop {
		g.Dropped++
	} else {
		g.Sent++
	}
	g.mu.Unlock()
	if drop {
		return
	}
	for _, p := range g.Peers {
		p.Apply(st)
	}
}

// DeterministicDropper keeps exactly one in every keepEvery packets and
// drops the rest: keepEvery=10 deterministically drops 90% of packets.
func DeterministicDropper(keepEvery uint64) func(seq uint64) bool {
	return func(seq uint64) bool { return seq%keepEvery != 0 }
}

// RandomDropper drops each packet with the given probability.
func RandomDropper(rate float64, seed int64) func(seq uint64) bool {
	rng := rand.New(rand.NewSource(seed))
	var mu sync.Mutex
	return func(seq uint64) bool {
		mu.Lock()
		defer mu.Unlock()
		return rng.Float64() < rate
	}
}
