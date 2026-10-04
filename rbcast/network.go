package rbcast

import "math/rand"

// Network is an unreliable link simulator: it can drop, duplicate and
// reorder messages. Delivery is explicit (via Flush) so tests stay
// deterministic with a seeded RNG.
type Network struct {
	rnd        *rand.Rand
	lossProb   float64
	dupProb    float64
	reorderWin int // pending buffer size shuffled before delivery; 0 = in-order
	pending    []queuedMsg
	Dropped    int
}

type queuedMsg struct {
	to string
	m  Message
}

func NewNetwork(seed int64, lossProb, dupProb float64, reorderWin int) *Network {
	return &Network{
		rnd:        rand.New(rand.NewSource(seed)),
		lossProb:   lossProb,
		dupProb:    dupProb,
		reorderWin: reorderWin,
	}
}

// Send enqueues a message for a recipient, applying loss and duplication.
func (n *Network) Send(to string, m Message) {
	copies := 1
	if n.rnd.Float64() < n.dupProb {
		copies++
	}
	for i := 0; i < copies; i++ {
		if n.rnd.Float64() < n.lossProb {
			n.Dropped++
			continue
		}
		n.pending = append(n.pending, queuedMsg{to: to, m: m})
	}
}

// Flush delivers all pending messages to the given nodes, shuffling within
// the reorder window to simulate out-of-order arrival.
func (n *Network) Flush(nodes map[string]*Node) {
	for len(n.pending) > 0 {
		take := len(n.pending)
		if n.reorderWin > 0 && take > n.reorderWin {
			take = n.reorderWin
		}
		batch := n.pending[:take]
		n.pending = n.pending[take:]
		if n.reorderWin > 0 {
			n.rnd.Shuffle(len(batch), func(i, j int) { batch[i], batch[j] = batch[j], batch[i] })
		}
		for _, q := range batch {
			if node, ok := nodes[q.to]; ok {
				node.Receive(q.m)
			}
		}
	}
}

// Pending returns the number of queued, undelivered messages.
func (n *Network) Pending() int { return len(n.pending) }
