package taskpkg

import (
	"encoding/binary"
	"errors"
	"fmt"
	"math"
)

// Search-space limits imposed on every input tree.
const (
	// MaxOptionalNodes bounds the number of droppable nodes.
	MaxOptionalNodes = 16
	// MaxIntCandidates bounds the candidate values per integer field.
	MaxIntCandidates = 8
)

// IntField is an integer length field. Value is the currently selected
// value and must be one of Candidates.
type IntField struct {
	Name       string
	Value      uint64
	Candidates []uint64
}

// Ref is a relative reference to another node, encoded as a fixed-width
// int32 offset from the end of the reference field to the start of the
// target node.
type Ref struct {
	Name   string
	Target string
}

// Node is one record of the input tree.
type Node struct {
	Tag      int // stable identifier used in the encoding
	Name     string
	Optional bool
	Fields   []IntField
	Refs     []Ref
	Children []*Node
}

// walk visits nodes in pre-order.
func walk(root *Node, fn func(n *Node)) {
	var rec func(n *Node)
	rec = func(n *Node) {
		fn(n)
		for _, c := range n.Children {
			rec(c)
		}
	}
	rec(root)
}

// CountNodes returns the number of nodes in the tree.
func CountNodes(root *Node) int {
	count := 0
	walk(root, func(*Node) { count++ })
	return count
}

// Clone returns a deep copy of the tree.
func Clone(root *Node) *Node {
	if root == nil {
		return nil
	}
	out := &Node{
		Tag:      root.Tag,
		Name:     root.Name,
		Optional: root.Optional,
		Refs:     append([]Ref(nil), root.Refs...),
	}
	for _, f := range root.Fields {
		out.Fields = append(out.Fields, IntField{
			Name:       f.Name,
			Value:      f.Value,
			Candidates: append([]uint64(nil), f.Candidates...),
		})
	}
	for _, c := range root.Children {
		out.Children = append(out.Children, Clone(c))
	}
	return out
}

// CheckShape verifies the structural limits and referential integrity of
// an input tree.
func CheckShape(root *Node) error {
	if root == nil {
		return errors.New("taskpkg: nil tree")
	}
	if root.Optional {
		return errors.New("taskpkg: root node must not be optional")
	}
	names := map[string]bool{}
	tags := map[int]string{}
	optional := 0
	var err error
	walk(root, func(n *Node) {
		if err != nil {
			return
		}
		switch {
		case n.Name == "":
			err = errors.New("taskpkg: empty node name")
		case names[n.Name]:
			err = fmt.Errorf("taskpkg: duplicate node name %q", n.Name)
		case n.Tag < 0:
			err = fmt.Errorf("taskpkg: node %q: negative tag %d", n.Name, n.Tag)
		}
		if err != nil {
			return
		}
		if prev, dup := tags[n.Tag]; dup {
			err = fmt.Errorf("taskpkg: nodes %q and %q share tag %d", prev, n.Name, n.Tag)
			return
		}
		tags[n.Tag] = n.Name
		names[n.Name] = true
		if n.Optional {
			optional++
		}
		for _, f := range n.Fields {
			if len(f.Candidates) == 0 || len(f.Candidates) > MaxIntCandidates {
				err = fmt.Errorf("taskpkg: field %s.%s: need 1..%d candidate values, got %d",
					n.Name, f.Name, MaxIntCandidates, len(f.Candidates))
				return
			}
			ok := false
			for _, v := range f.Candidates {
				if v == f.Value {
					ok = true
					break
				}
			}
			if !ok {
				err = fmt.Errorf("taskpkg: field %s.%s: value %d not among candidates",
					n.Name, f.Name, f.Value)
				return
			}
		}
	})
	if err != nil {
		return err
	}
	if optional > MaxOptionalNodes {
		return fmt.Errorf("taskpkg: %d optional nodes exceed limit %d", optional, MaxOptionalNodes)
	}
	walk(root, func(n *Node) {
		if err != nil {
			return
		}
		for _, r := range n.Refs {
			if !names[r.Target] {
				err = fmt.Errorf("taskpkg: ref %s.%s: unknown target %q", n.Name, r.Name, r.Target)
				return
			}
		}
	})
	return err
}

// OptionalNodes lists the names of optional nodes in pre-order.
func OptionalNodes(root *Node) []string {
	var names []string
	walk(root, func(n *Node) {
		if n.Optional {
			names = append(names, n.Name)
		}
	})
	return names
}

// DropOptional returns a deep copy of root with the named nodes removed.
func DropOptional(root *Node, drop map[string]bool) *Node {
	out := Clone(root)
	var rec func(n *Node)
	rec = func(n *Node) {
		kept := n.Children[:0]
		for _, c := range n.Children {
			if !drop[c.Name] {
				kept = append(kept, c)
			}
		}
		n.Children = kept
		for _, c := range n.Children {
			rec(c)
		}
	}
	rec(out)
	return out
}

func uvarintLen(v uint64) int {
	n := 1
	for v >= 0x80 {
		v >>= 7
		n++
	}
	return n
}

// Encode serializes the tree deterministically. Each node is laid out in
// pre-order as:
//
//	uvarint tag | uvarint per int field | 4-byte LE per ref | uvarint child count | children
//
// A reference stores the signed offset from the end of its own 4-byte
// field to the start of the target node. Encoding fails if a reference
// target is absent from the tree.
func Encode(root *Node) ([]byte, error) {
	byName := map[string]*Node{}
	walk(root, func(n *Node) {
		byName[n.Name] = n
	})
	starts := map[*Node]int{}
	pos := 0
	var measure func(n *Node)
	measure = func(n *Node) {
		starts[n] = pos
		size := uvarintLen(uint64(n.Tag)) + 1 // tag + child count
		for _, f := range n.Fields {
			size += uvarintLen(f.Value)
		}
		size += 4 * len(n.Refs)
		pos += size
		for _, c := range n.Children {
			measure(c)
		}
	}
	measure(root)
	buf := make([]byte, 0, pos)
	var emit func(n *Node) error
	emit = func(n *Node) error {
		buf = binary.AppendUvarint(buf, uint64(n.Tag))
		for _, f := range n.Fields {
			buf = binary.AppendUvarint(buf, f.Value)
		}
		for _, r := range n.Refs {
			target, ok := byName[r.Target]
			if !ok {
				return fmt.Errorf("taskpkg: ref %s.%s: dangling target %q", n.Name, r.Name, r.Target)
			}
			rel := int64(starts[target]) - int64(len(buf)+4)
			if rel < math.MinInt32 || rel > math.MaxInt32 {
				return fmt.Errorf("taskpkg: ref %s.%s: relative offset %d out of range", n.Name, r.Name, rel)
			}
			var tmp [4]byte
			binary.LittleEndian.PutUint32(tmp[:], uint32(int32(rel)))
			buf = append(buf, tmp[:]...)
		}
		buf = binary.AppendUvarint(buf, uint64(len(n.Children)))
		for _, c := range n.Children {
			if err := emit(c); err != nil {
				return err
			}
		}
		return nil
	}
	if err := emit(root); err != nil {
		return nil, err
	}
	return buf, nil
}
