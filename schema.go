package parquetcol

import (
	"errors"
	"fmt"
)

// PhysicalType is a Parquet physical type.
type PhysicalType int

// Parquet physical types supported by this package.
const (
	TypeBoolean PhysicalType = iota
	TypeInt32
	TypeInt64
	TypeFloat
	TypeDouble
	TypeByteArray
)

func (t PhysicalType) thriftType() int32 {
	// parquet.thrift Type enum: BOOLEAN=0, INT32=1, INT64=2, INT96=3,
	// FLOAT=4, DOUBLE=5, BYTE_ARRAY=6, FIXED_LEN_BYTE_ARRAY=7.
	switch t {
	case TypeBoolean:
		return 0
	case TypeInt32:
		return 1
	case TypeInt64:
		return 2
	case TypeFloat:
		return 4
	case TypeDouble:
		return 5
	case TypeByteArray:
		return 6
	default:
		panic(fmt.Sprintf("parquetcol: unknown physical type %d", t))
	}
}

func physicalFromThrift(t int32) (PhysicalType, error) {
	switch t {
	case 0:
		return TypeBoolean, nil
	case 1:
		return TypeInt32, nil
	case 2:
		return TypeInt64, nil
	case 4:
		return TypeFloat, nil
	case 5:
		return TypeDouble, nil
	case 6:
		return TypeByteArray, nil
	default:
		return 0, fmt.Errorf("parquetcol: unsupported physical type %d", t)
	}
}

// Repetition is a Parquet field repetition rule.
type Repetition int

// Repetition rules.
const (
	Required Repetition = iota
	Optional
	Repeated
)

func (r Repetition) thriftRep() int32 {
	switch r {
	case Required:
		return 0
	case Optional:
		return 1
	case Repeated:
		return 2
	default:
		panic(fmt.Sprintf("parquetcol: unknown repetition %d", r))
	}
}

func repetitionFromThrift(r int32) (Repetition, error) {
	switch r {
	case 0:
		return Required, nil
	case 1:
		return Optional, nil
	case 2:
		return Repeated, nil
	default:
		return 0, fmt.Errorf("parquetcol: unknown repetition type %d", r)
	}
}

// Node is a node of the Parquet schema tree. A node is either a primitive leaf
// (PhysicalType >= 0) or a group node (Children != nil).
type Node struct {
	Name       string
	Repetition Repetition
	Physical   PhysicalType
	IsLeaf     bool
	IsString   bool // BYTE_ARRAY leaf annotated as UTF8
	Children   []*Node
	parent     *Node
	repDepth   int
	defDepth   int
	maxDef     int
	maxRep     int
	synthetic  bool // sentinel leaf backing an empty group
}

// Leaf builds a primitive leaf node.
func Leaf(name string, rep Repetition, t PhysicalType) *Node {
	return &Node{Name: name, Repetition: rep, Physical: t, IsLeaf: true}
}

// StringLeaf builds a UTF8-annotated BYTE_ARRAY leaf.
func StringLeaf(name string, rep Repetition) *Node {
	return &Node{Name: name, Repetition: rep, Physical: TypeByteArray, IsLeaf: true, IsString: true}
}

// Group builds a non-leaf node.
func Group(name string, rep Repetition, children ...*Node) *Node {
	return &Node{Name: name, Repetition: rep, Children: children}
}

// ListOf builds the canonical 3-level Parquet LIST structure:
//
//	optional group name (LIST) {
//	  repeated group list {
//	    element <element node, renamed to "element">
//	  }
//	}
//
// element must be a leaf or group node; its repetition is forced to optional
// for leaves (the Parquet spec requires the element to be optional unless it
// is a group). The outer optional wrapper lets the writer distinguish a null
// list from an empty list.
func ListOf(name string, element *Node) *Node {
	element = cloneNode(element)
	element.Name = "element"
	if element.IsLeaf {
		element.Repetition = Optional
	}
	listGroup := &Node{
		Name:       "list",
		Repetition: Repeated,
		Children:   []*Node{element},
	}
	return &Node{
		Name:       name,
		Repetition: Optional,
		Children:   []*Node{listGroup},
	}
}

