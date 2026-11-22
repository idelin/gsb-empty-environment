package condbuilder

import (
	"fmt"
	"strings"
)

// Clause is the boolean operator used to combine WHERE filters.
type Clause string

const (
	And Clause = "AND"
	Or  Clause = "OR"
)

// SelectBuilder assembles one SELECT branch: a fixed projection list, a
// whitelisted/parameterized WHERE clause and whitelisted ORDER BY keys.
type SelectBuilder struct {
	wl       *Whitelist
	table    string
	columns  []string // logical whitelist names
	filters  []Filter
	combiner Clause
	orders   []Order
	limit    int
	hasLimit bool
}

// NewSelect starts a branch bound to a whitelist. The table name and every
// projected column must be present in the whitelist.
func NewSelect(wl *Whitelist, table string, columns []string) (*SelectBuilder, error) {
	if wl == nil {
		return nil, fmt.Errorf("select requires a non-nil whitelist")
	}
	if !validIdentifier(table) {
		return nil, fmt.Errorf("invalid table name %q", table)
	}
	if len(columns) == 0 {
		return nil, fmt.Errorf("select requires at least one column")
	}
	seen := make(map[string]bool, len(columns))
	for _, col := range columns {
		if !wl.Allow(col) {
			return nil, fmt.Errorf("column %q is not in the whitelist", col)
		}
		if seen[col] {
			return nil, fmt.Errorf("duplicate projected column %q", col)
		}
		seen[col] = true
	}
	cols := append([]string(nil), columns...)
	return &SelectBuilder{wl: wl, table: table, columns: cols, combiner: And}, nil
}

// Where appends a filter predicate.
func (s *SelectBuilder) Where(f Filter) *SelectBuilder {
	s.filters = append(s.filters, f)
	return s
}

// CombineWith switches the connector between filters (default AND).
func (s *SelectBuilder) CombineWith(c Clause) (*SelectBuilder, error) {
	if c != And && c != Or {
		return nil, fmt.Errorf("invalid clause connector %q", c)
	}
	s.combiner = c
	return s, nil
}

// OrderBy appends whitelisted sort keys.
func (s *SelectBuilder) OrderBy(orders ...Order) *SelectBuilder {
	s.orders = append(s.orders, orders...)
	return s
}

// Limit sets a row limit; negative values are rejected.
func (s *SelectBuilder) Limit(n int) (*SelectBuilder, error) {
	if n < 0 {
		return nil, fmt.Errorf("limit must be non-negative, got %d", n)
	}
	s.limit, s.hasLimit = n, true
	return s, nil
}

// Columns exposes the (validated) projection list for UNION compatibility
// checks.
func (s *SelectBuilder) Columns() []string { return append([]string(nil), s.columns...) }

// Build renders the branch and runs a structural validation over the finished
// SQL text before returning it. Bound values are returned separately and are
// never part of the SQL string.
func (s *SelectBuilder) Build() (string, []any, error) {
	projection := make([]string, 0, len(s.columns))
	for _, col := range s.columns {
		ident, err := s.wl.render(col)
		if err != nil {
			return "", nil, err
		}
		projection = append(projection, ident)
	}

	var sqlParts []string
	var args []any
	sqlParts = append(sqlParts,
		"SELECT "+strings.Join(projection, ", ")+" FROM "+quoteIdent(s.table))

	if len(s.filters) > 0 {
		predicates := make([]string, 0, len(s.filters))
		for _, f := range s.filters {
			pred, fArgs, err := f.render(s.wl)
			if err != nil {
				return "", nil, err
			}
			predicates = append(predicates, pred)
			args = append(args, fArgs...)
		}
		sqlParts = append(sqlParts,
			"WHERE "+strings.Join(predicates, " "+string(s.combiner)+" "))
	}

	orderSQL, err := renderOrders(s.wl, s.orders)
	if err != nil {
		return "", nil, err
	}
	if orderSQL != "" {
		sqlParts = append(sqlParts, orderSQL)
	}
	if s.hasLimit {
		sqlParts = append(sqlParts, fmt.Sprintf("LIMIT %d", s.limit))
	}

	sql := strings.Join(sqlParts, " ")
	if err := ValidateStructure(sql); err != nil {
		return "", nil, fmt.Errorf("structural validation failed: %w", err)
	}
	return sql, args, nil
}
