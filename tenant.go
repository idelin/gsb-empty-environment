// Package tenantfilter provides SQL rewriting middleware that confines every
// query to the tenant stored in the request context.
package tenantfilter

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
)

// DefaultTenantColumn is used when Config.TenantColumn is empty.
const DefaultTenantColumn = "tenant_id"

var (
	// ErrNoTenant means that Rewrite was called without an authenticated tenant.
	ErrNoTenant = errors.New("tenantfilter: no tenant in context")
	// ErrTenantOverride means a query tries to supply its own tenant predicate.
	ErrTenantOverride = errors.New("tenantfilter: query attempts to override the tenant predicate")
	// ErrUnsupportedQuery means the statement form is not guarded by this package.
	ErrUnsupportedQuery = errors.New("tenantfilter: unsupported query form")
	// ErrMalformedQuery means the SQL could not be tokenized or structurally parsed.
	ErrMalformedQuery = errors.New("tenantfilter: malformed query")
	// ErrMultipleStatements rejects batching, which could hide an unguarded statement.
	ErrMultipleStatements = errors.New("tenantfilter: multiple SQL statements are not allowed")
	// ErrArgumentMismatch means the supplied arguments do not match the placeholders.
	ErrArgumentMismatch = errors.New("tenantfilter: query argument count does not match placeholders")
)

// OverrideError describes an explicit tenant-column reference found in a
// filtering clause (WHERE, HAVING, ON, USING, QUALIFY or SET).
type OverrideError struct {
	Offset     int
	Identifier string
}

func (e *OverrideError) Error() string {
	return fmt.Sprintf("%v at byte %d (%s)", ErrTenantOverride, e.Offset, e.Identifier)
}

// Unwrap lets errors.Is(err, ErrTenantOverride) succeed.
func (e *OverrideError) Unwrap() error { return ErrTenantOverride }

type tenantContextKey struct{}

// WithTenant attaches an authenticated tenant identifier to ctx. Applications
// should only call this after authentication has resolved the tenant.
func WithTenant(ctx context.Context, tenantID any) context.Context {
	return context.WithValue(ctx, tenantContextKey{}, tenantID)
}

// TenantFromContext returns the tenant identifier attached by WithTenant.
func TenantFromContext(ctx context.Context) (any, bool) {
	if ctx == nil {
		return nil, false
	}
	value := ctx.Value(tenantContextKey{})
	return value, value != nil
}

func validTenant(value any) bool {
	if value == nil {
		return false
	}
	if text, ok := value.(string); ok {
		return strings.TrimSpace(text) != ""
	}
	return true
}

// PlaceholderStyle selects the SQL parameter syntax used while rendering.
type PlaceholderStyle uint8

const (
	// PlaceholderAuto uses $1-style placeholders when the input already uses
	// them, otherwise it uses ?.
	PlaceholderAuto PlaceholderStyle = iota
	// PlaceholderQuestion renders ? placeholders.
	PlaceholderQuestion
	// PlaceholderDollar renders $1, $2, ... placeholders.
	PlaceholderDollar
)

// Config controls tenant-column resolution.
type Config struct {
	// TenantColumn is the default tenant column. It defaults to tenant_id.
	TenantColumn string
	// TableTenantColumns overrides the tenant column for specific tables.
	// Keys may be unqualified ("orders") or schema-qualified ("public.orders").
	TableTenantColumns map[string]string
	// GlobalTables are the only tables exempt from tenant filtering. Entries
	// are explicit and may be unqualified or schema-qualified.
	GlobalTables []string
	// Placeholder controls the output parameter syntax.
	Placeholder PlaceholderStyle
}

// Guard rewrites SQL so that it cannot read or modify another tenant's rows.
type Guard struct {
	tenantColumn  string
	tableColumns  map[string]string
	globalTables  map[string]struct{}
	tenantColumns map[string]struct{}
	placeholder   PlaceholderStyle
}

