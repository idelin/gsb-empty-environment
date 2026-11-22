package condbuilder

import (
	"fmt"
	"sort"
	"strings"
)

// IdentifierPattern is the restricted character set allowed inside an
// identifier. External values can never be interpolated into identifiers:
// every identifier must be declared up-front in a Whitelist and must match
// this pattern before it is accepted.
const IdentifierPattern = `^[A-Za-z_][A-Za-z0-9_]*$`

// Whitelist is the single source of truth for every column identifier a
// query is allowed to reference. Identifiers are stored as logical names
// (the names used by callers) and rendered as quoted, structure-checked SQL
// identifiers. A nil Whitelist rejects every field.
type Whitelist struct {
	// logicalName -> canonical "table"."column" pieces (already validated)
	fields map[string]fieldSpec
}

type fieldSpec struct {
	logical string
	table   string // may be empty
	column  string
}

var identifierMaxLen = 63

// NewWhitelist builds a Whitelist from logical field names such as "id" or
// "users.name". Every component must match IdentifierPattern. Field names are
// case-sensitive logical keys.
func NewWhitelist(fields ...string) (*Whitelist, error) {
	wl := &Whitelist{fields: make(map[string]fieldSpec, len(fields))}
	for _, raw := range fields {
		spec, err := parseField(raw)
		if err != nil {
			return nil, fmt.Errorf("whitelist field %q: %w", raw, err)
		}
		if _, exists := wl.fields[spec.logical]; exists {
			return nil, fmt.Errorf("whitelist field %q: duplicated", raw)
		}
		wl.fields[spec.logical] = spec
	}
	return wl, nil
}

func parseField(raw string) (fieldSpec, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return fieldSpec{}, fmt.Errorf("empty field name")
	}
	parts := strings.Split(raw, ".")
	if len(parts) > 2 {
		return fieldSpec{}, fmt.Errorf("at most one table qualifier allowed")
	}
	for _, part := range parts {
		if !validIdentifier(part) {
			return fieldSpec{}, fmt.Errorf("invalid identifier %q (must match %s)", part, IdentifierPattern)
		}
	}
	spec := fieldSpec{logical: raw, column: parts[len(parts)-1]}
	if len(parts) == 2 {
		spec.table = parts[0]
	}
	return spec, nil
}

func validIdentifier(s string) bool {
	if s == "" || len(s) > identifierMaxLen {
		return false
	}
	for i, r := range s {
		switch {
		case r == '_' || (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z'):
		case i > 0 && r >= '0' && r <= '9':
		default:
			return false
		}
	}
	return true
}

// Allow reports whether the logical field name is part of the whitelist.
func (w *Whitelist) Allow(field string) bool {
	if w == nil {
		return false
	}
	_, ok := w.fields[field]
	return ok
}

// render resolves a logical field to a quoted SQL identifier. It returns an
// error for anything that was not explicitly declared, so external input can
// never reach the identifier position of a query.
func (w *Whitelist) render(field string) (string, error) {
	if w == nil {
		return "", fmt.Errorf("nil whitelist rejects field %q", field)
	}
	spec, ok := w.fields[field]
	if !ok {
		return "", fmt.Errorf("field %q is not in the whitelist", field)
	}
	if spec.table != "" {
		return quoteIdent(spec.table) + "." + quoteIdent(spec.column), nil
	}
	return quoteIdent(spec.column), nil
}

// quoteIdent emits a MySQL-style quoted identifier after a structural check.
// Backticks inside an (already pattern-validated) identifier are impossible,
// but the check is kept as defense in depth.
func quoteIdent(name string) string {
	if !validIdentifier(name) {
		// Unreachable through the public API; fail closed.
		panic("condbuilder: refusing to quote invalid identifier " + name)
	}
	return "`" + name + "`"
}

// Names returns the sorted logical field names, useful for error messages.
func (w *Whitelist) Names() []string {
	if w == nil {
		return nil
	}
	names := make([]string, 0, len(w.fields))
	for name := range w.fields {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
