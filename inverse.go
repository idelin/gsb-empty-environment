package baselinepatch

import "fmt"

// Inverse returns the patch that transforms p's target back into p's
// baseline. All old content is embedded in the original patch, so the inverse
// is derived syntactically — no baseline document is needed.
//
// Identity convention: for p produced by Diff (and any patch that applies
// cleanly), applying p then Inverse(p) yields a document whose canonical
// digest equals the original baseline digest. Float bits are preserved
// exactly, and "field set to null" inverts to an OpSet back to the old value
// (it never becomes a delete), while deletions invert to sets.
func (p *Patch) Inverse() (*Patch, error) {
	if err := p.Validate(); err != nil {
		return nil, err
	}
	inv := &Patch{
		BaselineDigest: p.TargetDigest,
		TargetDigest:   p.BaselineDigest,
		Ops:            make([]Op, 0, len(p.Ops)),
	}
	// Reverse order: forward edits are arranged so later ops use later paths;
	// the inverse walks back from the final state.
	for k := len(p.Ops) - 1; k >= 0; k-- {
		op := p.Ops[k]
		switch op.Kind {
		case OpSet:
			path := clonePath(op.Path)
			if op.OldExists {
				oldCopy, err := cloneValue(op.Old)
				if err != nil {
					return nil, err
				}
				newCopy, err := cloneValue(op.New[0])
				if err != nil {
					return nil, err
				}
				inv.Ops = append(inv.Ops, Op{
					Kind: OpSet, Path: path,
					OldExists: true, Old: newCopy, New: []any{oldCopy},
				})
			} else {
				// An added field is undone by deleting it; setting null would
				// leave a key that the baseline did not contain.
				newCopy, err := cloneValue(op.New[0])
				if err != nil {
					return nil, err
				}
				inv.Ops = append(inv.Ops, Op{
					Kind: OpDelete, Path: path, Old: newCopy,
				})
			}
		case OpDelete:
			path := clonePath(op.Path)
			oldCopy, err := cloneValue(op.Old)
			if err != nil {
				return nil, err
			}
			inv.Ops = append(inv.Ops, Op{
				Kind: OpSet, Path: path,
				OldExists: false, New: []any{oldCopy},
			})
		case OpSplice:
			path := clonePath(op.Path)
			removed := make([]any, len(op.Removed))
			for i, v := range op.Removed {
				c, err := cloneValue(v)
				if err != nil {
					return nil, err
				}
				removed[i] = c
			}
			inserted := make([]any, len(op.New))
			for i, v := range op.New {
				c, err := cloneValue(v)
				if err != nil {
					return nil, err
				}
				inserted[i] = c
			}
			// Remove the inserted elements and re-insert the removed ones at
			// the same anchor.
			inv.Ops = append(inv.Ops, Op{
				Kind:        OpSplice,
				Path:        path,
				Index:       op.Index,
				DeleteCount: len(op.New),
				New:         removed,
				Removed:     inserted,
			})
		default:
			return nil, fmt.Errorf("%w: unknown kind", ErrInvalidOp)
		}
	}
	if err := inv.Validate(); err != nil {
		return nil, err
	}
	return inv, nil
}
