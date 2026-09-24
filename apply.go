package baselinepatch

import (
	"fmt"
	"strconv"
)

// rootKind reports the required top-level container kind. The supported
// document root is an object; a scalar or array root has no field-change model
// and is rejected rather than treated as an implicit {}.
func requireRootObject(doc any) (map[string]any, error) {
	obj, ok := doc.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("%w: document root must be an object, got %T",
			ErrUnsupportedValue, doc)
	}
	return obj, nil
}

// ownerOf walks all but the final path segment, returning the containing
// object or array and the last segment (field name or array index string).
// Missing intermediates and type mismatches fail with ErrInvalidPath.
func ownerOf(root any, path []string) (owner any, last string, err error) {
	cur := root
	for i := 0; i < len(path)-1; i++ {
		switch node := cur.(type) {
		case map[string]any:
			next, ok := node[path[i]]
			if !ok {
				return nil, "", fmt.Errorf("%w: segment %q not found",
					ErrInvalidPath, path[i])
			}
			cur = next
		case []any:
			idx, ok := parseIndex(path[i])
			if !ok || idx < 0 || idx >= len(node) {
				return nil, "", fmt.Errorf("%w: bad array index %q",
					ErrInvalidPath, path[i])
			}
			cur = node[idx]
		default:
			return nil, "", fmt.Errorf("%w: segment %q traverses non-container",
				ErrInvalidPath, path[i])
		}
	}
	return cur, path[len(path)-1], nil
}

func parseIndex(s string) (int, bool) {
	// strconv.Atoi rejects sign-less garbage; leading "+" is rejected to keep
	// indices canonical; "0" and values without leading zeros are expected.
	if s == "" || s[0] == '+' || s[0] == '-' {
		return 0, false
	}
	if len(s) > 1 && s[0] == '0' {
		return 0, false
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return 0, false
	}
	return n, true
}

// resolve returns the value at the full path (used by splice, whose path
// names the array itself).
func resolve(root any, path []string) (any, error) {
	cur := root
	for _, seg := range path {
		switch node := cur.(type) {
		case map[string]any:
			next, ok := node[seg]
			if !ok {
				return nil, fmt.Errorf("%w: segment %q not found",
					ErrInvalidPath, seg)
			}
			cur = next
		case []any:
			idx, ok := parseIndex(seg)
			if !ok || idx < 0 || idx >= len(node) {
				return nil, fmt.Errorf("%w: bad array index %q",
					ErrInvalidPath, seg)
			}
			cur = node[idx]
		default:
			return nil, fmt.Errorf("%w: segment %q traverses non-container",
				ErrInvalidPath, seg)
		}
	}
	return cur, nil
}

