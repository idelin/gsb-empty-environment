package derstrict

// Pattern describes one element that the caller expects at the current level.
// Nested Patterns constrain the children of constructed elements.
type Pattern struct {
	// Class is the expected tag class.
	Class Class
	// Tag is the expected tag number, or AnyTag to match any tag.
	Tag int
	// Children constrains nested elements. A non-nil (including empty) slice
	// requires a constructed element whose children match in order. A nil
	// slice leaves nested content unconstrained (still fully DER-validated).
	Children []Pattern
	// Repeat matches zero or more successive elements with this Pattern. It
	// may only be set on the last Pattern of a Children slice.
	Repeat bool
}

// DecodeWithPattern runs the strict decoder and verifies that the top-level
// element and every nested element conform to pattern. All DER rules are
// enforced regardless of the pattern; the first violation (structural or
// schema-related) is reported with its byte offset.
func DecodeWithPattern(data []byte, pattern Pattern) (*Node, error) {
	if err := validatePattern(pattern); err != nil {
		return nil, err
	}
	node, err := Decode(data)
	if err != nil {
		return nil, err
	}
	if err := matchPattern(node, pattern); err != nil {
		return nil, err
	}
	return node, nil
}

func validatePattern(pattern Pattern) error {
	for i, child := range pattern.Children {
		if child.Repeat && i != len(pattern.Children)-1 {
			return &Error{
				Offset: 0,
				Op:     "pattern",
				Msg:    "Repeat is only allowed on the last pattern",
			}
		}
		if err := validatePattern(child); err != nil {
			return err
		}
	}
	return nil
}

func matchPattern(node *Node, pattern Pattern) error {
	if pattern.Tag != AnyTag && pattern.Class != AnyClass &&
		(node.Class != pattern.Class || node.Tag != pattern.Tag) {
		return &Error{
			Offset: node.Offset,
			Op:     "pattern",
			Msg:    "element does not match expected class and tag",
		}
	}
	if pattern.Children == nil {
		return nil
	}
	if !node.Constructed {
		return &Error{
			Offset: node.Offset,
			Op:     "pattern",
			Msg:    "expected constructed element with children",
		}
	}

	children := node.Children
	patterns := pattern.Children

	if len(patterns) > 0 && patterns[len(patterns)-1].Repeat {
		fixed := patterns[:len(patterns)-1]
		if len(children) < len(fixed) {
			return &Error{
				Offset: node.Offset,
				Op:     "pattern",
				Msg:    "not enough children for fixed pattern elements",
			}
		}
		for i, fixedPattern := range fixed {
			if err := matchPattern(children[i], fixedPattern); err != nil {
				return err
			}
		}
		repeated := patterns[len(patterns)-1]
		for _, child := range children[len(fixed):] {
			if err := matchPattern(child, repeated); err != nil {
				return err
			}
		}
		return nil
	}

	if len(children) != len(patterns) {
		return &Error{
			Offset: node.Offset,
			Op:     "pattern",
			Msg:    "child count does not match pattern",
		}
	}
	for i, childPattern := range patterns {
		if err := matchPattern(children[i], childPattern); err != nil {
			return err
		}
	}
	return nil
}
