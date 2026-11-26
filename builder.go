package kt

// BuildConsistencyProof constructs a minimal cross-epoch consistency proof
// between two trees using the migration logs of every epoch in (from, to].
// Changed regions become BoundaryChanged nodes at the topmost migrated prefix;
// every other full-depth position becomes an unchanged BoundaryLeaf node
// (empty positions carry the zero LeafValue). The result is the unique minimal
// boundary set that completely covers the label space, so it contains no
// redundant nodes.
func BuildConsistencyProof(from, to *Tree, logs []*MigrationLog) (*ConsistencyProof, error) {
	if from == nil || to == nil {
		return nil, ErrNilEntry
	}
	if from.Epoch() >= to.Epoch() || from.LabelDepth() != to.LabelDepth() {
		return nil, ErrProofEpoch
	}
	depth := from.LabelDepth()
	changedTop := map[string]BitPrefix{}
	// firstOld keeps the From-epoch subtree hash; chainNew accumulates the
	// hash produced by the most recent migration at each prefix.
	firstOld := map[string]Hash{}
	chainNew := map[string]Hash{}

	// Collect the topmost migrated prefixes across the interval. A nested
	// migration is subsumed by an earlier ancestor boundary. For a prefix
	// migrated in multiple epochs we track the full chain so the boundary's
	// NewHash is the hash produced by the final migration.
	for _, log := range logs {
		if log == nil || log.Epoch <= from.Epoch() || log.Epoch > to.Epoch() {
			return nil, ErrMigrationEpoch
		}
		for i := range log.Entries {
			m := &log.Entries[i]
			covered := false
			for _, anc := range changedTop {
				if anc.IsPrefixOf(m.Prefix) && anc.Len() < m.Prefix.Len() {
					covered = true
					break
				}
			}
			if covered {
				continue
			}
			for k, existing := range changedTop {
				if m.Prefix.IsPrefixOf(existing) && m.Prefix.Len() < existing.Len() {
					delete(changedTop, k)
					delete(firstOld, k)
					delete(chainNew, k)
				}
			}
			key := m.Prefix.String()
			changedTop[key] = m.Prefix
			if _, seen := firstOld[key]; !seen {
				oldH, _, err := from.SubtreeHash(m.Prefix)
				if err != nil {
					return nil, err
				}
				firstOld[key] = oldH
			}
			if m.Op == OpRevokeOut {
				chainNew[key] = recomputeEmpty(m.Epoch, depth, m.Prefix)
			} else {
				chainNew[key] = m.NewHash
			}
			// The boundary's NewHash must be the subtree hash inside the final
			// (To) epoch so root reconstruction succeeds; remember it last.
			toHash, _, err := to.SubtreeHash(m.Prefix)
			if err != nil {
				return nil, err
			}
			chainNew[key] = toHash
		}
	}

	p := &ConsistencyProof{
		FromEpoch:  from.Epoch(),
		ToEpoch:    to.Epoch(),
		LabelDepth: depth,
		FromRoot:   from.RootHash(),
		ToRoot:     to.RootHash(),
	}

	// Recursively walk the label space, stopping at topmost changed prefixes
	// and otherwise descending to full-depth leaf boundaries. A region empty
	// in both trees collapses to a single empty-subtree boundary at its
	// highest position, keeping the boundary set minimal.
	var walk func(prefix BitPrefix) error
	walk = func(prefix BitPrefix) error {
		if cp, ok := changedTop[prefix.String()]; ok && cp.Equal(prefix) {
			key := prefix.String()
			hs := [2]Hash{firstOld[key], chainNew[key]}
			p.Boundaries = append(p.Boundaries, BoundaryNode{
				Kind:    BoundaryChanged,
				Prefix:  prefix,
				OldHash: hs[0],
				NewHash: hs[1],
			})
			return nil
		}
		// Collapse a strictly-empty region before descending, so no child
		// boundary can overlap this one.
		if canCollapseEmpty(from, to, changedTop, prefix) {
			p.Boundaries = append(p.Boundaries, BoundaryNode{
				Kind:   BoundaryEmpty,
				Prefix: prefix,
			})
			return nil
		}
		if prefix.Len() == depth {
			ov, ok1 := from.Lookup(prefix)
			nv, ok2 := to.Lookup(prefix)
			if !ok1 || !ok2 || !ov.Equal(nv) {
				return ErrLeafChanged
			}
			p.Boundaries = append(p.Boundaries, BoundaryNode{
				Kind:   BoundaryLeaf,
				Prefix: prefix,
				Leaf:   LabeledLeaf{Label: prefix, Value: ov},
			})
			return nil
		}
		p0, err := prefix.AppendBit(0)
		if err != nil {
			return err
		}
		p1, err := prefix.AppendBit(1)
		if err != nil {
			return err
		}
		if err := walk(p0); err != nil {
			return err
		}
		return walk(p1)
	}
	if err := walk(EmptyPrefix()); err != nil {
		return nil, err
	}
	SortPrefixes(boundaryPrefixes(p.Boundaries))
	return p, nil
}

// canCollapseEmpty reports whether prefix covers no leaf in either tree and
// no changed (migrated) topmost prefix is located beneath it.
func canCollapseEmpty(from, to *Tree, changed map[string]BitPrefix, prefix BitPrefix) bool {
	for key := range from.leaves {
		label, err := PrefixFromString(key)
		if err != nil {
			return false
		}
		if prefix.IsPrefixOf(label) {
			return false
		}
	}
	for key := range to.leaves {
		label, err := PrefixFromString(key)
		if err != nil {
			return false
		}
		if prefix.IsPrefixOf(label) {
			return false
		}
	}
	for _, cp := range changed {
		if prefix.IsPrefixOf(cp) {
			return false
		}
	}
	return true
}

func boundaryPrefixes(bs []BoundaryNode) []BitPrefix {
	out := make([]BitPrefix, len(bs))
	for i := range bs {
		out[i] = bs[i].Prefix
	}
	return out
}

// PrefixFromString parses a 0/1 bit string into a BitPrefix.
func PrefixFromString(bits string) (BitPrefix, error) {
	p := EmptyPrefix()
	for _, c := range bits {
		if c != '0' && c != '1' {
			return BitPrefix{}, ErrPrefixEncoding
		}
		next, err := p.AppendBit(int(c - '0'))
		if err != nil {
			return BitPrefix{}, err
		}
		p = next
	}
	return p, nil
}
