package condbuilder

import (
	"fmt"
	"strings"
)

// UnionType selects the UNION variant.
type UnionType string

const (
	UnionDistinct UnionType = "UNION"
	UnionAll      UnionType = "UNION ALL"
)

// UnionBuilder combines several SELECT branches. Every branch must use the
// exact same Whitelist instance, project the same ordered column set, and the
// overall ORDER BY may only reference columns that appear in the result set —
// this closes the "order by a hidden column" and per-branch whitelist bypass
// holes.
type UnionBuilder struct {
	wl       *Whitelist
	branches []*SelectBuilder
	utype    UnionType
	orders   []Order
	limit    int
	hasLimit bool
}

// NewUnion starts a UNION query and registers its first branch.
func NewUnion(first *SelectBuilder, ut UnionType) (*UnionBuilder, error) {
	if first == nil {
		return nil, fmt.Errorf("union requires a first branch")
	}
	if ut != UnionDistinct && ut != UnionAll {
		return nil, fmt.Errorf("invalid union type %q", ut)
	}
	return &UnionBuilder{wl: first.wl, branches: []*SelectBuilder{first}, utype: ut}, nil
}

// Add registers another branch. It must share the first branch's whitelist
// (same instance) and project the identical column sequence.
func (u *UnionBuilder) Add(branch *SelectBuilder) (*UnionBuilder, error) {
	if branch == nil {
		return nil, fmt.Errorf("union branch must not be nil")
	}
	if branch.wl != u.wl {
		return nil, fmt.Errorf("union branch uses a different whitelist: all branches must share the same whitelist instance")
	}
	want := u.branches[0].Columns()
	got := branch.Columns()
	if len(got) != len(want) {
		return nil, fmt.Errorf("union branch column count %d does not match %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			return nil, fmt.Errorf("union branch column %d is %q, expected %q", i, got[i], want[i])
		}
	}
	if len(branch.orders) != 0 {
		return nil, fmt.Errorf("per-branch ORDER BY is not allowed in a UNION; order the combined result instead")
	}
	u.branches = append(u.branches, branch)
	return u, nil
}

// OrderBy sets the ordering of the combined result. Each field must be one of
// the result-set columns; fields that exist in the table but were not
// projected are rejected.
func (u *UnionBuilder) OrderBy(orders ...Order) (*UnionBuilder, error) {
	resultCols := u.branches[0].Columns()
	allowed := make(map[string]bool, len(resultCols))
	for _, col := range resultCols {
		allowed[col] = true
	}
	for _, o := range orders {
		if !allowed[o.Field] {
			return nil, fmt.Errorf("union ORDER BY field %q is not part of the result set %v", o.Field, resultCols)
		}
		if o.Dir != Asc && o.Dir != Desc {
			return nil, fmt.Errorf("union ORDER BY field %q has invalid direction %q", o.Field, o.Dir)
		}
	}
	u.orders = append(u.orders, orders...)
	return u, nil
}

// Limit sets a limit over the combined result.
func (u *UnionBuilder) Limit(n int) (*UnionBuilder, error) {
	if n < 0 {
		return nil, fmt.Errorf("limit must be non-negative, got %d", n)
	}
	u.limit, u.hasLimit = n, true
	return u, nil
}

// Build renders and structurally validates the full UNION statement,
// concatenating branch args in order.
func (u *UnionBuilder) Build() (string, []any, error) {
	var blocks []string
	var args []any
	for _, branch := range u.branches {
		sql, branchArgs, err := branch.Build()
		if err != nil {
			return "", nil, err
		}
		blocks = append(blocks, "("+sql+")")
		args = append(args, branchArgs...)
	}

	parts := []string{strings.Join(blocks, " "+string(u.utype)+" ")}

	orderSQL, err := renderOrders(u.wl, u.orders)
	if err != nil {
		return "", nil, err
	}
	if orderSQL != "" {
		parts = append(parts, orderSQL)
	}
	if u.hasLimit {
		parts = append(parts, fmt.Sprintf("LIMIT %d", u.limit))
	}

	sql := strings.Join(parts, " ")
	if err := ValidateStructure(sql); err != nil {
		return "", nil, fmt.Errorf("structural validation failed: %w", err)
	}
	return sql, args, nil
}
