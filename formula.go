// Package wafdecision compiles a Boolean WAF rule into a minimum worst-case
// cost decision DAG.
//
// A rule is a Boolean formula whose leaves are pure detector functions, each
// with an integer invocation cost and a selectivity interval. The compiler
// reorders detector evaluation while preserving the short-circuit truth
// semantics of AND/OR/NOT, guarantees that a leaf is evaluated at most once
// per request, and treats every detector failure (panic) as a deny verdict.
//
// At most 12 leaves per compiled rule are supported.
package wafdecision

// Formula is the Boolean rule provided by the caller.
//
// The supported concrete types are *Leaf, *Not, *And and *Or. A formula must
// be non-nil; empty And/Or nodes are reduced to logical constants.
type Formula interface {
	isFormula()
}

// Leaf references a detector by its leaf identifier.
type Leaf struct {
	ID int
}

// Not negates a sub-formula.
type Not struct {
	Inner Formula
}

// And is a short-circuit conjunction; children may be reordered freely.
type And struct {
	Children []Formula
}

// Or is a short-circuit disjunction; children may be reordered freely.
type Or struct {
	Children []Formula
}

func (*Leaf) isFormula() {}
func (*Not) isFormula()  {}
func (*And) isFormula()  {}
func (*Or) isFormula()   {}

// NewLeaf builds a leaf formula referencing the given detector identifier.
func NewLeaf(id int) *Leaf { return &Leaf{ID: id} }

// NotOf negates a sub-formula.
func NotOf(inner Formula) *Not { return &Not{Inner: inner} }

// AndOf builds a short-circuit conjunction.
func AndOf(children ...Formula) *And { return &And{Children: children} }

// OrOf builds a short-circuit disjunction.
func OrOf(children ...Formula) *Or { return &Or{Children: children} }

// EvalFormula evaluates a formula directly against a complete truth
// assignment. It is the semantic oracle used during exhaustive verification.
//
// Assignment a maps a leaf identifier to its Boolean detector result; leaves
// missing from the map evaluate to false.
func EvalFormula(f Formula, a map[int]bool) (bool, error) {
	if f == nil {
		return false, ErrNilFormula
	}
	switch t := f.(type) {
	case *Leaf:
		return a[t.ID], nil
	case *Not:
		v, err := EvalFormula(t.Inner, a)
		return !v, err
	case *And:
		for _, c := range t.Children {
			v, err := EvalFormula(c, a)
			if err != nil {
				return false, err
			}
			if !v {
				return false, nil
			}
		}
		return true, nil
	case *Or:
		for _, c := range t.Children {
			v, err := EvalFormula(c, a)
			if err != nil {
				return false, err
			}
			if v {
				return true, nil
			}
		}
		return false, nil
	default:
		return false, &UnsupportedFormulaError{Type: typeName(f)}
	}
}
