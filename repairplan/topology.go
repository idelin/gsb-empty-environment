// Package repairplan provides fault-domain-aware repair planning and
// per-round bandwidth scheduling for erasure-coded and replicated stripes.
//
// The planner consumes a Root -> Row -> Rack -> Host -> OSD topology tree
// and a set of degraded stripes, then produces a round-by-round repair
// timeline that honors fault-domain anti-affinity, per-node capacity,
// per-node ingress/egress bandwidth limits and concurrency caps.
package repairplan

import (
	"errors"
	"fmt"
	"sort"
)

// NodeLevel identifies a level in the topology tree.
type NodeLevel string

const (
	LevelRoot NodeLevel = "root"
	LevelRow  NodeLevel = "row"
	LevelRack NodeLevel = "rack"
	LevelHost NodeLevel = "host"
	LevelOSD  NodeLevel = "osd"
)

// levelOrder gives the fixed depth of each topology level.
var levelOrder = map[NodeLevel]int{
	LevelRoot: 0,
	LevelRow:  1,
	LevelRack: 2,
	LevelHost: 3,
	LevelOSD:  4,
}

// Unlimited marks an unbounded capacity or bandwidth limit.
const Unlimited int64 = -1

// Node is a single vertex of the topology tree. Bandwidth limits are
// expressed in bytes transferred during one scheduling round; a value of
// Unlimited means the node does not constrain traffic at that level.
type Node struct {
	ID                string
	Level             NodeLevel
	ParentID          string
	Online            bool
	RemainingCapacity int64 // bytes; Unlimited means not constrained
	IngressLimit      int64 // bytes per round; Unlimited means not constrained
	EgressLimit       int64 // bytes per round; Unlimited means not constrained
	MaxConcurrent     int   // >0 caps repair tasks touching the node in one round; <=0: no cap
}

// Topology is the validated, indexed topology tree.
type Topology struct {
	nodes map[string]*Node
	paths map[string][]*Node // root -> node path for every node
	osds  []string
}

// NewTopology validates the given nodes and builds an indexed topology.
// The tree must contain exactly one root, every non-root node must point at
// an existing parent one level above it, and cycles are rejected.
func NewTopology(nodes []Node) (*Topology, error) {
	if len(nodes) == 0 {
		return nil, errors.New("repairplan: topology must not be empty")
	}
	t := &Topology{
		nodes: make(map[string]*Node, len(nodes)),
		paths: make(map[string][]*Node, len(nodes)),
	}
	roots := 0
	for i := range nodes {
		n := nodes[i]
		if n.ID == "" {
			return nil, errors.New("repairplan: topology node has empty ID")
		}
		if _, ok := levelOrder[n.Level]; !ok {
			return nil, fmt.Errorf("repairplan: node %q has unknown level %q", n.ID, n.Level)
		}
		if _, dup := t.nodes[n.ID]; dup {
			return nil, fmt.Errorf("repairplan: duplicate topology node %q", n.ID)
		}
		if n.ParentID == "" {
			if n.Level != LevelRoot {
				return nil, fmt.Errorf("repairplan: node %q lacks a parent but is not root", n.ID)
			}
			roots++
		}
		t.nodes[n.ID] = &n
	}
	if roots != 1 {
		return nil, fmt.Errorf("repairplan: topology must have exactly one root, found %d", roots)
	}

	// Resolve parent links and root->node paths.
	for id := range t.nodes {
		seen := map[string]bool{}
		path := []*Node{}
		cur := id
		for {
			if seen[cur] {
				return nil, fmt.Errorf("repairplan: topology cycle detected at %q", cur)
			}
			seen[cur] = true
			n, ok := t.nodes[cur]
			if !ok {
				return nil, fmt.Errorf("repairplan: node %q references unknown parent %q", id, cur)
			}
			path = append([]*Node{n}, path...)
			if n.Level == LevelRoot {
				if n.ParentID != "" {
					return nil, fmt.Errorf("repairplan: root node %q must not have a parent", n.ID)
				}
				break
			}
			parent, ok := t.nodes[n.ParentID]
			if !ok {
				return nil, fmt.Errorf("repairplan: node %q references unknown parent %q", n.ID, n.ParentID)
			}
			if levelOrder[parent.Level] != levelOrder[n.Level]-1 {
				return nil, fmt.Errorf("repairplan: node %q level %q cannot hang under parent %q level %q",
					n.ID, n.Level, parent.ID, parent.Level)
			}
			cur = parent.ID
		}
		t.paths[id] = path
	}

	for id, n := range t.nodes {
		if n.Level == LevelOSD {
			if len(t.paths[id]) != len(levelOrder) {
				return nil, fmt.Errorf("repairplan: OSD %q is not reachable through the full hierarchy", id)
			}
			t.osds = append(t.osds, id)
		}
	}
	sort.Strings(t.osds)
	if len(t.osds) == 0 {
		return nil, errors.New("repairplan: topology contains no OSD nodes")
	}
	return t, nil
}

// Node returns the node with the given ID.
func (t *Topology) Node(id string) (*Node, bool) {
	n, ok := t.nodes[id]
	return n, ok
}

// OSDs returns all OSD IDs in lexicographic order.
func (t *Topology) OSDs() []string {
	out := make([]string, len(t.osds))
	copy(out, t.osds)
	return out
}

// path returns the root -> OSD path.
func (t *Topology) path(osdID string) []*Node {
	return t.paths[osdID]
}

// EffectiveOnline reports whether the OSD and every ancestor are online.
// A shard stored on an OSD that is offline (or whose rack/host is offline)
// is treated as lost rather than surviving.
func (t *Topology) EffectiveOnline(osdID string) bool {
	for _, n := range t.paths[osdID] {
		if !n.Online {
			return false
		}
	}
	return true
}

// Ancestor returns the ancestor node of the OSD at the given level.
func (t *Topology) Ancestor(osdID string, level NodeLevel) (*Node, error) {
	for _, n := range t.paths[osdID] {
		if n.Level == level {
			return n, nil
		}
	}
	return nil, fmt.Errorf("repairplan: OSD %q has no ancestor at level %q", osdID, level)
}

// RackOf returns the rack ID hosting the OSD.
func (t *Topology) RackOf(osdID string) string {
	n, err := t.Ancestor(osdID, LevelRack)
	if err != nil {
		return ""
	}
	return n.ID
}
