package blkidx

import (
	"fmt"
	"sort"
)

// Config tunes the index layout.
type Config struct {
	Version uint32 // committed into every block and node hash
	LeafCap int    // max entries per leaf block
	Fanout  int    // max children per internal node
}

func (c Config) withDefaults() Config {
	if c.LeafCap <= 0 {
		c.LeafCap = 32
	}
	if c.Fanout <= 0 {
		c.Fanout = 8
	}
	return c
}

// Index is the prover-side structure. The verifier never sees it.
type Index struct {
	cfg     Config
	entries []Entry

	leaves   [][]byte // encoded leaf blocks, by blockID
	leafDesc []ChildDesc

	nodes      map[uint32]*internalNode // internal nodes, by nodeID
	leafParent []uint32                 // leaf blockID -> parent nodeID
	leafSlot   []int                    // leaf blockID -> index within parent
	nodeParent map[uint32]uint32        // internal nodeID -> parent nodeID
	nodeSlot   map[uint32]int           // internal nodeID -> index within parent

	rootIsLeaf bool
	rootID     uint32 // internal nodeID of the root (when !rootIsLeaf)
	rootHash   [32]byte
	total      uint64
	depth      int // number of internal levels
}

// Build constructs the index over entries, which may be unordered; keys must
// be unique. The returned root hash (see RootHash) is the only value the
// verifier needs to trust.
func Build(entries []Entry, cfg Config) (*Index, error) {
	cfg = cfg.withDefaults()
	if len(entries) == 0 {
		return nil, fmt.Errorf("cannot build an index over zero entries")
	}
	sorted := make([]Entry, len(entries))
	copy(sorted, entries)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Key < sorted[j].Key })
	for i := 1; i < len(sorted); i++ {
		if sorted[i].Key == sorted[i-1].Key {
			return nil, fmt.Errorf("duplicate key %q", sorted[i].Key)
		}
	}

	ix := &Index{
		cfg:        cfg,
		entries:    sorted,
		nodes:      make(map[uint32]*internalNode),
		nodeParent: make(map[uint32]uint32),
		nodeSlot:   make(map[uint32]int),
		total:      uint64(len(sorted)),
	}

	// Leaf level.
	for start := 0; start < len(sorted); start += cfg.LeafCap {
		end := start + cfg.LeafCap
		if end > len(sorted) {
			end = len(sorted)
		}
		blockID := uint32(len(ix.leaves))
		baseOff := uint64(start)
		block := encodeLeaf(cfg.Version, blockID, baseOff, sorted[start:end])
		ix.leaves = append(ix.leaves, block)
		ix.leafDesc = append(ix.leafDesc, ChildDesc{
			Hash:    leafHash(block),
			BaseOff: baseOff,
			Count:   uint32(end - start),
			MaxKey:  sorted[end-1].Key,
		})
		ix.leafParent = append(ix.leafParent, 0)
		ix.leafSlot = append(ix.leafSlot, 0)
	}

	// Internal levels, bottom-up.
	type item struct {
		desc   ChildDesc
		isLeaf bool
		id     uint32
	}
	cur := make([]item, len(ix.leafDesc))
	for i, d := range ix.leafDesc {
		cur[i] = item{desc: d, isLeaf: true, id: uint32(i)}
	}
	nextNodeID := uint32(1)
	for len(cur) > 1 {
		var next []item
		for start := 0; start < len(cur); start += cfg.Fanout {
			end := start + cfg.Fanout
			if end > len(cur) {
				end = len(cur)
			}
			group := cur[start:end]
			children := make([]ChildDesc, len(group))
			for i, g := range group {
				children[i] = g.desc
			}
			nodeID := nextNodeID
			nextNodeID++
			node := &internalNode{Version: cfg.Version, NodeID: nodeID, Children: children}
			ix.nodes[nodeID] = node
			for i, g := range group {
				if g.isLeaf {
					ix.leafParent[g.id] = nodeID
					ix.leafSlot[g.id] = i
				} else {
					ix.nodeParent[g.id] = nodeID
					ix.nodeSlot[g.id] = i
				}
			}
			next = append(next, item{
				desc: ChildDesc{
					Hash:    node.hash(),
					BaseOff: children[0].BaseOff,
					Count:   uint32(sumCounts(children)),
					MaxKey:  children[len(children)-1].MaxKey,
				},
				id: nodeID,
			})
		}
		cur = next
		ix.depth++
	}

	root := cur[0]
	ix.rootIsLeaf = root.isLeaf
	ix.rootID = root.id
	ix.rootHash = root.desc.Hash
	return ix, nil
}

