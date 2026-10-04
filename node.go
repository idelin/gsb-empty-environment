package readrepair

import (
	"sync"
	"time"
)

// Node simulates a single replica. All writes (client writes and read
// repairs) go through the same conditional merge, so a stale repair can
// never overwrite a newer or concurrent state, and a tombstone is never
// resurrected by an older value.
type Node struct {
	ID string

	// Latency, if non-nil, simulates per-request network/processing delay.
	Latency func() time.Duration

	// HookRepair, if set, runs at the start of each Repair call before the
	// merge. Tests use it to orchestrate deterministic interleavings.
	HookRepair func()

	mu   sync.Mutex
	data map[string][]Version
}

// NewNode creates an empty replica.
func NewNode(id string) *Node {
	return &Node{ID: id, data: make(map[string][]Version)}
}

func (n *Node) delay() {
	if n.Latency != nil {
		time.Sleep(n.Latency())
	}
}

// Get returns the current sibling set for key.
func (n *Node) Get(key string) []Version {
	n.delay()
	n.mu.Lock()
	defer n.mu.Unlock()
	return append([]Version(nil), n.data[key]...)
}

// Put stores a client write. The caller supplies a clock that already
// descends from everything it has read; the node merges conditionally.
func (n *Node) Put(key string, v Version) []Version {
	n.delay()
	n.mu.Lock()
	defer n.mu.Unlock()
	merged := mergeVersions(n.data[key], []Version{v})
	n.data[key] = merged
	return append([]Version(nil), merged...)
}

// Repair applies a read-repair payload conditionally: the incoming versions
// are merged with whatever the node holds now. If the node was updated
// (possibly concurrently) after the coordinator read it, that newer or
// concurrent state survives the merge.
func (n *Node) Repair(key string, vs []Version) []Version {
	n.delay()
	if n.HookRepair != nil {
		n.HookRepair()
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	merged := mergeVersions(n.data[key], vs)
	n.data[key] = merged
	return append([]Version(nil), merged...)
}

// state is a test/helper accessor without simulated latency.
func (n *Node) state(key string) []Version {
	n.mu.Lock()
	defer n.mu.Unlock()
	return append([]Version(nil), n.data[key]...)
}
