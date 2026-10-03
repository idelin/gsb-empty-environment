package gsb

import "sort"

// Node maintains a local membership view plus its incremental digest.
type Node struct {
	ID      string
	members map[string]Member
	digest  Digest
}

// NewNode creates a node that starts out knowing only itself.
func NewNode(id string) *Node {
	n := &Node{ID: id, members: make(map[string]Member)}
	n.Upsert(Member{ID: id, State: Alive, Incarnation: 1})
	return n
}

// Upsert inserts or updates a member, maintaining the digest incrementally.
// Stale updates (lower incarnation, or same incarnation with a less severe
// state) are ignored.
func (n *Node) Upsert(m Member) bool {
	if old, ok := n.members[m.ID]; ok {
		if !m.overrides(old) {
			return false
		}
		n.digest.Remove(old)
	}
	n.members[m.ID] = m
	n.digest.Add(m)
	return true
}

// Merge folds a batch of remote members into the local view and returns
// how many entries actually changed.
func (n *Node) Merge(remote []Member) int {
	changed := 0
	for _, m := range remote {
		if n.Upsert(m) {
			changed++
		}
	}
	return changed
}

// DigestSum is the 4-byte CRC advertised in probe messages.
func (n *Node) DigestSum() uint32 { return n.digest.Sum() }

// Get returns the local view of one member.
func (n *Node) Get(id string) (Member, bool) {
	m, ok := n.members[id]
	return m, ok
}

// Members returns the view sorted by member ID.
func (n *Node) Members() []Member {
	out := make([]Member, 0, len(n.members))
	for _, m := range n.members {
		out = append(out, m)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// membersInSegments returns the members whose IDs hash into the given
// ring intervals — the payload of an interval reconciliation pull.
func (n *Node) membersInSegments(segs []int) []Member {
	want := make(map[uint32]bool, len(segs))
	for _, s := range segs {
		want[uint32(s)] = true
	}
	var out []Member
	for _, m := range n.members {
		if want[segmentIndex(m.ID)] {
			out = append(out, m)
		}
	}
	return out
}