func cloneNode(n *Node) *Node {
	cp := *n
	if n.Children != nil {
		cp.Children = make([]*Node, len(n.Children))
		for i, c := range n.Children {
			cp.Children[i] = cloneNode(c)
		}
	}
	return &cp
}

// Schema is a validated schema tree.
type Schema struct {
	Root   *Node
	leaves []*Node // primitive leaves in column order (including synthetic ones)
}

// emptySentinelName backs empty groups so they survive shredding/assembly.
const emptySentinelName = "__parquetcol_empty__"

// Compile validates the tree, fills cached depth/level information and returns
// a usable Schema.
func Compile(root *Node) (*Schema, error) {
	if root == nil {
		return nil, errors.New("parquetcol: nil schema root")
	}
	if root.Name == "" {
		return nil, errors.New("parquetcol: schema root needs a name")
	}
	root.Repetition = Required
	root.parent = nil
	seen := map[string]bool{}
	if err := validateNode(root, seen); err != nil {
		return nil, err
	}
	computeDepths(root, 0, 0)
	s := &Schema{Root: root}
	collectLeaves(root, &s.leaves)
	if len(s.leaves) == 0 {
		return nil, errors.New("parquetcol: schema has no leaf columns")
	}
	return s, nil
}

func validateNode(n *Node, seen map[string]bool) error {
	if n.Name == "" {
		return errors.New("parquetcol: node with empty name")
	}
	if n.IsLeaf {
		if len(n.Children) != 0 {
			return fmt.Errorf("parquetcol: leaf %q must not have children", n.Name)
		}
		return nil
	}
	if len(n.Children) == 0 {
		// Give an empty group a synthetic optional boolean leaf so it has a
		// column carrying its definition levels. The sentinel never appears
		// in assembled records.
		sentinel := &Node{
			Name:       emptySentinelName,
			Repetition: Optional,
			Physical:   TypeBoolean,
			IsLeaf:     true,
			synthetic:  true,
		}
		n.Children = []*Node{sentinel}
	}
	childNames := map[string]bool{}
	for _, c := range n.Children {
		if childNames[c.Name] {
			return fmt.Errorf("parquetcol: duplicate child name %q under %q", c.Name, n.Name)
		}
		childNames[c.Name] = true
		c.parent = n
		if err := validateNode(c, seen); err != nil {
			return err
		}
	}
	return nil
}

func computeDepths(n *Node, repDepth, defDepth int) {
	n.repDepth = repDepth
	n.defDepth = defDepth
	if n.Repetition == Repeated {
		repDepth++
	}
	if n.Repetition != Required {
		defDepth++
	}
	n.maxRep = repDepth
	n.maxDef = defDepth
	for _, c := range n.Children {
		computeDepths(c, repDepth, defDepth)
	}
}

func collectLeaves(n *Node, out *[]*Node) {
	if n.IsLeaf {
		*out = append(*out, n)
		return
	}
	for _, c := range n.Children {
		collectLeaves(c, out)
	}
}

// Leaves returns the column leaves of the schema in column order.
func (s *Schema) Leaves() []*Node { return s.leaves }

// path returns the dot-separated path of a node, excluding the schema root.
func nodePath(n *Node) []string {
	var parts []string
	for cur := n; cur != nil && cur.parent != nil; cur = cur.parent {
		parts = append(parts, cur.Name)
	}
	for i, j := 0, len(parts)-1; i < j; i, j = i+1, j-1 {
		parts[i], parts[j] = parts[j], parts[i]
	}
	return parts
}

// findChild looks up a direct child by name.
func findChild(n *Node, name string) *Node {
	for _, c := range n.Children {
		if c.Name == name {
			return c
		}
	}
	return nil
}