// RootHash is the commitment the verifier trusts.
func (ix *Index) RootHash() [32]byte { return ix.rootHash }

// TotalEntries is the committed number of entries.
func (ix *Index) TotalEntries() uint64 { return ix.total }

// Depth is the number of internal levels between leaves and the root.
func (ix *Index) Depth() int { return ix.depth }

// locate finds the leaf that must contain key (or prove its absence).
func (ix *Index) locate(key string) (leafIdx int, idxInLeaf int, found bool) {
	i := sort.Search(len(ix.leafDesc), func(i int) bool { return ix.leafDesc[i].MaxKey >= key })
	if i == len(ix.leafDesc) {
		i = len(ix.leafDesc) - 1
	}
	_, entries, err := decodeLeaf(ix.leaves[i])
	if err != nil {
		panic(fmt.Sprintf("prover index corrupt: leaf %d: %v", i, err))
	}
	j := lowerBound(entries, key)
	found = j < len(entries) && entries[j].Key == key
	return i, j, found
}

// leafContaining returns the index of the leaf holding global offset off.
func (ix *Index) leafContaining(off uint64) int {
	i := sort.Search(len(ix.leafDesc), func(i int) bool {
		return ix.leafDesc[i].BaseOff+uint64(ix.leafDesc[i].Count) > off
	})
	if i == len(ix.leafDesc) {
		return len(ix.leafDesc) - 1
	}
	return i
}

// rank returns the number of entries with key < key (prover side).
func (ix *Index) rank(key string) uint64 {
	leafIdx, idx, _ := ix.locate(key)
	return ix.leafDesc[leafIdx].BaseOff + uint64(idx)
}

// pathForLeaf builds the bottom-up internal path for a leaf.
func (ix *Index) pathForLeaf(leafIdx int) []PathLevel {
	if ix.rootIsLeaf {
		return nil
	}
	var path []PathLevel
	slot := ix.leafSlot[leafIdx]
	nodeID := ix.leafParent[leafIdx]
	for {
		n := ix.nodes[nodeID]
		children := make([]ChildDesc, len(n.Children))
		copy(children, n.Children)
		path = append(path, PathLevel{
			NodeID:   n.NodeID,
			Version:  n.Version,
			ChildIdx: slot,
			Children: children,
		})
		if nodeID == ix.rootID {
			return path
		}
		slot = ix.nodeSlot[nodeID]
		nodeID = ix.nodeParent[nodeID]
	}
}

// LayoutStats reports the real byte occupancy of the encoding.
type LayoutStats struct {
	Blocks        int
	InternalNodes int
	HeaderBytes   int // sum of leaf headers
	EntriesBytes  int // sum of leaf entry regions
	RestartBytes  int // sum of restart arrays
	InternalBytes int // sum of encoded internal nodes
	TotalBytes    int // whole persisted index
	RawKeyBytes   int // uncompressed key bytes
	RawValueBytes int // uncompressed value bytes
}

// Layout measures the encoded index, section by section.
func (ix *Index) Layout() LayoutStats {
	var st LayoutStats
	st.Blocks = len(ix.leaves)
	st.InternalNodes = len(ix.nodes)
	for _, b := range ix.leaves {
		hdr, _, err := decodeLeaf(b)
		if err != nil {
			panic(err)
		}
		numRestarts := (int(hdr.Count) + RestartInterval - 1) / RestartInterval
		entriesLen := len(b) - LeafHeaderSize - 4*numRestarts
		st.HeaderBytes += LeafHeaderSize
		st.EntriesBytes += entriesLen
		st.RestartBytes += 4 * numRestarts
		st.TotalBytes += len(b)
	}
	for _, n := range ix.nodes {
		st.InternalBytes += len(encodeInternal(n.Version, n.NodeID, n.Children))
	}
	st.TotalBytes += st.InternalBytes
	for _, e := range ix.entries {
		st.RawKeyBytes += len(e.Key)
		st.RawValueBytes += len(e.Value)
	}
	return st
}
