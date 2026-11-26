package joinenum

import (
	"errors"
	"fmt"
)

// OpKind is the kind of a binary join operator.
type OpKind uint8

const (
	// InnerJoin is the ordinary commutative and associative join operator.
	InnerJoin OpKind = iota
	// LeftOuterJoin preserves all rows of its left input and null-extends its
	// right input when no matching row exists.
	LeftOuterJoin
	// SemiJoin returns the left rows for which at least one matching right row
	// exists.  Its output schema is the left schema.
	SemiJoin
	// AntiJoin returns the left rows for which no matching right row exists.
	// Its output schema is the left schema.
	AntiJoin
)

func (k OpKind) valid() bool { return k <= AntiJoin }

func (k OpKind) String() string {
	switch k {
	case InnerJoin:
		return "InnerJoin"
	case LeftOuterJoin:
		return "LeftOuterJoin"
	case SemiJoin:
		return "SemiJoin"
	case AntiJoin:
		return "AntiJoin"
	default:
		return fmt.Sprintf("OpKind(%d)", uint8(k))
	}
}

// Token is the one-letter operator token used by the canonical pre-order
// encoding.  It is part of the deterministic tie-breaking contract.
func (k OpKind) Token() byte {
	switch k {
	case InnerJoin:
		return 'I'
	case LeftOuterJoin:
		return 'L'
	case SemiJoin:
		return 'S'
	case AntiJoin:
		return 'A'
	default:
		return '?'
	}
}

// Relation describes a base table.  Cardinality is the exact row count used
// by the integer cost model.  Attributes are only needed by the optional
// real-data evaluator.
type Relation struct {
	Name        string
	Cardinality int64
	Attributes  []string
}

// Selectivity is an exact rational selectivity in [0, 1].
type Selectivity struct {
	Num int64
	Den int64
}

// Sel returns the exact selectivity num/den.
func Sel(num, den int64) Selectivity { return Selectivity{Num: num, Den: den} }

// One is the selectivity of a predicate that matches every pair.
var One = Selectivity{Num: 1, Den: 1}

func (s Selectivity) validate() error {
	if s.Den <= 0 {
		return fmt.Errorf("joinenum: selectivity denominator must be positive, got %d", s.Den)
	}
	if s.Num < 0 || s.Num > s.Den {
		return fmt.Errorf("joinenum: selectivity %d/%d is outside [0,1]", s.Num, s.Den)
	}
	return nil
}

func (s Selectivity) String() string { return fmt.Sprintf("%d/%d", s.Num, s.Den) }

// Column identifies one attribute of one base relation.
type Column struct {
	Rel  int
	Attr string
}

// Value is a scalar value used by the optional evaluator.  Null values are
// represented by Null=true; the other fields are then ignored.
type Value struct {
	Null bool
	Int  int64
	Str  string
}

// IntValue returns a non-null integer value.
func IntValue(v int64) Value { return Value{Int: v} }

// StringValue returns a non-null string value.
func StringValue(v string) Value { return Value{Str: v} }

// NullValue returns the SQL NULL value.
func NullValue() Value { return Value{Null: true} }

// Tuple is a row flowing through an evaluated plan.  A relation is present in
// the map iff its columns are part of the tuple's schema.  Null-extended
// relations are present with all attribute values equal to NullValue.
type Tuple map[Column]Value

// MatchFunc evaluates a predicate over a tuple.  SQL three-valued logic is
// reduced to "does this row match", i.e. only TRUE counts as a match.
type MatchFunc func(Tuple) bool

// Predicate is a join predicate together with its optimizer metadata.
//
// Refs is authoritative for the optimizer: it lists every base relation whose
// attributes occur in the predicate.  Selectivity is the exact estimated
// fraction of input pairs satisfying the predicate.
//
// NullPreserving marks predicates such as "B.x IS NULL" that can be TRUE when
// the null-supplying input has been null-extended.  The zero value (false)
// denotes the usual null-rejecting comparison semantics.  This flag is used
// only when deciding whether a left-outer join may be re-associated into a
// null-producing input.
type Predicate struct {
	Name           string
	Refs           []int
	Selectivity    Selectivity
	NullPreserving bool
	Match          MatchFunc
}

func (p *Predicate) refSet() RelationSet {
	if p == nil {
		return 0
	}
	var s RelationSet
	for _, r := range p.Refs {
		if r >= 0 && r < 63 {
			s = s.Add(r)
		}
	}
	return s
}

func (p *Predicate) clone() *Predicate {
	if p == nil {
		return nil
	}
	cp := *p
	cp.Refs = append([]int(nil), p.Refs...)
	return &cp
}

// Node is a node of the input syntax tree.  Leaves are created with Rel;
// internal nodes with Inner, LeftOuter, Semi or Anti.  An internal node has
// non-nil Left/Right and Pred and an unused Leaf value; a leaf has Leaf >= 0.
type Node struct {
	Leaf  int
	Kind  OpKind
	Pred  *Predicate
	Left  *Node
	Right *Node
}

// Rel returns a base-relation leaf.
func Rel(index int) *Node { return &Node{Leaf: index} }

// Inner returns an inner-join node.
func Inner(left, right *Node, pred *Predicate) *Node {
	return &Node{Leaf: -1, Kind: InnerJoin, Left: left, Right: right, Pred: pred}
}

// LeftOuter returns a left-outer-join node.
func LeftOuter(left, right *Node, pred *Predicate) *Node {
	return &Node{Leaf: -1, Kind: LeftOuterJoin, Left: left, Right: right, Pred: pred}
}

// Semi returns a semi-join node.
func Semi(left, right *Node, pred *Predicate) *Node {
	return &Node{Leaf: -1, Kind: SemiJoin, Left: left, Right: right, Pred: pred}
}

// Anti returns an anti-join node.
func Anti(left, right *Node, pred *Predicate) *Node {
	return &Node{Leaf: -1, Kind: AntiJoin, Left: left, Right: right, Pred: pred}
}

// IsLeaf reports whether n is a base-relation leaf.
func (n *Node) IsLeaf() bool { return n != nil && n.Left == nil && n.Right == nil && n.Leaf >= 0 }

// Input is the complete optimizer input.
type Input struct {
	Relations []Relation
	Root      *Node
	// Cost is optional.  When nil, DefaultCostModel is used.
	Cost CostModel
}

// Side identifies which child of an operator contains a relation or operator.
type Side uint8

const (
	// RootSide is used for the root operator and for leaves.
	RootSide Side = iota
	// LeftSide is the preserved (non-null-supplying) child of an operator.
	LeftSide
	// RightSide is the protected (null-supplying/existential) child.
	RightSide
)

func (s Side) String() string {
	switch s {
	case LeftSide:
		return "left"
	case RightSide:
		return "right"
	default:
		return "root"
	}
}

var (
	// ErrInvalidInput reports a malformed syntax tree, relation reference or
	// predicate.
	ErrInvalidInput = errors.New("joinenum: invalid input")
	// ErrCardinalityOverflow reports an overflow in the exact integer cost
	// model.
	ErrCardinalityOverflow = errors.New("joinenum: integer cost overflow")
)
