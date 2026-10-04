package antientropy

// Stats counts the communication performed by a sync run.
type Stats struct {
	Rounds             int // deepest recursion level that exchanged messages
	DigestExchanges    int // digest messages sent (both directions counted)
	RecordExchanges    int // leaf record-list messages sent (both directions)
	RecordsTransferred int // individual records shipped across the wire
	NodesSkipped       int // subtrees pruned because digests matched
}

// Sync reconciles stores a and b in place. Both peers build their digest
// tree with the same split rule, then recursively exchange digests:
// matching digests prune the subtree; at conflicting leaves the real
// records (tombstones included) are compared and merged. Returns the
// communication statistics for the run.
func Sync(a, b *Store, cfg Config) Stats {
	ta := buildDigestTree(a, cfg)
	tb := buildDigestTree(b, cfg)
	var st Stats
	syncNode(ta.root, tb.root, a, b, &st)
	return st
}

func syncNode(na, nb *node, a, b *Store, st *Stats) {
	st.DigestExchanges += 2 // each peer sends its digest for this node
	if na.depth+1 > st.Rounds {
		st.Rounds = na.depth + 1
	}
	if na.digest == nb.digest {
		st.NodesSkipped++
		return
	}
	if na.leaf || nb.leaf {
		// Leaf conflict: digests alone cannot decide — compare real records.
		reconcileLeaf(na, nb, a, b, st)
		return
	}
	for i := 0; i < 2; i++ {
		syncNode(na.children[i], nb.children[i], a, b, st)
	}
}

// reconcileLeaf exchanges the actual records of both peers for the leaf
// range and merges them into both stores with last-writer-wins semantics.
// The two trees may have different shapes (one side leaf, the other
// internal), so records are pulled from the stores by range, not from
// the tree nodes.
func reconcileLeaf(na, nb *node, a, b *Store, st *Stats) {
	st.RecordExchanges += 2
	aKeys := a.keysInRange(na.lo, na.hi)
	bKeys := b.keysInRange(nb.lo, nb.hi)
	st.RecordsTransferred += len(aKeys) + len(bKeys)
	for _, k := range aKeys {
		r, _ := a.Get(k)
		b.apply(r)
	}
	for _, k := range bKeys {
		r, _ := b.Get(k)
		a.apply(r)
	}
}
