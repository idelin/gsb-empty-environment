package kt

// validateMigrations enforces all topology-change constraints for one epoch.
func (v *Verifier) validateMigrations(epoch uint32, log *MigrationLog, oldTree, newTree *Tree, newCounter uint64) error {
	prefixes := make([]BitPrefix, 0, len(log.Entries))
	revokeCount := uint64(0)
	for i := range log.Entries {
		m := &log.Entries[i]
		if m.Epoch != epoch {
			return ErrMigrationEpoch
		}
		prefixes = append(prefixes, m.Prefix)
		switch m.Op {
		case OpSplit, OpMerge, OpRevokeOut:
		default:
			return ErrUnknownOp
		}
		if err := v.checkAuth(m, epoch); err != nil {
			return err
		}
		if m.Op == OpRevokeOut {
			revokeCount++
			v.graveyard[m.OldHash] = struct{}{}
			v.revokedPrefixes[m.Prefix.String()] = struct{}{}
			if err := v.checkSubtreeHashes(m, oldTree, newTree); err != nil {
				return err
			}
			continue
		}
		// Split/merge that reintroduces a previously revoked prefix must
		// never reuse an old subtree hash.
		if _, revoked := v.revokedPrefixes[m.Prefix.String()]; revoked {
			if _, buried := v.graveyard[m.NewHash]; buried {
				return ErrHashReuse
			}
			if hist := v.prefixHistory[m.Prefix.String()]; hist != nil {
				if _, seen := hist[m.NewHash]; seen {
					return ErrHashReuse
				}
			}
		}
		// Universal guard: no new subtree anywhere may resurrect a revoked
		// subtree hash, even after relabeling via split/merge.
		if _, buried := v.graveyard[m.NewHash]; buried {
			return ErrHashReuse
		}
		if err := v.checkSubtreeHashes(m, oldTree, newTree); err != nil {
			return err
		}
		if m.Op == OpMerge {
			if err := v.checkMerge(m, oldTree); err != nil {
				return err
			}
		}
		hist := v.prefixHistory[m.Prefix.String()]
		if hist == nil {
			hist = map[Hash]struct{}{}
			v.prefixHistory[m.Prefix.String()] = hist
		}
		hist[m.NewHash] = struct{}{}
	}
	if !Disjoint(prefixes) {
		return ErrMigrationPrefixes
	}
	// The directory-wide counter must rise by exactly the number of
	// revoke-out migrations performed in this epoch.
	if newCounter != v.lastCounter+revokeCount {
		return ErrCounterNotMonotonic
	}
	return nil
}

// checkAuth validates the fresh issuance/authorization attached to a
// migration: trusted issuer, correct epoch/prefix/op, unique nonce and
// strictly increasing per-issuer serial.
func (v *Verifier) checkAuth(m *Migration, epoch uint32) error {
	if m.Auth == nil {
		return ErrMissingAuth
	}
	if err := m.Auth.Verify(v.issuers); err != nil {
		return err
	}
	rec := m.Auth.Record
	if rec.Epoch != epoch || !rec.Prefix.Equal(m.Prefix) || rec.Op != m.Op {
		return ErrAuthEpoch
	}
	if m.Op != OpRevokeOut && rec.NewHash != m.NewHash {
		return ErrMigrationHash
	}
	nonceKey := string(m.Auth.Issuer) + ":" + string(rec.Nonce)
	if _, used := v.usedNonces[nonceKey]; used {
		return ErrAuthReplay
	}
	issuerKey := string(m.Auth.Issuer)
	if last, ok := v.lastSerial[issuerKey]; ok && rec.Serial <= last {
		return ErrAuthReplay
	}
	v.usedNonces[nonceKey] = struct{}{}
	v.lastSerial[issuerKey] = rec.Serial
	return nil
}

