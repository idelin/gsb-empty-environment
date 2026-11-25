package zoneauth

import "crypto/ed25519"

// PatchBuilder stages a finite zone update, snapshots the evidence required
// for each operation, applies the mutations, commits the new root and binds
// an optional old-key handoff. It is the manager-side counterpart to
// VerifyPatch.
type PatchBuilder struct {
	zone        *Zone
	snapshot    *Zone
	oldRoot     *RootEnvelope
	oldRootHash []byte
	ops         []Operation
	rotateKey   ed25519.PrivateKey
	synced      bool
}

// NewPatchBuilder starts a patch against the zone's current committed root.
func NewPatchBuilder(z *Zone) *PatchBuilder {
	return &PatchBuilder{
		zone: z,
	}
}

// ensureSynced freezes the old root at the first staged mutation. All old
// proofs and the old envelope then describe exactly the same tree state,
// regardless of any direct host mutations performed before the builder.
func (b *PatchBuilder) ensureSynced() {
	if b.synced {
		return
	}
	b.synced = true
	b.zone.Sync()
	b.snapshot = b.zone.Clone()
	b.oldRootHash = b.zone.RootHash()
	b.oldRoot = currentEnvelope(b.zone)
}

func currentEnvelope(z *Zone) *RootEnvelope {
	env := &RootEnvelope{
		Zone:          z.name,
		Sequence:      z.sequence,
		PreviousKeyID: z.prevKeyID,
		RootHash:      z.RootHash(),
	}
	SignRootEnvelope(env, z.key)
	return env
}

// AddDelegation stages a new delegation cut.
func (b *PatchBuilder) AddDelegation(target Name, ns []Record) error {
	b.ensureSynced()
	oldProof := b.snapshot.BuildProof(target)
	if err := b.zone.AddDelegation(target, ns); err != nil {
		return err
	}
	b.ops = append(b.ops, Operation{
		Kind:     OpAddDelegation,
		Target:   target.Clone(),
		OldProof: oldProof,
	})
	return nil
}

// RemoveDelegation stages a delegation removal.
func (b *PatchBuilder) RemoveDelegation(target Name) error {
	b.ensureSynced()
	oldProof := b.snapshot.BuildProof(target)
	if err := b.zone.RemoveDelegation(target); err != nil {
		return err
	}
	b.ops = append(b.ops, Operation{
		Kind:     OpRemoveDelegation,
		Target:   target.Clone(),
		OldProof: oldProof,
	})
	return nil
}

// SetWildcard stages a wildcard template install, replacement or removal.
func (b *PatchBuilder) SetWildcard(anchor Name, recs []Record) error {
	b.ensureSynced()
	oldProof := b.snapshot.BuildProof(anchor.With("*"))
	if err := b.zone.SetWildcard(anchor, recs); err != nil {
		return err
	}
	b.ops = append(b.ops, Operation{
		Kind:     OpSetWildcard,
		Target:   anchor.Clone(),
		OldProof: oldProof,
	})
	return nil
}

// RotateKey requests an old-key handoff to newKey in this revision.
func (b *PatchBuilder) RotateKey(newKey ed25519.PrivateKey) {
	b.rotateKey = newKey
}

// AttachClientProofs signs out minimal new-root partial proofs for the given
// cached queries. The host calls this for queries whose prior evidence may
// be invalidated by the staged update.
func (b *PatchBuilder) AttachClientProofs(patch *Patch, queries ...Name) {
	seen := map[string]bool{}
	for _, query := range queries {
		key := query.Key()
		if seen[key] {
			continue
		}
		seen[key] = true
		patch.ClientProofs = append(patch.ClientProofs, b.zone.BuildProof(query))
	}
}

// Sign applies any staged handoff, commits the new root and fills in the
// new-root proof of every operation before returning the finished patch.
func (b *PatchBuilder) Sign() *Patch {
	nextSequence := b.oldRoot.Sequence + 1
	patch := &Patch{
		Zone:    b.zone.Name(),
		OldRoot: b.oldRoot,
		Ops:     b.ops,
	}

	var handoff *HandoffEnvelope
	if b.rotateKey != nil {
		// Pin the exact post-mutation root hash, then let the OLD key sign a
		// handoff binding both the pre-mutation old root and the new root.
		newRoot := b.zone.CommitAt(nextSequence)
		handoff = b.zone.BuildHandoffAt(b.rotateKey, b.oldRootHash, newRoot.RootHash, nextSequence)
		newRoot = b.zone.CommitAt(nextSequence)
		patch.Handoff = handoff
		patch.NewRoot = newRoot
	}
	if patch.NewRoot == nil {
		patch.NewRoot = b.zone.CommitAt(nextSequence)
	}
	patch.NewApex = b.zone.SnapshotNode(Name{})

	for i := range patch.Ops {
		op := &patch.Ops[i]
		query := op.Target
		if op.Kind == OpSetWildcard {
			query = op.Target.With("*")
		}
		op.NewProof = b.zone.BuildProof(query)
	}
	return patch
}
