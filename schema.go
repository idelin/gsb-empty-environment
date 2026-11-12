package netsec

import (
	"errors"
	"fmt"
	"strings"
)

type nodeKind uint8

const (
	tableNode nodeKind = iota
	arrayTableNode
	valueNode
)

type tableState uint8

const (
	implicitTable tableState = iota
	headerTable
	dottedTable
	inlineTable
)

type schemaNode struct {
	kind     nodeKind
	state    tableState
	children map[string]*schemaNode
	elements []*schemaNode
}

func newTable(state tableState) *schemaNode {
	return &schemaNode{
		kind:     tableNode,
		state:    state,
		children: make(map[string]*schemaNode),
	}
}

func newNode(kind nodeKind) *schemaNode {
	if kind == tableNode {
		return newTable(implicitTable)
	}
	return &schemaNode{kind: kind}
}

type schema struct {
	root    *schemaNode
	current *schemaNode
}

func newSchema() *schema {
	root := newTable(headerTable)
	return &schema{root: root, current: root}
}

func pathString(parts []string) string {
	var b strings.Builder
	for _, part := range parts {
		fmt.Fprintf(&b, "%q.", part)
	}
	return strings.TrimSuffix(b.String(), ".")
}

func (s *schema) applyHeader(parts []string, array bool) error {
	n := s.root

	for i, part := range parts {
		last := i == len(parts)-1
		child := n.children[part]

		if !last {
			if child == nil {
				child = newTable(implicitTable)
				n.children[part] = child
			}
			switch child.kind {
			case tableNode:
				if child.state == inlineTable {
					return fmt.Errorf("%w: %s", ErrInlineTableExtension, pathString(parts[:i+1]))
				}
				n = child
			case arrayTableNode:
				if len(child.elements) == 0 {
					return fmt.Errorf("%w: empty array of tables %s", ErrTableArrayConflict, pathString(parts[:i+1]))
				}
				n = child.elements[len(child.elements)-1]
			default:
				return fmt.Errorf("%w: %s is already a value", ErrDuplicateDefinition, pathString(parts[:i+1]))
			}
			continue
		}

		if array {
			if child == nil {
				child = &schemaNode{kind: arrayTableNode}
				n.children[part] = child
			}
			if child.kind != arrayTableNode {
				return fmt.Errorf("%w: %s", ErrTableArrayConflict, pathString(parts))
			}
			element := newTable(headerTable)
			child.elements = append(child.elements, element)
			s.current = element
			return nil
		}

		if child == nil {
			child = newTable(headerTable)
			n.children[part] = child
			s.current = child
			return nil
		}

		switch child.kind {
		case tableNode:
			switch child.state {
			case implicitTable:
				child.state = headerTable
				s.current = child
				return nil
			case headerTable:
				return fmt.Errorf("%w: table %s", ErrDuplicateDefinition, pathString(parts))
			case dottedTable:
				return fmt.Errorf("%w: table %s was defined by dotted keys", ErrDuplicateDefinition, pathString(parts))
			case inlineTable:
				return fmt.Errorf("%w: %s", ErrInlineTableExtension, pathString(parts))
			}
		case arrayTableNode:
			return fmt.Errorf("%w: %s", ErrTableArrayConflict, pathString(parts))
		default:
			return fmt.Errorf("%w: %s is already a value", ErrDuplicateDefinition, pathString(parts))
		}
	}

	return errors.New("empty table header")
}

func (s *schema) applyKey(base *schemaNode, parts []string, value *schemaNode) error {
	n := base

	for i, part := range parts {
		last := i == len(parts)-1
		child := n.children[part]

		if !last {
			if child == nil {
				child = newTable(dottedTable)
				n.children[part] = child
			}
			switch child.kind {
			case tableNode:
				if child.state == inlineTable {
					return fmt.Errorf("%w: %s", ErrInlineTableExtension, pathString(parts[:i+1]))
				}
				if child.state == headerTable {
					return fmt.Errorf("%w: dotted key redefines table %s", ErrDuplicateDefinition, pathString(parts[:i+1]))
				}
				if child.state == implicitTable {
					child.state = dottedTable
				}
				n = child
			case arrayTableNode:
				if len(child.elements) == 0 {
					return fmt.Errorf("%w: empty array of tables %s", ErrTableArrayConflict, pathString(parts[:i+1]))
				}
				n = child.elements[len(child.elements)-1]
			default:
				return fmt.Errorf("%w: %s is already a value", ErrDuplicateDefinition, pathString(parts[:i+1]))
			}
			continue
		}

		if child != nil {
			return fmt.Errorf("%w: key %s", ErrDuplicateDefinition, pathString(parts))
		}
		n.children[part] = value
	}

	return nil
}
