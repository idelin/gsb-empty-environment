package bgpsim

import (
	"fmt"
	"sort"
)

// Node describes one BGP speaker in the simulated AS.
//
// Roles are intentionally flags rather than an enum because a route
// reflector may simultaneously be an AS border router, and a border
// router may be a route-reflector client. Client membership itself is
// expressed through Config.Clusters.
type Node struct {
	ID       NodeID   `json:"id"`
	RouterID RouterID `json:"router_id,omitempty"`

	// Reflector marks a route reflector. Its own ClusterID is taken from
	// the matching Cluster configuration (or Node.ClusterID as fallback).
	Reflector bool `json:"reflector,omitempty"`

	// Border marks an AS border router that receives ExternalRoutes.
	// Externally injected routes are stable origin advertisements: the
	// border always originates its best external route for a prefix,
	// matching eBGP administrative-distance preference.
	Border bool `json:"border,omitempty"`

	// ClusterID optionally fixes the reflector's own cluster id when no
	// Cluster entry references it as a reflector.
	ClusterID ClusterID `json:"cluster_id,omitempty"`
}

// Cluster defines a route-reflection cluster: one shared ClusterID served
// by one or more Reflectors and a set of Clients. Every reflector is
// peered with every listed client; reflectors are additionally placed in
// a standard full iBGP mesh. A client may appear in several clusters to
// model simultaneous direct sessions to multiple reflectors.
type Cluster struct {
	ID         ClusterID `json:"id"`
	Reflectors []NodeID  `json:"reflectors"`
	Clients    []NodeID  `json:"clients,omitempty"`
}

// Options controls selection and simulation behavior.
type Options struct {
	// AlwaysCompareMED enables "bgp always-compare-med": MED is compared
	// across different neighboring AS numbers, making the preference
	// relation total with respect to MED.
	AlwaysCompareMED bool `json:"always_compare_med,omitempty"`

	// MaxRounds bounds synchronous evolution. Default 256.
	MaxRounds int `json:"max_rounds,omitempty"`
}

// Config is the complete input to Simulate.
type Config struct {
	Nodes          []Node                    `json:"nodes"`
	Clusters       []Cluster                 `json:"clusters,omitempty"`
	ExternalRoutes []Route                   `json:"external_routes,omitempty"`
	IGP            map[NodeID]map[NodeID]int `json:"igp,omitempty"`
	ExtraPeers     [][2]NodeID               `json:"extra_peers,omitempty"`
	Options        Options                   `json:"options,omitempty"`
}

// DenseIGP builds a sparse IGP map from a dense cost matrix aligned with
// ids. m[i][j] is the shortest-path IGP cost from ids[i] to ids[j].
func DenseIGP(ids []NodeID, m [][]int) (map[NodeID]map[NodeID]int, error) {
	if len(m) != len(ids) {
		return nil, fmt.Errorf("bgpsim: IGP matrix has %d rows for %d node ids", len(m), len(ids))
	}
	out := make(map[NodeID]map[NodeID]int, len(ids))
	for i, from := range ids {
		if len(m[i]) != len(ids) {
			return nil, fmt.Errorf("bgpsim: IGP row %d has length %d, want %d", i, len(m[i]), len(ids))
		}
		row := make(map[NodeID]int, len(ids))
		for j, to := range ids {
			row[to] = m[i][j]
		}
		out[from] = row
	}
	return out, nil
}

func (c *Config) nodeByID() map[NodeID]*Node {
	m := make(map[NodeID]*Node, len(c.Nodes))
	for i := range c.Nodes {
		m[c.Nodes[i].ID] = &c.Nodes[i]
	}
	return m
}