// preflight verifies every operation against the baseline without mutating
// anything. It enforces old-value expectations, so patches are self-checking
// and safely invertible.
func preflight(root any, ops []Op) error {
	for i := range ops {
		op := &ops[i]
		owner, last, err := ownerOf(root, op.Path)
		if err != nil {
			return fmt.Errorf("op %d: %w", i, err)
		}
		switch op.Kind {
		case OpSet:
			switch parent := owner.(type) {
			case map[string]any:
				old, exists := parent[last]
				if exists != op.OldExists {
					return fmt.Errorf("op %d: %w field %q existence mismatch",
						i, ErrInvalidOp, last)
				}
				if exists {
					if err := expectValue(i, "old", old, op.Old); err != nil {
						return err
					}
				}
			case []any:
				idx, ok := parseIndex(last)
				if !ok || idx < 0 || idx >= len(parent) {
					return fmt.Errorf("op %d: %w bad array index %q",
						i, ErrInvalidPath, last)
				}
				if !op.OldExists {
					return fmt.Errorf("op %d: %w replacing existing index %d",
						i, ErrInvalidOp, idx)
				}
				if err := expectValue(i, "old", parent[idx], op.Old); err != nil {
					return err
				}
			default:
				return fmt.Errorf("op %d: %w parent is not an object or array",
					i, ErrInvalidPath)
			}
		case OpDelete:
			parent, ok := owner.(map[string]any)
			if !ok {
				return fmt.Errorf("op %d: %w delete target parent must be object",
					i, ErrInvalidPath)
			}
			old, exists := parent[last]
			if !exists {
				return fmt.Errorf("op %d: %w cannot delete absent field %q",
					i, ErrInvalidOp, last)
			}
			if err := expectValue(i, "deleted", old, op.Old); err != nil {
				return err
			}
		case OpSplice:
			target, rerr := resolve(root, op.Path)
			if rerr != nil {
				return fmt.Errorf("op %d: %w", i, rerr)
			}
			parent, ok := target.([]any)
			if !ok {
				return fmt.Errorf("op %d: %w splice target must be array",
					i, ErrInvalidPath)
			}
			if op.Index > len(parent) {
				return fmt.Errorf("op %d: %w splice index %d beyond len %d",
					i, ErrInvalidOp, op.Index, len(parent))
			}
			if op.Index+op.DeleteCount > len(parent) {
				return fmt.Errorf("op %d: %w splice deletes past end",
					i, ErrInvalidOp)
			}
			for j := 0; j < op.DeleteCount; j++ {
				if err := expectValue(i, "removed",
					parent[op.Index+j], op.Removed[j]); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func expectValue(opIdx int, what string, got, want any) error {
	eq, err := equalValues(got, want)
	if err != nil {
		return err
	}
	if !eq {
		return fmt.Errorf("op %d: %w %s value mismatch", opIdx, ErrInvalidOp, what)
	}
	return nil
}

func execute(root map[string]any, ops []Op) {
	for i := range ops {
		op := &ops[i]
		owner, last, _ := ownerOf(root, op.Path) // preflight already proved it
		switch op.Kind {
		case OpSet:
			switch parent := owner.(type) {
			case map[string]any:
				parent[last] = op.New[0]
			case []any:
				idx, _ := parseIndex(last)
				parent[idx] = op.New[0]
			}
		case OpDelete:
			delete(owner.(map[string]any), last)
		case OpSplice:
			parent, _ := resolve(root, op.Path)
			arr := parent.([]any)
			tail := append([]any(nil), arr[op.Index+op.DeleteCount:]...)
			arr = append(arr[:op.Index], op.New...)
			arr = append(arr, tail...)
			writeBack(root, op.Path, arr)
		}
	}
}

// writeBack stores a replaced array at the slot named by path. execute mutates
// a local slice header on reallocation, so the new header must be published to
// its containing object/array.
func writeBack(root any, path []string, arr []any) {
	cur := root
	for i := 0; i < len(path)-1; i++ {
		switch node := cur.(type) {
		case map[string]any:
			cur = node[path[i]]
		case []any:
			idx, _ := parseIndex(path[i])
			cur = node[idx]
		}
	}
	last := path[len(path)-1]
	switch node := cur.(type) {
	case map[string]any:
		node[last] = arr
	case []any:
		idx, _ := parseIndex(last)
		node[idx] = arr
	}
}

// Apply checks the baseline digest, validates and preflights every op against
// a deep copy of the baseline, executes the edits, then verifies the target
// digest. The original baseline is never modified; any failure returns an
// error and nil result.
//
// A nil baseline returns ErrMissingBaseline — Apply never substitutes {} or
// any other default object.
func Apply(baseline any, p *Patch) (any, error) {
	if baseline == nil {
		return nil, ErrMissingBaseline
	}
	if _, err := requireRootObject(baseline); err != nil {
		return nil, err
	}
	if err := p.Validate(); err != nil {
		return nil, err
	}
	gotDigest, err := Digest(baseline)
	if err != nil {
		return nil, err
	}
	if gotDigest != p.BaselineDigest {
		return nil, fmt.Errorf("%w: expected %x, got %x",
			ErrBaselineMismatch, p.BaselineDigest[:8], gotDigest[:8])
	}

	work, err := cloneValue(baseline)
	if err != nil {
		return nil, err
	}
	root := work.(map[string]any)

	if err := preflight(root, p.Ops); err != nil {
		return nil, err
	}
	execute(root, p.Ops)

	finalDigest, err := Digest(root)
	if err != nil {
		return nil, err
	}
	if finalDigest != p.TargetDigest {
		return nil, fmt.Errorf("%w: computed %x, patch claims %x",
			ErrTargetMismatch, finalDigest[:8], p.TargetDigest[:8])
	}
	return root, nil
}
