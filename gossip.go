package antientropy

import (
	"math/rand"
	"sync"
)

// Network simulates an unreliable transport: each gossip piggyback
// packet is dropped independently with probability LossRate.
type Network struct {
	LossRate float64

	mu      sync.Mutex
	rng     *rand.Rand
	Sent    int
	Dropped int
}

func NewNetwork(lossRate float64, seed int64) *Network {
	return &Network{LossRate: lossRate, rng: rand.New(rand.NewSource(seed))}
}

// Send attempts to deliver updates from one node to another.
// It reports whether the packet survived the loss simulation.
func (n *Network) Send(from, to *Node, updates []State) bool {
	n.mu.Lock()
	n.Sent++
	drop := n.rng.Float64() < n.LossRate
	if drop {
		n.Dropped++
	}
	n.mu.Unlock()
	if drop {
		return false
	}
	to.Receive(updates)
	return true
}

// Node is a cluster member running gossip plus anti-entropy.
type Node struct {
	ID    string
	Store *Store
	net   *Network

	mu      sync.Mutex
	rng     *rand.Rand
	hot     []State // recent updates piggybacked on gossip
	repairs []State // states this node learned via anti-entropy
}

const hotBufferCap = 64

func NewNode(id string, net *Network, seed int64) *Node {
	return &Node{
		ID:    id,
		Store: NewStore(),
		net:   net,
		rng:   rand.New(rand.NewSource(seed)),
	}
}

// Publish records a local observation (e.g. a peer went offline)
// and marks it for gossip dissemination.
func (n *Node) Publish(st State) {
	if n.Store.Apply(st) {
		n.addHot(st)
	}
}

// Receive applies gossiped updates and re-queues fresh ones
// so they keep spreading on subsequent rounds.
func (n *Node) Receive(updates []State) {
	for _, st := range updates {
		if n.Store.Apply(st) {
			n.addHot(st)
		}
	}
}

func (n *Node) addHot(st State) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.hot = append(n.hot, st)
	if len(n.hot) > hotBufferCap {
		n.hot = n.hot[len(n.hot)-hotBufferCap:]
	}
}

// GossipRound sends the hot update buffer to one random peer,
// subject to network loss.
func (n *Node) GossipRound(peers []*Node) {
	n.mu.Lock()
	updates := append([]State(nil), n.hot...)
	n.mu.Unlock()
	if len(updates) == 0 {
		return
	}
	peer := n.pickPeer(peers)
	if peer == nil {
		return
	}
	n.net.Send(n, peer, updates)
}

func (n *Node) pickPeer(peers []*Node) *Node {
	n.mu.Lock()
	defer n.mu.Unlock()
	if len(peers) == 0 {
		return nil
	}
	for tries := 0; tries < 4; tries++ {
		p := peers[n.rng.Intn(len(peers))]
		if p != n {
			return p
		}
	}
	return nil
}

// recordRepair logs states this node learned through anti-entropy.
func (n *Node) recordRepair(states []State) {
	if len(states) == 0 {
		return
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	n.repairs = append(n.repairs, states...)
}

// Repairs returns all states this node has repaired via anti-entropy so far.
func (n *Node) Repairs() []State {
	n.mu.Lock()
	defer n.mu.Unlock()
	return append([]State(nil), n.repairs...)
}
