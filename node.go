package sim

// VersionedValue is a value tagged with a monotonically increasing version
// issued by the cluster's single sequencer.
type VersionedValue struct {
	Val string
	Ver uint64
}

// Node is a storage replica. It may crash (Down) and recover.
type Node struct {
	ID   string
	Down bool
	Data map[string]VersionedValue

	// StaleRejected counts writes/repairs refused because the replica
	// already holds an equal or newer version (safety evidence).
	StaleRejected int
}

func NewNode(id string) *Node {
	return &Node{ID: id, Data: map[string]VersionedValue{}}
}

// Apply stores v only if it is strictly newer than the stored version.
// This is the safety kernel: a stale repair (or delayed old write) can
// never overwrite a newer value. Returns true when the value was stored.
func (n *Node) Apply(key string, v VersionedValue) bool {
	if n.Down {
		return false
	}
	cur, ok := n.Data[key]
	if ok && cur.Ver >= v.Ver {
		n.StaleRejected++
		return false
	}
	n.Data[key] = v
	return true
}

// Get returns the stored value for key.
func (n *Node) Get(key string) (VersionedValue, bool) {
	v, ok := n.Data[key]
	return v, ok
}
