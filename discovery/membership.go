package discovery

import (
	"math/rand"
	"sync"
	"time"
)

func init() {
	rand.Seed(time.Now().UnixNano())
}

// Table is the local membership view of a node. It is safe for
// concurrent use by the gossip loop, the RPC server and callers.
type Table struct {
	mu      sync.RWMutex
	self    NodeMeta
	members map[string]NodeMeta
}

// NewTable creates a membership table seeded with the node itself.
func NewTable(self NodeMeta) *Table {
	return &Table{
		self:    self,
		members: map[string]NodeMeta{self.ID: self},
	}
}

// Self returns the metadata of the local node.
func (t *Table) Self() NodeMeta {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.self
}

// Merge folds remote metadata into the table and reports how many
// previously unknown members were added. Higher incarnation wins;
// ties are broken by LastSeen. Entries about the local node are
// ignored so remote views can never clobber our own identity.
func (t *Table) Merge(metas []NodeMeta) int {
	t.mu.Lock()
	defer t.mu.Unlock()
	added := 0
	for _, m := range metas {
		if m.ID == "" || m.Addr == "" || m.ID == t.self.ID {
			continue
		}
		cur, ok := t.members[m.ID]
		if !ok {
			t.members[m.ID] = m
			added++
			continue
		}
		if m.Incarnation > cur.Incarnation ||
			(m.Incarnation == cur.Incarnation && m.LastSeen > cur.LastSeen) {
			t.members[m.ID] = m
		}
	}
	return added
}

// Size returns the number of known members, including the local node.
func (t *Table) Size() int {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return len(t.members)
}

// Has reports whether the given member ID is known.
func (t *Table) Has(id string) bool {
	t.mu.RLock()
	defer t.mu.RUnlock()
	_, ok := t.members[id]
	return ok
}

// Snapshot returns the full membership view, including the local node.
func (t *Table) Snapshot() []NodeMeta {
	t.mu.RLock()
	defer t.mu.RUnlock()
	out := make([]NodeMeta, 0, len(t.members))
	for _, m := range t.members {
		out = append(out, m)
	}
	return out
}

// Sample returns up to n randomly shuffled members, including the
// local node. Used as the payload of gossip exchanges.
func (t *Table) Sample(n int) []NodeMeta {
	all := t.Snapshot()
	if len(all) <= n {
		return all
	}
	perm := rand.Perm(len(all))
	out := make([]NodeMeta, 0, n)
	for i := 0; i < n; i++ {
		out = append(out, all[perm[i]])
	}
	return out
}

// RandomPeer picks a random member other than the local node.
func (t *Table) RandomPeer() (NodeMeta, bool) {
	t.mu.RLock()
	defer t.mu.RUnlock()
	var peers []NodeMeta
	for _, m := range t.members {
		if m.ID != t.self.ID {
			peers = append(peers, m)
		}
	}
	if len(peers) == 0 {
		return NodeMeta{}, false
	}
	return peers[rand.Intn(len(peers))], true
}
