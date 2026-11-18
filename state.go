package netsec

type nodeKind uint8

const (
	kindTable nodeKind = iota
	kindValue
	kindInlineTable
	kindArrayTable
)

type tableNode struct {
	kind nodeKind

	// explicit records a [table] or [[table]] declaration. dotted records a
	// table introduced by a dotted key. Header-created parent tables have both
	// flags unset and may later be declared explicitly.
	explicit bool
	dotted   bool

	children map[string]*tableNode
	elements []*tableNode
}

func newTable() *tableNode {
	return &tableNode{kind: kindTable, children: make(map[string]*tableNode)}
}

func newValue(kind nodeKind) *tableNode {
	return &tableNode{kind: kind}
}

func newArrayTable() *tableNode {
	return &tableNode{kind: kindArrayTable}
}

func (n *tableNode) clone() *tableNode {
	if n == nil {
		return nil
	}
	cloned := &tableNode{
		kind:     n.kind,
		explicit: n.explicit,
		dotted:   n.dotted,
	}
	if n.children != nil {
		cloned.children = make(map[string]*tableNode, len(n.children))
		for name, child := range n.children {
			cloned.children[name] = child.clone()
		}
	}
	if n.elements != nil {
		cloned.elements = make([]*tableNode, len(n.elements))
		for i, element := range n.elements {
			cloned.elements[i] = element.clone()
		}
	}
	return cloned
}

func (n *tableNode) lastElement() *tableNode {
	if n == nil || n.kind != kindArrayTable || len(n.elements) == 0 {
		return nil
	}
	return n.elements[len(n.elements)-1]
}

// declareHeader applies an absolute [table] or [[table]] path.
func (root *tableNode) declareHeader(path []string, arrayTable bool) (*tableNode, error) {
	current := root
	for _, segment := range path[:len(path)-1] {
		if current.kind == kindArrayTable {
			current = current.lastElement()
		}
		if current == nil || current.kind != kindTable {
			if current != nil && current.kind == kindInlineTable {
				return nil, ErrInlineTableAppend
			}
			return nil, ErrDuplicateKey
		}

		child := current.children[segment]
		if child == nil {
			child = newTable()
			current.children[segment] = child
		}
		switch child.kind {
		case kindTable, kindArrayTable:
			current = child
		case kindInlineTable:
			return nil, ErrInlineTableAppend
		default:
			return nil, ErrDuplicateKey
		}
	}

	if current.kind == kindArrayTable {
		current = current.lastElement()
	}
	if current == nil || current.kind != kindTable {
		return nil, ErrDuplicateKey
	}

	name := path[len(path)-1]
	child := current.children[name]
	if arrayTable {
		switch {
		case child == nil:
			child = newArrayTable()
			child.elements = append(child.elements, newTable())
			current.children[name] = child
			return child.lastElement(), nil
		case child.kind == kindArrayTable:
			element := newTable()
			child.elements = append(child.elements, element)
			return element, nil
		case child.kind == kindInlineTable:
			return nil, ErrInlineTableAppend
		case child.kind == kindTable:
			return nil, ErrArrayTableMismatch
		default:
			return nil, ErrDuplicateKey
		}
	}

	switch {
	case child == nil:
		child = newTable()
		child.explicit = true
		current.children[name] = child
		return child, nil
	case child.kind == kindTable:
		if child.explicit || child.dotted {
			return nil, ErrTableRedefined
		}
		child.explicit = true
		return child, nil
	case child.kind == kindInlineTable:
		return nil, ErrInlineTableAppend
	case child.kind == kindArrayTable:
		return nil, ErrArrayTableMismatch
	default:
		return nil, ErrDuplicateKey
	}
}

// assignValue applies a key/value pair relative to current. Intermediate
// tables introduced by the dotted key are marked as dotted tables. Traversing
// an explicitly declared table with a dotted key is rejected, matching TOML's
// prohibition on redefining a [table] using dotted keys.
func assignValue(current *tableNode, path []string, valueKind nodeKind) error {
	if current == nil || current.kind != kindTable || len(path) == 0 {
		return ErrInvalidTOML
	}

	cursor := current
	for _, segment := range path[:len(path)-1] {
		if cursor.kind == kindArrayTable {
			cursor = cursor.lastElement()
		}
		if cursor == nil || cursor.kind != kindTable {
			return ErrDuplicateKey
		}

		child := cursor.children[segment]
		if child == nil {
			child = newTable()
			child.dotted = true
			cursor.children[segment] = child
			cursor = child
			continue
		}

		switch child.kind {
		case kindTable:
			if child.explicit {
				return ErrTableRedefined
			}
			cursor = child
		case kindArrayTable:
			cursor = child
		case kindInlineTable:
			return ErrInlineTableAppend
		default:
			return ErrDuplicateKey
		}
	}

	if cursor.kind == kindArrayTable {
		cursor = cursor.lastElement()
	}
	if cursor == nil || cursor.kind != kindTable {
		return ErrDuplicateKey
	}

	name := path[len(path)-1]
	if _, exists := cursor.children[name]; exists {
		return ErrDuplicateKey
	}
	cursor.children[name] = newValue(valueKind)
	return nil
}

// defineInlineValue checks key conflicts inside an inline table. Inline tables
// have no headers, so implicit tables are valid as long as no path is sealed or
// defined twice.
func defineInlineValue(current *tableNode, path []string, valueKind nodeKind) error {
	if len(path) == 0 {
		return ErrInvalidTOML
	}

	cursor := current
	for _, segment := range path[:len(path)-1] {
		child := cursor.children[segment]
		if child == nil {
			child = newTable()
			cursor.children[segment] = child
			cursor = child
			continue
		}
		switch child.kind {
		case kindTable:
			cursor = child
		case kindInlineTable:
			return ErrInlineTableAppend
		default:
			return ErrDuplicateKey
		}
	}

	name := path[len(path)-1]
	if _, exists := cursor.children[name]; exists {
		return ErrDuplicateKey
	}
	cursor.children[name] = newValue(valueKind)
	return nil
}
