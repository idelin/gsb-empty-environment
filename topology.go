// Package migration implements a progressive migration controller that
// shifts traffic between an old routing topology (Top_Old) and a new
// routing topology (Top_New) using percentage-based canary rollout,
// while guaranteeing a single read/write channel per key at any moment
// (no dual-write split-brain).
package migration

import (
	"fmt"
	"hash/crc32"
	"sort"
	"sync"
)

// Node is a single storage member of a topology.
type Node interface {
	ID() string
	Get(key string) (string, bool)
	Set(key, value string)
	Delete(key string)
}

// MemNode is an in-memory Node implementation.
type MemNode struct {
	id   string
	mu   sync.RWMutex
	data map[string]string
}

// NewMemNode creates an in-memory node with the given id.
func NewMemNode(id string) *MemNode {
	return &MemNode{id: id, data: make(map[string]string)}
}

func (n *MemNode) ID() string { return n.id }

func (n *MemNode) Get(key string) (string, bool) {
	n.mu.RLock()
	defer n.mu.RUnlock()
	v, ok := n.data[key]
	return v, ok
}

func (n *MemNode) Set(key, value string) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.data[key] = value
}

func (n *MemNode) Delete(key string) {
	n.mu.Lock()
	defer n.mu.Unlock()
	delete(n.data, key)
}

// Len returns the number of keys stored on the node (used by demos/tests).
func (n *MemNode) Len() int {
	n.mu.RLock()
	defer n.mu.RUnlock()
	return len(n.data)
}

// Topology is a consistent-hash ring over a set of nodes.
type Topology struct {
	name     string
	replicas int
	ring     []uint32        // sorted virtual-node hashes
	vnode    map[uint32]Node // hash -> node
	members  []Node
}

// NewTopology builds a consistent-hash ring. Each physical node is
// expanded into `replicas` virtual nodes for balanced distribution.
func NewTopology(name string, nodes []Node, replicas int) (*Topology, error) {
	if len(nodes) == 0 {
		return nil, fmt.Errorf("topology %q: no nodes", name)
	}
	if replicas <= 0 {
		replicas = 100
	}
	t := &Topology{
		name:     name,
		replicas: replicas,
		vnode:    make(map[uint32]Node),
		members:  append([]Node(nil), nodes...),
	}
	for _, n := range nodes {
		for i := 0; i < replicas; i++ {
			h := crc32.ChecksumIEEE([]byte(fmt.Sprintf("%s#%d", n.ID(), i)))
			t.ring = append(t.ring, h)
			t.vnode[h] = n
		}
	}
	sort.Slice(t.ring, func(i, j int) bool { return t.ring[i] < t.ring[j] })
	return t, nil
}

// Name returns the topology name.
func (t *Topology) Name() string { return t.name }

// Nodes returns the physical members of the topology.
func (t *Topology) Nodes() []Node { return append([]Node(nil), t.members...) }

// Route maps a key to the node responsible for it.
func (t *Topology) Route(key string) Node {
	h := crc32.ChecksumIEEE([]byte(key))
	i := sort.Search(len(t.ring), func(i int) bool { return t.ring[i] >= h })
	if i == len(t.ring) {
		i = 0
	}
	return t.vnode[t.ring[i]]
}

func (t *Topology) Get(key string) (string, bool) { return t.Route(key).Get(key) }
func (t *Topology) Set(key, value string)         { t.Route(key).Set(key, value) }
func (t *Topology) Delete(key string)             { t.Route(key).Delete(key) }
