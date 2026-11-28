package federate

import (
	"fmt"
	"strings"
)

// Column describes one table column.
type Column struct {
	Name      string
	Type      Type
	Nullable  bool
	Collation string
}

// Schema is an ordered list of columns.
type Schema struct {
	Columns []Column
}

func (s *Schema) index(name string) int {
	for i := range s.Columns {
		if s.Columns[i].Name == name {
			return i
		}
	}
	return -1
}

// Column returns a column definition by name.
func (s *Schema) Column(name string) (Column, bool) {
	i := s.index(name)
	if i < 0 {
		return Column{}, false
	}
	return s.Columns[i], true
}

func (s *Schema) validate() error {
	if s == nil || len(s.Columns) == 0 {
		return fmt.Errorf("schema must contain at least one column")
	}
	seen := make(map[string]struct{}, len(s.Columns))
	for _, c := range s.Columns {
		if c.Name == "" {
			return fmt.Errorf("column name must not be empty")
		}
		if _, ok := seen[c.Name]; ok {
			return fmt.Errorf("duplicate column %q", c.Name)
		}
		seen[c.Name] = struct{}{}
	}
	return nil
}

// boundColumn is a column together with its table qualifier.
type boundColumn struct {
	Table  string
	Column Column
}

// Scope resolves (table, column) pairs for an evaluated row.
type Scope struct {
	cols        []boundColumn
	qualified   map[string]int
	unqualified map[string]int
	ambiguous   map[string]bool
}

func newScope(schemas ...tableSchema) *Scope {
	s := &Scope{
		qualified:   make(map[string]int),
		unqualified: make(map[string]int),
		ambiguous:   make(map[string]bool),
	}
	for _, ts := range schemas {
		for _, c := range ts.schema.Columns {
			idx := len(s.cols)
			s.cols = append(s.cols, boundColumn{Table: ts.table, Column: c})
			s.qualified[qualifiedKey(ts.table, c.Name)] = idx
			if _, exists := s.unqualified[c.Name]; exists {
				s.ambiguous[c.Name] = true
			} else {
				s.unqualified[c.Name] = idx
			}
		}
	}
	return s
}

type tableSchema struct {
	table  string
	schema *Schema
}

func qualifiedKey(table, column string) string {
	return strings.ToLower(table) + "\x00" + strings.ToLower(column)
}

func (s *Scope) resolve(table, column string) (int, bool) {
	if s == nil {
		return -1, false
	}
	if table != "" {
		i, ok := s.qualified[qualifiedKey(table, column)]
		return i, ok
	}
	if s.ambiguous[column] {
		return -1, false
	}
	i, ok := s.unqualified[column]
	return i, ok
}

func (s *Scope) columns() []boundColumn {
	if s == nil {
		return nil
	}
	return s.cols
}

// Row is a value row together with its resolution scope. The scope is carried
// through joins so qualified column references keep working.
type Row struct {
	Values []Value
	scope  *Scope
}

// NewRow builds a row for a single table.
func NewRow(schema *Schema, values ...Value) Row {
	return Row{Values: values, scope: newScope(tableSchema{table: "", schema: schema})}
}

// RowForTable builds a row qualified by a table name.
func RowForTable(table string, schema *Schema, values ...Value) Row {
	return Row{Values: values, scope: newScope(tableSchema{table: table, schema: schema})}
}

// Value resolves a column in the row.
func (r Row) Value(table, column string) Value {
	i, ok := r.scope.resolve(table, column)
	if !ok || i < 0 || i >= len(r.Values) {
		return Null()
	}
	return r.Values[i]
}