var columnNameRE = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// NewGuard validates cfg and returns a reusable Guard. A Guard is immutable
// after construction and safe for concurrent use.
func NewGuard(cfg Config) (*Guard, error) {
	column := strings.TrimSpace(cfg.TenantColumn)
	if column == "" {
		column = DefaultTenantColumn
	}
	if !columnNameRE.MatchString(column) {
		return nil, fmt.Errorf("tenantfilter: invalid tenant column %q", cfg.TenantColumn)
	}

	g := &Guard{
		tenantColumn:  column,
		tableColumns:  make(map[string]string, len(cfg.TableTenantColumns)),
		globalTables:  make(map[string]struct{}, len(cfg.GlobalTables)),
		tenantColumns: map[string]struct{}{strings.ToLower(column): {}},
		placeholder:   cfg.Placeholder,
	}
	if g.placeholder > PlaceholderDollar {
		return nil, fmt.Errorf("tenantfilter: invalid placeholder style %d", cfg.Placeholder)
	}

	for table, tableColumn := range cfg.TableTenantColumns {
		key := normalizeTableName(table)
		if key == "" {
			return nil, fmt.Errorf("tenantfilter: empty table name in TableTenantColumns")
		}
		tableColumn = strings.TrimSpace(tableColumn)
		if !columnNameRE.MatchString(tableColumn) {
			return nil, fmt.Errorf("tenantfilter: invalid tenant column %q for table %q", tableColumn, table)
		}
		g.tableColumns[key] = tableColumn
		g.tenantColumns[strings.ToLower(tableColumn)] = struct{}{}
	}
	for _, table := range cfg.GlobalTables {
		key := normalizeTableName(table)
		if key == "" {
			return nil, fmt.Errorf("tenantfilter: empty table name in GlobalTables")
		}
		g.globalTables[key] = struct{}{}
	}
	return g, nil
}

// MustNewGuard is NewGuard with a panic on invalid configuration.
func MustNewGuard(cfg Config) *Guard {
	guard, err := NewGuard(cfg)
	if err != nil {
		panic(err)
	}
	return guard
}

// TenantColumn returns the default tenant column used by the guard.
func (g *Guard) TenantColumn() string {
	if g == nil {
		return ""
	}
	return g.tenantColumn
}

func (g *Guard) columnFor(table string, unqualified string) (string, bool) {
	if g.isGlobal(table, unqualified) {
		return "", false
	}
	if column, ok := g.tableColumns[table]; ok {
		return column, true
	}
	if column, ok := g.tableColumns[unqualified]; ok {
		return column, true
	}
	return g.tenantColumn, true
}

func (g *Guard) isGlobal(table string, unqualified string) bool {
	if _, ok := g.globalTables[table]; ok {
		return true
	}
	_, ok := g.globalTables[unqualified]
	return ok
}

func normalizeTableName(name string) string {
	var parts []string
	var current strings.Builder
	var quote byte
	for i := 0; i < len(name); i++ {
		c := name[i]
		if quote != 0 {
			current.WriteByte(c)
			if c == quote {
				if i+1 < len(name) && name[i+1] == quote {
					current.WriteByte(name[i+1])
					i++
					continue
				}
				quote = 0
			}
			continue
		}
		switch c {
		case '"', '`':
			quote = c
			current.WriteByte(c)
		case '[':
			quote = ']'
			current.WriteByte(c)
		case '.':
			parts = append(parts, strings.TrimSpace(current.String()))
			current.Reset()
		default:
			current.WriteByte(c)
		}
	}
	parts = append(parts, strings.TrimSpace(current.String()))
	for i, part := range parts {
		parts[i] = normalizeTablePart(part)
	}
	return strings.Join(parts, ".")
}

func normalizeTablePart(part string) string {
	switch {
	case strings.HasPrefix(part, `"`), strings.HasPrefix(part, "`"), strings.HasPrefix(part, "["):
		return unquoteIdentifier(part)
	default:
		return strings.ToLower(unquoteIdentifier(part))
	}
}

// Rewrite injects tenant predicates into query and returns the rewritten SQL
// with the tenant argument(s) in textual placeholder order.
func (g *Guard) Rewrite(ctx context.Context, query string, args []any) (string, []any, error) {
	if g == nil || g.tenantColumn == "" {
		return "", nil, fmt.Errorf("tenantfilter: nil or uninitialized guard")
	}
	tenant, ok := TenantFromContext(ctx)
	if !ok || !validTenant(tenant) {
		return "", nil, ErrNoTenant
	}
	return g.rewrite(query, args, tenant)
}