// checkSubtreeHashes verifies declared OldHash/NewHash against the actual
// epoch trees when they are provided, and enforces structural shape rules.
func (v *Verifier) checkSubtreeHashes(m *Migration, oldTree, newTree *Tree) error {
	switch m.Op {
	case OpRevokeOut:
		if m.NewHash != (Hash{}) {
			return ErrMigrationHash
		}
		if oldTree != nil {
			got, _, err := oldTree.SubtreeHash(m.Prefix)
			if err != nil {
				return err
			}
			if got != m.OldHash {
				return ErrMigrationHash
			}
		}
		if newTree != nil {
			// After migrating out, the region must be structurally empty:
			// recompute the fully-expanded empty subtree and compare.
			want := recomputeEmpty(newTree.Epoch(), newTree.LabelDepth(), m.Prefix)
			got, _, err := newTree.SubtreeHash(m.Prefix)
			if err != nil {
				return err
			}
			if got != want {
				return ErrMigrationHash
			}
		}
	case OpSplit, OpMerge:
		if m.NewHash == (Hash{}) {
			return ErrMigrationHash
		}
		if newTree != nil {
			got, _, err := newTree.SubtreeHash(m.Prefix)
			if err != nil {
				return err
			}
			if got != m.NewHash {
				return ErrMigrationHash
			}
		}
		if m.Op == OpSplit && oldTree != nil {
			got, _, err := oldTree.SubtreeHash(m.Prefix)
			if err != nil {
				return err
			}
			// Epoch binding: an empty old subtree is represented by the empty
			// commitment of the migration epoch; a populated one must match.
			emptyOld := recomputeEmpty(oldTree.Epoch(), v.labelDepth, m.Prefix)
			emptyNow := recomputeEmpty(m.Epoch, v.labelDepth, m.Prefix)
			if got != m.OldHash && !(got == emptyOld && m.OldHash == emptyNow) {
				return ErrMigrationHash
			}
		}
		// For OpMerge the OldHash/SiblingHash correspond to the two children
		// and are validated in checkMerge.
	}
	return nil
}

// checkMerge enforces sibling integrity: the sibling prefix must be the real
// bit-sibling of Prefix, the sibling hash must match the previous tree, and
// the two children must recompute to the declared merged hash at the parent.
//
// For a merge the migration Prefix is the parent being (re)built; OldHash is
// the parent's previous-epoch hash and SiblingHash is the previous-epoch hash
// of its 1-child, while NewHash is the parent subtree hash in the new epoch.
func (v *Verifier) checkMerge(m *Migration, oldTree *Tree) error {
	zero, err := m.Prefix.AppendBit(0)
	if err != nil {
		return ErrMergeSibling
	}
	one, err := m.Prefix.AppendBit(1)
	if err != nil {
		return ErrMergeSibling
	}
	if !one.Equal(m.SiblingPrefix) {
		return ErrMergeSibling
	}
	// Sibling integrity inside the previous epoch: the two children must be
	// real siblings and their committed combination must equal the parent's
	// previous-epoch hash (OldHash). This proves the merge is not inventing
	// content. The new-epoch parent hash is checked against the new tree in
	// checkSubtreeHashes.
	if oldTree != nil {
		got0, _, err := oldTree.SubtreeHash(zero)
		if err != nil {
			return err
		}
		got1, _, err := oldTree.SubtreeHash(one)
		if err != nil {
			return err
		}
		if got1 != m.SiblingHash {
			return ErrMergeSibling
		}
		prevParent := InteriorHash(m.Epoch-1, v.labelDepth-m.Prefix.Len(), m.Prefix, got0, got1)
		if prevParent != m.OldHash {
			return ErrMergeSibling
		}
	}
	return nil
}

