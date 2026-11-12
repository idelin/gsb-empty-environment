// Package netsec provides an object monitor for instrumented programs that
// emit binary messages built over union (C-style shared-storage) memory.
//
// A union reinterprets the same bytes through different active members. When
// the active member changes, bytes written through the previous member are not
// cleared. If the new active member is sent before every byte it covers was
// initialized, the transmitted buffer carries residual bytes that still
// originate from the previous member. This package reconstructs that leak
// without touching real memory: it tracks an explicit layout, active member
// switches, per-byte writes and send events, and reports the source of every
// residual byte that a send would transmit.
package netsec

import (
	"errors"
	"fmt"
	"strings"
)

// Kind describes how a layout field occupies its storage.
type Kind int

const (
	// Struct fields occupy disjoint, non-overlapping storage. A struct has no
	// active member; every initialized child is simultaneously reachable.
	Struct Kind = iota

	// Union fields place all of their children at the same starting offset, so the
	// children overlap. Exactly one child is active at a time and is selected with
	// Monitor.Switch; the monitor never initializes bytes as a side effect of a
	// switch.
	Union

	// Leaf fields are plain scalar or array storage. They cannot carry
	// children and their size must be greater than zero.
	Leaf
)

func (k Kind) String() string {
	switch k {
	case Struct:
		return "struct"
	case Union:
		return "union"
	case Leaf:
		return "leaf"
	default:
		return fmt.Sprintf("kind(%d)", int(k))
	}
}

// Field describes one node of an explicit memory layout.
//
// Offset is relative to the nearest enclosing layout object (the layout root),
// not to the parent node. Union members therefore describe their real, shared
// absolute offsets, including any overlap with sibling members. Size is the
// number of bytes the field occupies and must be positive. Children describe
// nested structs and unions; a Leaf field must have no children.
type Field struct {
	Name     string
	Kind     Kind
	Offset   int
	Size     int
	Children []Field
}

// node is the validated, indexed form of a Field.
type node struct {
	field  Field
	path   string
	parent *node

	// byPath indexes every node by its dot-separated path.
	byPath map[string]*node

	// active is the currently selected child of a union node. It is nil for
	// struct and leaf nodes and for unions that have never been switched.
	active *node
}

// buildNode validates a field tree and produces an indexed node tree.
//
// parentPath is the dot-separated path of the parent; it is empty at the root.
// byPath is shared across the whole tree and indexes every generated node.
func buildNode(f Field, parentPath string, byPath map[string]*node) (*node, error) {
	if f.Name == "" {
		return nil, errors.New("netsec: field name must not be empty")
	}
	if strings.ContainsAny(f.Name, ". ") {
		return nil, fmt.Errorf("netsec: field name %q must not contain dots or spaces", f.Name)
	}
	if f.Offset < 0 {
		return nil, fmt.Errorf("netsec: field %q has negative offset %d", f.Name, f.Offset)
	}
	if f.Size <= 0 {
		return nil, fmt.Errorf("netsec: field %q must have a positive size, got %d", f.Name, f.Size)
	}

	path := f.Name
	if parentPath != "" {
		path = parentPath + "." + f.Name
	}
	if _, exists := byPath[path]; exists {
		return nil, fmt.Errorf("netsec: duplicate field path %q", path)
	}

	cur := &node{field: f, path: path, byPath: byPath}
	byPath[path] = cur

	switch f.Kind {
	case Leaf:
		if len(f.Children) != 0 {
			return nil, fmt.Errorf("netsec: leaf field %q must not have children", path)
		}
	case Struct, Union:
		if len(f.Children) == 0 {
			return nil, fmt.Errorf("netsec: %s field %q must have at least one child", f.Kind, path)
		}
	default:
		return nil, fmt.Errorf("netsec: field %q has unknown kind %d", path, f.Kind)
	}

	for i := range f.Children {
		child, err := buildNode(f.Children[i], path, byPath)
		if err != nil {
			return nil, err
		}
		child.parent = cur
		cur.field.Children[i] = child.field

		rel := child.field.Offset - f.Offset
		if rel < 0 {
			return nil, fmt.Errorf("netsec: child %q starts before its parent %q", child.path, path)
		}
		end := rel + child.field.Size
		if end > f.Size {
			return nil, fmt.Errorf("netsec: child %q (offset=%d size=%d) overflows parent %q (offset=%d size=%d)",
				child.path, child.field.Offset, child.field.Size, path, f.Offset, f.Size)
		}

		if f.Kind == Struct {
			for j := 0; j < i; j++ {
				other := f.Children[j]
				if intervalsOverlap(child.field.Offset, child.field.Size, other.Offset, other.Size) {
					return nil, fmt.Errorf("netsec: struct %q has overlapping children %q and %q",
						path, child.path, path+"."+other.Name)
				}
			}
		} else {
			// Every union member starts at the union's own offset so that the
			// members share storage.
			if child.field.Offset != f.Offset {
				return nil, fmt.Errorf("netsec: union member %q must share the union offset %d, got %d",
					child.path, f.Offset, child.field.Offset)
			}
		}
	}
	return cur, nil
}

func intervalsOverlap(offA, sizeA, offB, sizeB int) bool {
	return offA < offB+sizeB && offB < offA+sizeA
}
