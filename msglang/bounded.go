package msglang

import (
	"fmt"
)

// Verdict is the three-valued result of bounded enumeration.
type Verdict int

const (
	// VerdictNonEmpty means a witness was found within the bounds.
	VerdictNonEmpty Verdict = iota
	// VerdictEmpty means the bounded universe was exhausted and the exact
	// intersection language is empty.
	VerdictEmpty
	// VerdictUnknown means enumeration stopped because a bound was exhausted;
	// emptiness is not implied.
	VerdictUnknown
)

// String returns the stable verdict name.
func (v Verdict) String() string {
	switch v {
	case VerdictNonEmpty:
		return "NonEmpty"
	case VerdictEmpty:
		return "Empty"
	case VerdictUnknown:
		return "Unknown"
	default:
		return fmt.Sprintf("Verdict(%d)", int(v))
	}
}

// Bounds constrains finite tree enumeration.
//
// MaxNodes is the inclusive node-count limit of the enumerated universe and
// must be positive.
// MaxHeight limits the height of any generated tree (0 means no height limit).
// NodeBudget limits the cumulative nodes materialized while generating the
// finite tree universe; exhaustion yields VerdictUnknown rather than
// VerdictEmpty. A zero NodeBudget means no cumulative limit (enumeration is
// still bounded by MaxNodes and MaxHeight).
type Bounds struct {
	MaxNodes   int
	MaxHeight  int
	NodeBudget int
}

// BoundedResult is the outcome of bounded enumeration, cross-checked with the
// exact fixpoint analysis.
type BoundedResult struct {
	Verdict        Verdict
	Reason         string
	Tree           *Node
	NodeCount      int
	Height         int
	Product        *Automaton
	ProductTrace   *Derivation
	LeftTrace      *Derivation
	RightTrace     *Derivation
	NodesExamined  int
	ExactEmpty     bool
	ExactMinNodes  int
	ExactMinHeight int
}

// BoundedIntersect enumerates finite trees in increasing node count and tests
// membership against the product automaton. It never confuses an exhausted
// budget with an empty language.
func BoundedIntersect(left, right *Automaton, bounds Bounds) (*BoundedResult, error) {
	if err := bounds.Validate(); err != nil {
		return nil, err
	}
	product, err := Intersect(left, right)
	if err != nil {
		return nil, err
	}

	exact, err := minimalTree(product)
	if err != nil {
		return nil, err
	}
	exactWinner := selectFinal(product, exact)
	result := &BoundedResult{Product: product, ExactEmpty: exactWinner == nil}
	if exactWinner != nil {
		result.ExactMinNodes = exactWinner.cost
		result.ExactMinHeight = exactWinner.height
	}

	enumerator, err := newEnumerator(product, bounds)
	if err != nil {
		return nil, err
	}
	// Exact emptiness is decided by the fixpoint; enumeration of an empty
	// universe would only spend budget and can never find a witness.
	if result.ExactEmpty {
		result.Verdict = VerdictEmpty
		result.Reason = "exact product fixpoint has no reachable final state"
		result.NodesExamined = enumerator.examined
		return result, nil
	}
	sizeCap := bounds.MaxNodes
	heightCap := bounds.MaxHeight
	if heightCap == 0 {
		heightCap = sizeCap - 1
	}
	if sizeCap < result.ExactMinNodes {
		result.Verdict = VerdictUnknown
		result.Reason = fmt.Sprintf("size bound %d is below exact minimum node count %d",
			sizeCap, result.ExactMinNodes)
		result.NodesExamined = enumerator.examined
		return result, nil
	}
	if heightCap < result.ExactMinHeight {
		result.Verdict = VerdictUnknown
		result.Reason = fmt.Sprintf("height bound %d is below exact minimum height %d",
			heightCap, result.ExactMinHeight)
		result.NodesExamined = enumerator.examined
		return result, nil
	}
	rootLabels := enumerator.productiveRootLabels()

	for n := 1; n <= sizeCap; n++ {
		for _, label := range rootLabels {
			trees, stopped, err := enumerator.trees(label, n, heightCap)
			if err != nil {
				return nil, err
			}
			if stopped {
				return enumerator.unknown(result, "node budget exhausted while generating trees"), nil
			}
			for _, tree := range trees {
				ok, derivation, err := product.Accept(tree)
				if err != nil {
					return nil, err
				}
				if !ok {
					continue
				}
				result.Verdict = VerdictNonEmpty
				result.Tree = tree
				result.NodeCount = tree.NodeCount()
				result.Height = tree.Height()
				result.ProductTrace = derivation
				result.NodesExamined = enumerator.examined
				tr := traceForProductTree(product, tree, derivation)
				result.LeftTrace = projectTrace(tr, left, true)
				result.RightTrace = projectTrace(tr, right, false)
				return result, nil
			}
		}
	}

	result.NodesExamined = enumerator.examined
	// Reaching here means the exact language is non-empty and the minimum fits
	// the declared bounds, yet no witness was enumerated: the node budget was
	// not exceeded, so this is an internal inconsistency rather than emptiness.
	result.Verdict = VerdictUnknown
	result.Reason = fmt.Sprintf(
		"enumeration produced no accepted tree although exact minimum is %d node(s) within bounds",
		result.ExactMinNodes)
	return result, nil
}

// Validate checks that the bounds are usable.
func (b Bounds) Validate() error {
	if b.MaxNodes < 0 || b.MaxHeight < 0 || b.NodeBudget < 0 {
		return fmt.Errorf("msglang: bounds must be non-negative")
	}
	if b.MaxNodes == 0 {
		return fmt.Errorf("msglang: bounded enumeration requires a positive MaxNodes")
	}
	return nil
}