// sortedNodeIDs returns node ids in lexical order, which is the stable
// iteration order used by fingerprints and reports.
func (c *Config) sortedNodeIDs() []NodeID {
	ids := make([]NodeID, 0, len(c.Nodes))
	for _, n := range c.Nodes {
		ids = append(ids, n.ID)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	return ids
}

// buildPeers derives the undirected BGP session graph and the cluster id
// owned by each reflector.
func (c *Config) buildPeers(nodes map[NodeID]*Node) (map[NodeID]map[NodeID]bool, map[NodeID]ClusterID, error) {
	peers := make(map[NodeID]map[NodeID]bool, len(nodes))
	for id := range nodes {
		peers[id] = make(map[NodeID]bool)
	}
	addEdge := func(a, b NodeID) error {
		if a == b {
			return nil
		}
		if _, ok := nodes[a]; !ok {
			return fmt.Errorf("bgpsim: peer endpoint %q is not a node", a)
		}
		if _, ok := nodes[b]; !ok {
			return fmt.Errorf("bgpsim: peer endpoint %q is not a node", b)
		}
		peers[a][b] = true
		peers[b][a] = true
		return nil
	}

	rrCluster := make(map[NodeID]ClusterID)
	assignCluster := func(rr NodeID, cid ClusterID) error {
		if old, ok := rrCluster[rr]; ok && old != cid {
			return fmt.Errorf("bgpsim: reflector %q assigned conflicting cluster ids %q and %q", rr, old, cid)
		}
		rrCluster[rr] = cid
		return nil
	}

	var reflectors []NodeID
	for _, n := range c.Nodes {
		if n.Reflector {
			reflectors = append(reflectors, n.ID)
			if n.ClusterID != "" {
				if err := assignCluster(n.ID, n.ClusterID); err != nil {
					return nil, nil, err
				}
			}
		}
	}

	for _, cl := range c.Clusters {
		if cl.ID == "" {
			return nil, nil, fmt.Errorf("bgpsim: cluster with reflectors %v has empty id", cl.Reflectors)
		}
		if len(cl.Reflectors) == 0 {
			return nil, nil, fmt.Errorf("bgpsim: cluster %q has no reflectors", cl.ID)
		}
		for _, rr := range cl.Reflectors {
			if _, ok := nodes[rr]; !ok {
				return nil, nil, fmt.Errorf("bgpsim: cluster %q references unknown reflector %q", cl.ID, rr)
			}
			if !nodes[rr].Reflector {
				return nil, nil, fmt.Errorf("bgpsim: node %q used as reflector in cluster %q but Reflector=false", rr, cl.ID)
			}
			if err := assignCluster(rr, cl.ID); err != nil {
				return nil, nil, err
			}
			for _, client := range cl.Clients {
				if _, ok := nodes[client]; !ok {
					return nil, nil, fmt.Errorf("bgpsim: cluster %q references unknown client %q", cl.ID, client)
				}
				if err := addEdge(rr, client); err != nil {
					return nil, nil, err
				}
			}
		}
	}

	// Standard route-reflector topology: all reflectors form an iBGP mesh.
	for i := 0; i < len(reflectors); i++ {
		for j := i + 1; j < len(reflectors); j++ {
			if err := addEdge(reflectors[i], reflectors[j]); err != nil {
				return nil, nil, err
			}
		}
	}

	for _, e := range c.ExtraPeers {
		if err := addEdge(e[0], e[1]); err != nil {
			return nil, nil, err
		}
	}
	return peers, rrCluster, nil
}

func (c *Config) validate(nodes map[NodeID]*Node, rrCluster map[NodeID]ClusterID, prefixes []string) error {
	if len(c.Nodes) == 0 {
		return fmt.Errorf("bgpsim: Config.Nodes must not be empty")
	}
	seen := make(map[NodeID]bool, len(c.Nodes))
	for _, n := range c.Nodes {
		if n.ID == "" {
			return fmt.Errorf("bgpsim: node with empty id")
		}
		if seen[n.ID] {
			return fmt.Errorf("bgpsim: duplicate node id %q", n.ID)
		}
		seen[n.ID] = true
		if n.Reflector {
			if _, ok := rrCluster[n.ID]; !ok {
				return fmt.Errorf("bgpsim: reflector %q has no cluster id (set Node.ClusterID or reference it from a Cluster)", n.ID)
			}
		}
	}

	egress := make(map[NodeID]bool)
	for _, r := range c.ExternalRoutes {
		if r.Prefix == "" {
			return fmt.Errorf("bgpsim: external route at %q has empty Prefix", r.Egress)
		}
		if r.Egress == "" {
			return fmt.Errorf("bgpsim: external route %s has empty Egress", r.Prefix)
		}
		n, ok := nodes[r.Egress]
		if !ok {
			return fmt.Errorf("bgpsim: route %s references unknown egress %q", r.Prefix, r.Egress)
		}
		_ = n
		egress[r.Egress] = true
	}

	// Every internal speaker may need the IGP cost to every egress.
	for id := range nodes {
		for e := range egress {
			if id == e {
				continue
			}
			if c.IGP == nil || c.IGP[id] == nil {
				return fmt.Errorf("bgpsim: missing IGP row for node %q", id)
			}
			if _, ok := c.IGP[id][e]; !ok {
				return fmt.Errorf("bgpsim: missing IGP cost from %q to egress %q", id, e)
			}
		}
	}
	_ = prefixes
	return nil
}
