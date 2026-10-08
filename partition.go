package graphcontract

import "fmt"

// Partition assigns every node of a graph to exactly one group. Groups are
// identified by their index 0..NumGroups()-1.
type Partition struct {
	groups  [][]NodeID
	groupOf map[NodeID]int
}

// NewPartition builds a partition from explicit groups. It rejects empty
// groups and nodes assigned to more than one group.
func NewPartition(groups [][]NodeID) (*Partition, error) {
	if len(groups) == 0 {
		return nil, fmt.Errorf("graphcontract: partition must contain at least one group")
	}
	p := &Partition{groupOf: make(map[NodeID]int)}
	for i, members := range groups {
		if len(members) == 0 {
			return nil, fmt.Errorf("graphcontract: group %d is empty", i)
		}
		cp := make([]NodeID, len(members))
		copy(cp, members)
		p.groups = append(p.groups, cp)
		for _, n := range members {
			if prev, dup := p.groupOf[n]; dup {
				return nil, fmt.Errorf("graphcontract: node %d appears in groups %d and %d", n, prev, i)
			}
			p.groupOf[n] = i
		}
	}
	return p, nil
}

// NumGroups returns the number of groups in the partition.
func (p *Partition) NumGroups() int { return len(p.groups) }

// GroupOf returns the group index of n and whether n is partitioned at all.
func (p *Partition) GroupOf(n NodeID) (int, bool) {
	g, ok := p.groupOf[n]
	return g, ok
}

// Members returns a copy of the members of group i.
func (p *Partition) Members(i int) []NodeID {
	out := make([]NodeID, len(p.groups[i]))
	copy(out, p.groups[i])
	return out
}

// Groups returns a copy of all groups.
func (p *Partition) Groups() [][]NodeID {
	out := make([][]NodeID, len(p.groups))
	for i := range p.groups {
		out[i] = p.Members(i)
	}
	return out
}

// ComposePartitions composes p1, a partition of the original nodes, with p2,
// a partition of p1's groups (p2 ranges over node IDs 0..p1.NumGroups()-1).
// The result partitions the original nodes by p2.GroupOf(p1.GroupOf(n)).
func ComposePartitions(p1, p2 *Partition) (*Partition, error) {
	groups := make([][]NodeID, p2.NumGroups())
	for _, members := range p1.groups {
		for _, n := range members {
			g1, _ := p1.GroupOf(n)
			g2, ok := p2.GroupOf(NodeID(g1))
			if !ok {
				return nil, fmt.Errorf("graphcontract: group %d of first partition is not covered by second partition", g1)
			}
			groups[g2] = append(groups[g2], n)
		}
	}
	return NewPartition(groups)
}
