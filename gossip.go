package noderouter

import (
	"math/rand"
	"sync"
)

// Cluster is an in-process gossip transport connecting one MemberList
// replica per node. It models the epidemic broadcast used by real
// membership protocols (SWIM / memberlist style push-pull).
type Cluster struct {
	mu      sync.RWMutex
	members map[string]*MemberList
	rnd     *rand.Rand
}

// NewCluster creates an empty gossip network. rnd may be nil, in which case
// a default source is used.
func NewCluster(rnd *rand.Rand) *Cluster {
	if rnd == nil {
		rnd = rand.New(rand.NewSource(1))
	}
	return &Cluster{members: make(map[string]*MemberList), rnd: rnd}
}

// Add attaches a node's MemberList to the network.
func (c *Cluster) Add(m *MemberList) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.members[m.self] = m
}

// GossipRound performs one epidemic round: every node picks up to fanout
// random peers and pushes its full state view to them (push gossip).
// Returns the total number of state records applied across the network.
func (c *Cluster) GossipRound(fanout int) int {
	c.mu.RLock()
	members := make([]*MemberList, 0, len(c.members))
	for _, m := range c.members {
		members = append(members, m)
	}
	c.mu.RUnlock()

	applied := 0
	for _, src := range members {
		view := src.Snapshot()
		for _, dst := range c.pickPeers(src.self, fanout) {
			applied += dst.Merge(view)
		}
	}
	return applied
}

// Converge runs gossip rounds until a full round applies no new states
// (all replicas agree) or maxRounds is exhausted. It returns the number of
// rounds executed and whether convergence was reached.
func (c *Cluster) Converge(fanout, maxRounds int) (rounds int, converged bool) {
	for rounds = 1; rounds <= maxRounds; rounds++ {
		if c.GossipRound(fanout) == 0 {
			return rounds, true
		}
	}
	return maxRounds, false
}

func (c *Cluster) pickPeers(self string, fanout int) []*MemberList {
	c.mu.RLock()
	defer c.mu.RUnlock()
	ids := make([]string, 0, len(c.members))
	for id := range c.members {
		if id != self {
			ids = append(ids, id)
		}
	}
	c.rnd.Shuffle(len(ids), func(i, j int) { ids[i], ids[j] = ids[j], ids[i] })
	if fanout > len(ids) {
		fanout = len(ids)
	}
	out := make([]*MemberList, 0, fanout)
	for _, id := range ids[:fanout] {
		out = append(out, c.members[id])
	}
	return out
}
