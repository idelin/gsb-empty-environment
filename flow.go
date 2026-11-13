package netsec

import (
	"errors"
	"sort"
)

// ErrInvalidProof is returned when an OwnerSnapshot creation request omits a
// required information-flow proof or supplies a proof that does not match the
// actual expression (protected dependency, possible error or side effect).
var ErrInvalidProof = errors.New("netsec: invalid information-flow proof")

// FlowSummary is the static information-flow summary of one output expression.
// A summary is only a valid non-interference proof when Protected is empty and
// both MayError and HasSideEffect are false.
type FlowSummary struct {
	// Protected lists every protected (tenant) column that can reach the
	// output, in canonical order.
	Protected []string
	// MayError is true when evaluation can throw (missing column, division by
	// zero, error-capable scalar function, ...).
	MayError bool
	// HasSideEffect is true when the expression invokes an impure operation.
	HasSideEffect bool
	// Digest binds the summary to a specific structural expression, so a
	// caller cannot attach a proof computed for a different expression.
	Digest string
}

// ValidProof reports whether the summary proves that an output depends on no
// protected column, cannot throw and has no side effect.
func (s FlowSummary) ValidProof() bool {
	return len(s.Protected) == 0 && !s.MayError && !s.HasSideEffect
}

// AnalyzeFlow computes the authoritative information-flow summary for an
// expression against the source schema. The protected set names tenant/RLS
// columns whose values must never leak into an OwnerSnapshot output.
func AnalyzeFlow(e Expr, schema Schema) (FlowSummary, error) {
	if err := validateExpr(e, schema.columns()); err != nil {
		return FlowSummary{}, err
	}
	s := flowState{protected: map[string]bool{}}
	for _, c := range schema.Protected {
		s.protected[c] = true
	}
	out := analyzeNode(e, s)
	out.Digest = canonicalExpr(e)
	sort.Strings(out.Protected)
	return out, nil
}

type flowState struct {
	protected map[string]bool
}

func analyzeNode(e Expr, st flowState) FlowSummary {
	switch n := e.(type) {
	case ColExpr:
		s := FlowSummary{}
		if st.protected[n.Column] {
			s.Protected = []string{n.Column}
		}
		return s
	case LitExpr:
		return FlowSummary{}
	case BinExpr:
		l := analyzeNode(n.Left, st)
		r := analyzeNode(n.Right, st)
		s := mergeFlow(l, r)
		if n.Op == OpDiv {
			s.MayError = true
		}
		return s
	case FuncExpr:
		fn := scalarFuncs[n.Name]
		s := FlowSummary{HasSideEffect: fn.sideEffect}
		for _, a := range n.Args {
			s = mergeFlow(s, analyzeNode(a, st))
		}
		if fn.mayError != nil {
			args := make([]int64, len(n.Args))
			s.MayError = s.MayError || fn.mayError(args)
		}
		return s
	case AggExpr:
		// The aggregate itself does not introduce a protected dependency; the
		// argument's flow is what matters. COUNT(*) style arguments still
		// analyze normally.
		return analyzeNode(n.Arg, st)
	default:
		return FlowSummary{MayError: true}
	}
}

func mergeFlow(a, b FlowSummary) FlowSummary {
	seen := map[string]bool{}
	protected := make([]string, 0, len(a.Protected)+len(b.Protected))
	for _, c := range append(append([]string{}, a.Protected...), b.Protected...) {
		if !seen[c] {
			seen[c] = true
			protected = append(protected, c)
		}
	}
	sort.Strings(protected)
	return FlowSummary{
		Protected:     protected,
		MayError:      a.MayError || b.MayError,
		HasSideEffect: a.HasSideEffect || b.HasSideEffect,
	}
}

// FlowProof is the proof a caller must attach to every OwnerSnapshot output.
type FlowProof struct {
	Summary FlowSummary
}

// verifyProof checks the caller-supplied proof against the host-computed
// summary. A missing proof, a digest mismatch, or a summary that still depends
// on protected data / can error / has a side effect rejects creation.
func verifyProof(e Expr, schema Schema, proof *FlowProof) error {
	if proof == nil {
		return ErrInvalidProof
	}
	got, err := AnalyzeFlow(e, schema)
	if err != nil {
		return err
	}
	if proof.Summary.Digest != got.Digest {
		return ErrInvalidProof
	}
	if !proof.Summary.ValidProof() || !got.ValidProof() {
		return ErrInvalidProof
	}
	if !equalStrings(proof.Summary.Protected, got.Protected) ||
		proof.Summary.MayError != got.MayError ||
		proof.Summary.HasSideEffect != got.HasSideEffect {
		return ErrInvalidProof
	}
	return nil
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
