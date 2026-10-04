package snapdelta

// OpType is the kind of a delta operation.
type OpType byte

const (
	// OpPut sets a key to a value.
	OpPut OpType = iota
	// OpDelete removes a key.
	OpDelete
)

// Op is a single delta operation. Ops are applied in list order, so
// repeated operations on the same key are well defined (last one wins).
type Op struct {
	Type  OpType
	Key   uint16
	Value []byte // only meaningful for OpPut
}

// Entry is a key/value pair. In a Proof, OldEntries lists the pre-delta
// values of touched keys that existed; a touched key missing from
// OldEntries is claimed to have been absent.
type Entry struct {
	Key   uint16
	Value []byte
}

// ProofNode is an inherited block: the hash of an untouched subtree.
// Nodes must be listed in canonical order, strictly increasing by
// (Depth, Index).
type ProofNode struct {
	Depth uint8  // 0 = root
	Index uint32 // position within the level
	Hash  Hash
}

// Proof binds a delta to an old/new version pair and a key range, and
// carries everything needed to recompute both roots.
type Proof struct {
	OldVersion uint64
	NewVersion uint64 // must be greater than OldVersion
	RangeStart uint16 // inclusive
	RangeEnd   uint16 // inclusive
	// OldEntries holds the pre-delta values of touched keys, sorted by
	// key with no duplicates.
	OldEntries []Entry
	// Nodes are the inherited blocks, sorted by (Depth, Index).
	Nodes []ProofNode
}

// Delta is the declared state transition.
type Delta struct {
	// NewRoot is the claimed post-delta root.
	NewRoot Hash
	// Ops are applied in order; duplicate keys are allowed.
	Ops []Op
}
