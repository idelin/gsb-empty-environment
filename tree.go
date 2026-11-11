package netsec

import (
	"errors"
	"fmt"
	"hash/crc32"
)

var (
	// ErrDependencyCycle is returned when derived fields reference each
	// other in a cycle and cannot be recomputed.
	ErrDependencyCycle = errors.New("netsec: dependency cycle among derived fields")
	// ErrDanglingReference is returned when a derived field references a
	// node ID that does not exist (for example after it was deleted).
	ErrDanglingReference = errors.New("netsec: derived field references unknown node")
	// ErrDuplicateID is returned when two nodes share a non-empty ID.
	ErrDuplicateID = errors.New("netsec: duplicate node ID")
	// ErrInvalidWidth is returned when a derived field width is not 1..8.
	ErrInvalidWidth = errors.New("netsec: derived field width must be between 1 and 8 bytes")
	// ErrDerivedChildren is returned when a derived field node has children.
	ErrDerivedChildren = errors.New("netsec: derived field node must be a leaf")
)

// Node is one element of the sample tree. A node is either a group
// (Children non-empty), a derived field (exactly one of LengthOf,
// OffsetOf, ChecksumOf set), or a raw blob (Data).
//
// Derived fields are recomputed on every Encode, so deleting or
// shrinking nodes automatically fixes up lengths, offsets and
// checksums. Only nodes with a non-empty ID are reduction targets and
// reference targets.
type Node struct {
	ID       string
	Data     []byte
	Children []*Node

	// LengthOf, if set, makes this node encode the total encoded size
	// of the referenced subtree.
	LengthOf string
	// OffsetOf, if set, makes this node encode the byte offset of the
	// referenced node from the start of the sample.
	OffsetOf string
	// ChecksumOf, if set, makes this node encode the CRC-32 (IEEE) of
	// the referenced subtree's encoding.
	ChecksumOf string
	// Width is the encoded byte width of a derived field (1..8).
	Width int
}

func (n *Node) derived() bool {
	return n.LengthOf != "" || n.OffsetOf != "" || n.ChecksumOf != ""
}

func (n *Node) ref() string {
	switch {
	case n.LengthOf != "":
		return n.LengthOf
	case n.OffsetOf != "":
		return n.OffsetOf
	default:
		return n.ChecksumOf
	}
}

// Clone deep-copies the tree.
func Clone(n *Node) *Node {
	c := *n
	c.Data = append([]byte(nil), n.Data...)
	if n.Children != nil {
		c.Children = make([]*Node, len(n.Children))
		for i, ch := range n.Children {
			c.Children[i] = Clone(ch)
		}
	}
	return &c
}

// layout holds precomputed positions of every named node. Because
// derived fields have a fixed Width, all sizes and offsets are known
// before any value is computed.
type layout struct {
	nodes   map[string]*Node
	offsets map[string]int
	sizes   map[string]int
}

func buildLayout(root *Node) (*layout, error) {
	l := &layout{
		nodes:   make(map[string]*Node),
		offsets: make(map[string]int),
		sizes:   make(map[string]int),
	}
	var all []*Node
	var collect func(n *Node) error
	collect = func(n *Node) error {
		all = append(all, n)
		if n.ID != "" {
			if _, dup := l.nodes[n.ID]; dup {
				return fmt.Errorf("%w: %q", ErrDuplicateID, n.ID)
			}
			l.nodes[n.ID] = n
		}
		for _, c := range n.Children {
			if err := collect(c); err != nil {
				return err
			}
		}
		return nil
	}
	if err := collect(root); err != nil {
		return nil, err
	}
	for _, n := range all {
		if !n.derived() {
			continue
		}
		if len(n.Children) > 0 {
			return nil, fmt.Errorf("%w: %q", ErrDerivedChildren, n.ID)
		}
		if n.Width < 1 || n.Width > 8 {
			return nil, fmt.Errorf("%w: %q width %d", ErrInvalidWidth, n.ID, n.Width)
		}
		if _, ok := l.nodes[n.ref()]; !ok {
			return nil, fmt.Errorf("%w: %q -> %q", ErrDanglingReference, n.ID, n.ref())
		}
	}
	if err := checkCycles(l); err != nil {
		return nil, err
	}
	off := 0
	var assign func(n *Node) int
	assign = func(n *Node) int {
		start := off
		var size int
		switch {
		case len(n.Children) > 0:
			for _, c := range n.Children {
				size += assign(c)
			}
		case n.derived():
			size = n.Width
			off += size
		default:
			size = len(n.Data)
			off += size
		}
		if n.ID != "" {
			l.offsets[n.ID] = start
			l.sizes[n.ID] = size
		}
		return size
	}
	assign(root)
	return l, nil
}

func checkCycles(l *layout) error {
	const (
		white = iota
		gray
		black
	)
	color := make(map[string]int, len(l.nodes))
	var visit func(id string) error
	visit = func(id string) error {
		color[id] = gray
		if n := l.nodes[id]; n.derived() {
			t := n.ref()
			switch color[t] {
			case gray:
				return fmt.Errorf("%w: %q -> %q", ErrDependencyCycle, id, t)
			case white:
				if err := visit(t); err != nil {
					return err
				}
			}
		}
		color[id] = black
		return nil
	}
	for id := range l.nodes {
		if color[id] == white {
			if err := visit(id); err != nil {
				return err
			}
		}
	}
	return nil
}

// Encode serializes the tree, recomputing every derived field. It
// fails on dangling references, dependency cycles and invalid widths.
func Encode(root *Node) ([]byte, error) {
	l, err := buildLayout(root)
	if err != nil {
		return nil, err
	}
	return encodeNode(root, l)
}

func encodeNode(n *Node, l *layout) ([]byte, error) {
	if len(n.Children) > 0 {
		var out []byte
		for _, c := range n.Children {
			b, err := encodeNode(c, l)
			if err != nil {
				return nil, err
			}
			out = append(out, b...)
		}
		return out, nil
	}
	if !n.derived() {
		return append([]byte(nil), n.Data...), nil
	}
	v, err := derivedValue(n, l)
	if err != nil {
		return nil, err
	}
	out := make([]byte, n.Width)
	for i := n.Width - 1; i >= 0; i-- {
		out[i] = byte(v)
		v >>= 8
	}
	return out, nil
}

func derivedValue(n *Node, l *layout) (uint64, error) {
	ref := n.ref()
	switch {
	case n.LengthOf != "":
		return uint64(l.sizes[ref]), nil
	case n.OffsetOf != "":
		return uint64(l.offsets[ref]), nil
	default:
		b, err := encodeNode(l.nodes[ref], l)
		if err != nil {
			return 0, err
		}
		return uint64(crc32.ChecksumIEEE(b)), nil
	}
}