// VerifyConsistency verifies an append-only consistency proof between two
// ingested (possibly non-adjacent) epochs. It reconstructs both roots,
// replays every changed boundary against the migration logs of epochs in
// (From, To], and rejects unchanged leaves whose value or counter changed.
func (v *Verifier) VerifyConsistency(p *ConsistencyProof) error {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.locked {
		return ErrVerifierLocked
	}
	if err := p.checkStructural(); err != nil {
		return err
	}
	from, ok1 := v.roots[p.FromEpoch]
	to, ok2 := v.roots[p.ToEpoch]
	if !ok1 || !ok2 {
		return ErrUnknownEpoch
	}
	if from.Body.TreeRoot != p.FromRoot || to.Body.TreeRoot != p.ToRoot {
		return ErrRootMismatch
	}
	if p.LabelDepth != v.labelDepth {
		return ErrLabelDepth
	}
	oldRoot, err := p.reconstructRoot(p.FromEpoch, false)
	if err != nil {
		return err
	}
	if oldRoot != p.FromRoot {
		return ErrRootMismatch
	}
	newRoot, err := p.reconstructRoot(p.ToEpoch, true)
	if err != nil {
		return err
	}
	if newRoot != p.ToRoot {
		return ErrRootMismatch
	}
	for _, b := range p.Boundaries {
		switch b.Kind {
		case BoundaryLeaf:
			if err := v.checkUnchangedLeaf(b, p.FromEpoch, p.ToEpoch); err != nil {
				return err
			}
		case BoundaryEmpty:
			// Both stored trees must have an empty subtree at this prefix.
			oldTree, newTree := v.trees[p.FromEpoch], v.trees[p.ToEpoch]
			if oldTree == nil || newTree == nil {
				return ErrUnknownEpoch
			}
			oh, _, err := oldTree.SubtreeHash(b.Prefix)
			if err != nil {
				return err
			}
			nh, _, err := newTree.SubtreeHash(b.Prefix)
			if err != nil {
				return err
			}
			wantOld := recomputeEmpty(p.FromEpoch, v.labelDepth, b.Prefix)
			wantNew := recomputeEmpty(p.ToEpoch, v.labelDepth, b.Prefix)
			if oh != wantOld || nh != wantNew {
				return ErrLeafChanged
			}
		case BoundaryChanged:
			if err := v.replayChangedBoundary(b, p.FromEpoch, p.ToEpoch); err != nil {
				return err
			}
		}
	}
	return nil
}

// checkUnchangedLeaf ensures an unchanged boundary leaf really is unchanged:
// present with an identical value in both stored trees and untouched by any
// migration in the interval.
func (v *Verifier) checkUnchangedLeaf(b BoundaryNode, fromEpoch, toEpoch uint32) error {
	oldTree, newTree := v.trees[fromEpoch], v.trees[toEpoch]
	if oldTree == nil || newTree == nil {
		return ErrUnknownEpoch
	}
	ov, ok1 := oldTree.Lookup(b.Leaf.Label)
	nv, ok2 := newTree.Lookup(b.Leaf.Label)
	switch {
	case ok1 && ok2:
		if !ov.Equal(nv) || !ov.Equal(b.Leaf.Value) {
			return ErrLeafChanged
		}
	case !ok1 && !ok2:
		// Empty leaf in both epochs: the boundary must carry the zero value.
		if len(b.Leaf.Value.PublicKey) != 0 || b.Leaf.Value.Revoked || b.Leaf.Value.RevocCounter != 0 {
			return ErrLeafChanged
		}
	default:
		return ErrLeafChanged
	}
	for epoch := fromEpoch + 1; epoch <= toEpoch; epoch++ {
		log := v.migrations[epoch]
		if log == nil {
			return ErrUnknownEpoch
		}
		for i := range log.Entries {
			if log.Entries[i].Prefix.IsPrefixOf(b.Leaf.Label) {
				return ErrLeafTouchedByMigration
			}
		}
	}
	return nil
}

