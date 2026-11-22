package recipereview

// SymbolicStatus classifies a structural proof attempt.
type SymbolicStatus int

const (
	// SymbolicInconclusive means the structural rules cannot settle the
	// property and enumeration must decide it.
	SymbolicInconclusive SymbolicStatus = iota
	// SymbolicProven means the property holds for every allowed assignment.
	SymbolicProven
)

// ProofResult is the outcome of one symbolic check.
type ProofResult struct {
	Status SymbolicStatus
	Reason string
}

// symbolicDomain tracks fields known to be non-empty along a branch arm.
type symbolicDomain map[string]bool

// firstBytes returns an over-approximation of the set of bytes that can appear
// first in the encoding of e under the given non-empty domain. A nil result
// means the expression can produce the empty string and has no first byte.
func firstBytes(r *Recipe, e Expr, dom symbolicDomain) map[byte]bool {
	switch n := e.(type) {
	case *ConstExpr:
		if len(n.Value) == 0 {
			return nil
		}
		return map[byte]bool{n.Value[0]: true}
	case *RawExpr:
		f := r.Fields[n.Field]
		set := map[byte]bool{}
		for _, c := range f.Alphabet.sortedBytes() {
			set[c] = true
		}
		return set
	case *PrefixExpr:
		f := r.Fields[n.Field]
		set := map[byte]bool{}
		for length := f.MinLen; length <= f.MaxLen; length++ {
			set[byte(length)] = true
		}
		return set
	case *EscapeExpr:
		f := r.Fields[n.Field]
		set := map[byte]bool{}
		if f.MinLen == 0 && !dom[n.Field] {
			set[n.Terminator] = true
		}
		set[n.Escape] = true
		for _, c := range f.Alphabet.sortedBytes() {
			if c != n.Escape && c != n.Terminator {
				set[c] = true
			}
		}
		return set
	case *BranchExpr:
		childDom := cloneSymbolic(dom)
		childDom[n.Field] = true
		set := map[byte]bool{}
		mergeFirst(set, firstBytes(r, n.IfEmpty, dom))
		mergeFirst(set, firstBytes(r, n.IfNonEmpty, childDom))
		if len(set) == 0 {
			return nil
		}
		return set
	case *SeqExpr:
		set := map[byte]bool{}
		for _, part := range n.Parts {
			fb := firstBytes(r, part, dom)
			mergeFirst(set, fb)
			if fb != nil {
				break
			}
		}
		if len(set) == 0 {
			return nil
		}
		return set
	}
	return nil
}

func mergeFirst(dst, src map[byte]bool) {
	for c := range src {
		dst[c] = true
	}
}

func cloneSymbolic(in symbolicDomain) symbolicDomain {
	out := make(symbolicDomain, len(in)+1)
	for k := range in {
		out[k] = true
	}
	return out
}

func disjointSets(a, b map[byte]bool) bool {
	if a == nil || b == nil {
		return false
	}
	for c := range a {
		if b[c] {
			return false
		}
	}
	return true
}

// proveInjective structurally proves that a recipe maps distinct assignments
// to distinct info strings. It is sound but deliberately conservative: any
// construct it cannot reason about falls back to enumeration.
func proveInjective(r *Recipe) ProofResult {
	return injectivityOf(r, r.Root, symbolicDomain{})
}

func injectivityOf(r *Recipe, e Expr, dom symbolicDomain) ProofResult {
	switch n := e.(type) {
	case *ConstExpr:
		return ProofResult{Status: SymbolicProven}
	case *RawExpr:
		// A fixed-length field taken verbatim is injective on its alphabet.
		return ProofResult{Status: SymbolicProven}
	case *PrefixExpr:
		// Length framing makes a variable-length verbatim copy injective.
		return ProofResult{Status: SymbolicProven}
	case *EscapeExpr:
		// Escaping plus a bare terminator uniquely delimits the value.
		return ProofResult{Status: SymbolicProven}
	case *BranchExpr:
		childDom := cloneSymbolic(dom)
		childDom[n.Field] = true
		if pr := injectivityOf(r, n.IfEmpty, dom); pr.Status != SymbolicProven {
			return pr
		}
		if pr := injectivityOf(r, n.IfNonEmpty, childDom); pr.Status != SymbolicProven {
			return pr
		}
		emptyFirst := firstBytes(r, n.IfEmpty, dom)
		nonEmptyFirst := firstBytes(r, n.IfNonEmpty, childDom)
		if !disjointSets(emptyFirst, nonEmptyFirst) {
			return ProofResult{
				Status: SymbolicInconclusive,
				Reason: "branch arms share a possible first byte; emptiness partition not proven disjoint",
			}
		}
		return ProofResult{Status: SymbolicProven}
	case *SeqExpr:
		for _, part := range n.Parts {
			if pr := injectivityOf(r, part, dom); pr.Status != SymbolicProven {
				return pr
			}
		}
		return ProofResult{Status: SymbolicProven}
	}
	return ProofResult{Status: SymbolicInconclusive, Reason: "unknown expression node"}
}

// proveDistinct structurally proves that two different-purpose recipes can
// never collide. The conservative rule only discharges branch-free recipes
// whose root encodings start from disjoint byte sets.
func proveDistinct(r1, r2 *Recipe) ProofResult {
	if containsBranch(r1.Root) || containsBranch(r2.Root) {
		return ProofResult{Status: SymbolicInconclusive, Reason: "branched recipes require enumeration"}
	}
	first1 := firstBytes(r1, r1.Root, symbolicDomain{})
	first2 := firstBytes(r2, r2.Root, symbolicDomain{})
	if !disjointSets(first1, first2) {
		return ProofResult{Status: SymbolicInconclusive, Reason: "root first-byte sets overlap"}
	}
	return ProofResult{Status: SymbolicProven}
}

func containsBranch(e Expr) bool {
	switch n := e.(type) {
	case *BranchExpr:
		return true
	case *SeqExpr:
		for _, part := range n.Parts {
			if containsBranch(part) {
				return true
			}
		}
	}
	return false
}
