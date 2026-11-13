package netsec

import (
	"errors"
	"sort"
)

// RefreshMode selects how a view is materialized and shared.
type RefreshMode string

const (
	// OwnerSnapshot stores a single public aggregate computed without any RLS.
	// It may hold only tenant-free aggregates backed by valid flow proofs.
	OwnerSnapshot RefreshMode = "OwnerSnapshot"
	// PerPolicyPartition stores one partition per (policyDigest, subjectClass)
	// so each querier only sees rows permitted by its current policy.
	PerPolicyPartition RefreshMode = "PerPolicyPartition"
)

// Output is one projected column.
type Output struct {
	Name string
	Expr Expr
	// Proof is required (and validated) for every OwnerSnapshot output.
	Proof *FlowProof
}

// Projection describes the grouping and output expressions of a view.
type Projection struct {
	// GroupBy lists source columns forming the grouping key.
	GroupBy []string
	Outputs []Output
}

// ViewDef is the immutable definition of a materialized view.
type ViewDef struct {
	Name             string
	SourceID         string
	SourceVersion    int64
	OwnerID          string
	Mode             RefreshMode
	Projection       Projection
	DefinitionDigest string
}

// ErrInvalidView is returned for malformed view definitions.
var ErrInvalidView = errors.New("netsec: invalid view definition")

// NewViewDef validates and builds a view definition. For OwnerSnapshot every
// output must carry a valid information-flow proof and the view must contain
// at least one aggregate (a public aggregate, never raw tenant rows).
func NewViewDef(name, ownerID string, src *Source, mode RefreshMode, proj Projection) (*ViewDef, error) {
	if name == "" || ownerID == "" || src == nil {
		return nil, ErrInvalidView
	}
	if mode != OwnerSnapshot && mode != PerPolicyPartition {
		return nil, ErrInvalidView
	}
	if len(proj.Outputs) == 0 {
		return nil, ErrInvalidView
	}
	schema := src.schema
	for _, g := range proj.GroupBy {
		if !schema.columns()[g] {
			return nil, ErrInvalidView
		}
	}
	hasAgg := false
	groupSet := map[string]bool{}
	for _, g := range proj.GroupBy {
		groupSet[g] = true
	}
	for _, out := range proj.Outputs {
		if out.Name == "" || out.Expr == nil {
			return nil, ErrInvalidView
		}
		if err := validateExpr(out.Expr, schema.columns()); err != nil {
			return nil, err
		}
		if containsAgg(out.Expr) {
			hasAgg = true
		}
		if mode == OwnerSnapshot {
			if err := verifyProof(out.Expr, schema, out.Proof); err != nil {
				return nil, err
			}
		}
		if !containsAgg(out.Expr) && len(proj.GroupBy) > 0 {
			// A non-aggregated output is only well defined when it depends
			// exclusively on grouping columns.
			if err := requireGroupColumns(out.Expr, groupSet); err != nil {
				return nil, err
			}
		}
	}
	if mode == OwnerSnapshot && !hasAgg {
		return nil, errors.New("netsec: OwnerSnapshot requires a public aggregate")
	}

	v := &ViewDef{
		Name:          name,
		SourceID:      src.id,
		SourceVersion: src.version,
		OwnerID:       ownerID,
		Mode:          mode,
		Projection:    proj,
	}
	v.DefinitionDigest = v.computeDigest()
	return v, nil
}

func requireGroupColumns(e Expr, groupSet map[string]bool) error {
	switch n := e.(type) {
	case ColExpr:
		if !groupSet[n.Column] {
			return ErrInvalidView
		}
	case BinExpr:
		if err := requireGroupColumns(n.Left, groupSet); err != nil {
			return err
		}
		return requireGroupColumns(n.Right, groupSet)
	case FuncExpr:
		for _, a := range n.Args {
			if err := requireGroupColumns(a, groupSet); err != nil {
				return err
			}
		}
	case LitExpr:
	default:
		return ErrInvalidView
	}
	return nil
}

// RequiresRetractable reports whether every aggregate in the view supports
// monoid retraction. False means incremental refresh must fall back to a full
// refresh.
func (v *ViewDef) RequiresRetractable() bool {
	for _, out := range v.Projection.Outputs {
		if exprHasNonRetractable(out.Expr) {
			return false
		}
	}
	return true
}

func exprHasNonRetractable(e Expr) bool {
	switch n := e.(type) {
	case AggExpr:
		if !n.Kind.Retractable() {
			return true
		}
		return exprHasNonRetractable(n.Arg)
	case BinExpr:
		return exprHasNonRetractable(n.Left) || exprHasNonRetractable(n.Right)
	case FuncExpr:
		for _, a := range n.Args {
			if exprHasNonRetractable(a) {
				return true
			}
		}
		return false
	default:
		return false
	}
}

func (v *ViewDef) computeDigest() string {
	parts := []string{"view", v.Name, v.SourceID, itoa(v.SourceVersion), v.OwnerID, string(v.Mode)}
	gb := append([]string{}, v.Projection.GroupBy...)
	sort.Strings(gb)
	parts = append(parts, "gb="+joinParts(gb))
	for _, out := range v.Projection.Outputs {
		parts = append(parts, "out="+out.Name+":"+canonicalExpr(out.Expr))
	}
	return digest(parts...)
}

// aggregateKinds returns the aggregate kind for each output that is an
// aggregate, in output order, with -1 for non-aggregate outputs.
func (v *ViewDef) aggregateKinds() []AggKind {
	kinds := make([]AggKind, len(v.Projection.Outputs))
	for i, out := range v.Projection.Outputs {
		kinds[i] = topAggKind(out.Expr)
	}
	return kinds
}

// topAggKind returns the aggregate kind when the output root is an aggregate.
func topAggKind(e Expr) AggKind {
	if agg, ok := e.(AggExpr); ok {
		return agg.Kind
	}
	return ""
}
