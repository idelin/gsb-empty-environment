package nestedslice

import "fmt"

// Validate checks cross-field descriptor invariants. Payload bytes are not
// required: validation uses only descriptor metadata and symbolic accessors.
func Validate(root *Node) error {
	return validateNode(root, map[string]bool{})
}

func validateNode(n *Node, seen map[string]bool) error {
	if n == nil {
		return errf("nil node")
	}
	if !seen[n.ID] {
		seen[n.ID] = true
	}
	if n.Length < 0 {
		return errf("node %q has negative length %d", n.ID, n.Length)
	}
	for _, ref := range n.bufferRefs() {
		if ref.Buffer.Length < 0 {
			return errf("buffer %q has negative length", ref.Buffer.ID)
		}
	}
	switch n.Kind {
	case PrimitiveNode:
		if n.Value == nil || n.ValueWidth <= 0 {
			return errf("primitive node %q needs a value buffer and positive width", n.ID)
		}
	case ListNode:
		if n.Offsets == nil || len(n.Children) != 1 {
			return errf("list node %q needs offsets and exactly one child", n.ID)
		}
		if !n.FixedSize && n.OffsetData == nil && n.OffsetFn == nil {
			return errf("variable list node %q needs OffsetData or OffsetFn", n.ID)
		}
		if n.OffsetData != nil && int64(len(n.OffsetData)) != n.Length+1 {
			return errf("list node %q offsets length %d, want %d",
				n.ID, len(n.OffsetData), n.Length+1)
		}
		if err := validateNode(n.Children[0], seen); err != nil {
			return err
		}
	case StructNode:
		if len(n.Children) == 0 {
			return errf("struct node %q needs children", n.ID)
		}
		for _, child := range n.Children {
			if err := validateNode(child, seen); err != nil {
				return err
			}
		}
	case DenseUnionNode:
		if n.TypeIDs == nil || n.UnionOffsets == nil || len(n.Children) == 0 {
			return errf("dense union node %q needs type ids, offsets and children", n.ID)
		}
		for _, child := range n.Children {
			if err := validateNode(child, seen); err != nil {
				return err
			}
		}
	case DictionaryNode:
		if n.Index == nil || n.Dictionary == nil {
			return errf("dictionary node %q needs an index buffer and dictionary", n.ID)
		}
		if err := validateNode(n.Dictionary, seen); err != nil {
			return err
		}
	default:
		return errf("node %q has unknown kind", n.ID)
	}
	return nil
}

type validationError struct{ msg string }

func (e validationError) Error() string { return "nestedslice: " + e.msg }

func errf(format string, args ...any) error {
	return validationError{msg: fmt.Sprintf(format, args...)}
}