// replayChangedBoundary ensures a changed boundary is explained by exactly the
// topmost migration touching its prefix inside (From, To]. A prefix may be
// migrated several times across consecutive epochs (e.g. revoke then split);
// the chain must start at the boundary's OldHash and end at its NewHash.
func (v *Verifier) replayChangedBoundary(b BoundaryNode, fromEpoch, toEpoch uint32) error {
	found := false
	// expected tracks the hash the next migration must present as OldHash.
	// Before the first migration touching this boundary there is no chain
	// expectation yet; the boundary's OldHash anchors the From-epoch state.
	var expected Hash
	haveExpected := false
	firstSeen := true
	for epoch := fromEpoch + 1; epoch <= toEpoch; epoch++ {
		log := v.migrations[epoch]
		if log == nil {
			return ErrUnknownEpoch
		}
		// Select the most specific migration covering this boundary; if any
		// migration at the boundary's own prefix exists it must win, otherwise
		// an ancestor migration is the topmost explanation.
		var best *Migration
		for i := range log.Entries {
			m := &log.Entries[i]
			if !m.Prefix.IsPrefixOf(b.Prefix) {
				continue
			}
			if best == nil || m.Prefix.Len() > best.Prefix.Len() {
				best = m
			}
		}
		if best == nil {
			continue
		}
		// An ancestor migration (shorter prefix than the boundary) explains
		// the whole region only on its first appearance; subsequent epochs
		// without a specific migration leave the boundary unchanged.
		ancestor := best.Prefix.Len() < b.Prefix.Len()
		found = true
		// On the very first covering migration (no prior event in this loop),
		// require its OldHash to equal the boundary's From-epoch anchor.
		if firstSeen {
			wantOld := b.OldHash
			emptyFrom := recomputeEmpty(fromEpoch, v.labelDepth, b.Prefix)
			emptyNow := recomputeEmpty(epoch, v.labelDepth, best.Prefix)
			if b.OldHash == emptyFrom {
				wantOld = emptyNow
			}
			if best.Op == OpSplit && best.OldHash != wantOld {
				return ErrMigrationHash
			}
			// OpMerge's OldHash is the parent's previous-epoch hash; its
			// integrity is enforced separately in checkMerge, so no anchor
			// equality is imposed here.
			if best.Op == OpRevokeOut && best.OldHash != b.OldHash {
				return ErrMigrationHash
			}
		}
		firstSeen = false
		switch best.Op {
		case OpRevokeOut:
			if haveExpected && best.OldHash != expected {
				return ErrMigrationHash
			}
			// The revoked region is empty from this epoch onward. The hash is
			// epoch-bound; record the prefix and let the next migration compare
			// against the empty subtree in its own epoch.
			expected = Hash{}
			haveExpected = false
		case OpMerge:
			if haveExpected && !ancestor && best.OldHash != expected {
				return ErrMigrationHash
			}
			expected = best.NewHash
			haveExpected = true
		default: // OpSplit
			if !haveExpected {
				emptyAtEpoch := recomputeEmpty(epoch, v.labelDepth, best.Prefix)
				if best.OldHash != emptyAtEpoch {
					return ErrMigrationHash
				}
			} else if best.OldHash != expected {
				return ErrMigrationHash
			}
			expected = best.NewHash
			haveExpected = true
		}
	}
	if !found {
		return ErrBoundaryUnexplained
	}
	toTree := v.trees[toEpoch]
	if toTree == nil {
		return ErrUnknownEpoch
	}
	got, _, err := toTree.SubtreeHash(b.Prefix)
	if err != nil {
		return err
	}
	if got != b.NewHash {
		return ErrMigrationHash
	}
	return nil
}

var (
	// ErrVerifierLocked indicates use after a hard lock.
	ErrVerifierLocked = prefixError("kt: verifier locked after fraud")
	// ErrUnknownEpoch references an epoch not ingested by the verifier.
	ErrUnknownEpoch = prefixError("kt: unknown epoch")
	// ErrLeafChanged indicates an "unchanged" leaf differs across epochs.
	ErrLeafChanged = prefixError("kt: unchanged leaf value changed")
	// ErrLeafTouchedByMigration indicates a migration touched a leaf claimed
	// unchanged.
	ErrLeafTouchedByMigration = prefixError("kt: leaf touched by migration but claimed unchanged")
	// ErrBoundaryUnexplained indicates a changed boundary without a migration.
	ErrBoundaryUnexplained = prefixError("kt: changed boundary lacks authorizing migration")
)
