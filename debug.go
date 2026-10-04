package blkidx

// This file exposes small helpers used by the demo and tests to inspect the
// tree shape and to simulate storage corruption after the root hash has been
// committed. They are not needed by verifiers.

// Reset clears recorded access statistics.
func (s *VerifyStats) Reset() { s.reset() }

// LocateLeaf returns the index of the leaf block that would hold key.
func (ix *Index) LocateLeaf(key string) int {
	leafIdx, _, _ := ix.locate(key)
	return leafIdx
}

// LeafParent returns the parent internal node of a leaf and the leaf's slot
// within it.
func (ix *Index) LeafParent(leafIdx int) (nodeID uint32, slot int) {
	return ix.leafParent[leafIdx], ix.leafSlot[leafIdx]
}

// NumChildren returns the number of children of an internal node.
func (ix *Index) NumChildren(nodeID uint32) int {
	return len(ix.nodes[nodeID].Children)
}

// CorruptChildCount adds delta to one child count inside an internal summary
// node, simulating metadata-only storage corruption after commitment.
func (ix *Index) CorruptChildCount(nodeID uint32, childIdx int, delta int32) {
	c := &ix.nodes[nodeID].Children[childIdx]
	c.Count = uint32(int32(c.Count) + delta)
}
