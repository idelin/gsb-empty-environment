// Package joinselim removes redundant equi-joins while preserving SQL
// multiset (bag) or DISTINCT semantics.
package joinselim

import "fmt"

// Ref is a qualified column reference: Alias.Column.
type Ref struct {
	Alias  string `json:"alias"`
	Column string `json:"column"`
}

// Col is a concise constructor for Ref.
func Col(alias, column string) Ref { return Ref{Alias: alias, Column: column} }

// String renders alias.column.
func (r Ref) String() string { return r.Alias + "." + r.Column }

// Table describes a base table occurrence in a query.
type TableRef struct {
	Alias string `json:"alias"`
	Table string `json:"table"`
}

// Equality is one scalar equi-join predicate.
type Equality struct {
	Left  Ref `json:"left"`
	Right Ref `json:"right"`
}

// JoinType enumerates supported join operators.
type JoinType string

const (
	// InnerJoin is SQL INNER JOIN.
	InnerJoin JoinType = "INNER"
	// LeftJoin is SQL LEFT OUTER JOIN.
	LeftJoin JoinType = "LEFT"
)

// PredicateOp enumerates supported single-column or column-comparison predicates.
type PredicateOp string

const (
	OpNotNull  PredicateOp = "IS NOT NULL"
	OpIsNull   PredicateOp = "IS NULL"
	OpEqual    PredicateOp = "="
	OpNotEqual PredicateOp = "<>"
	OpLT       PredicateOp = "<"
	OpLE       PredicateOp = "<="
	OpGT       PredicateOp = ">"
	OpGE       PredicateOp = ">="
)

// Predicate is a simple conjunctive predicate.
//
// For OpNotNull and OpIsNull only Col is used. For column-column comparisons,
// set Other. Otherwise Value is compared with Col.
type Predicate struct {
	Op    PredicateOp `json:"op"`
	Col   Ref         `json:"col"`
	Other *Ref        `json:"other,omitempty"`
	Value any         `json:"value,omitempty"`
}

// NotNull constructs col IS NOT NULL.
func NotNull(col Ref) Predicate { return Predicate{Op: OpNotNull, Col: col} }

// IsNull constructs col IS NULL.
func IsNull(col Ref) Predicate { return Predicate{Op: OpIsNull, Col: col} }

// EqualValue constructs col = value.
func EqualValue(col Ref, value any) Predicate {
	return Predicate{Op: OpEqual, Col: col, Value: value}
}

// CompareValue constructs col op value.
func CompareValue(op PredicateOp, col Ref, value any) Predicate {
	return Predicate{Op: op, Col: col, Value: value}
}

// EqualColumns constructs left = right.
func EqualColumns(left, right Ref) Predicate {
	return Predicate{Op: OpEqual, Col: left, Other: &right}
}

// Join describes one table added after Query.From in left-to-right join order.
type Join struct {
	Type         JoinType    `json:"type"`
	Alias        string      `json:"alias"`
	Table        string      `json:"table"`
	On           []Equality  `json:"on,omitempty"`
	OnPredicates []Predicate `json:"on_predicates,omitempty"`
}

// Query is the restricted logical query shape understood by the rewriter.
//
// Joins are evaluated in slice order as a left-deep sequence. Each equality may
// reference any alias introduced before or by that join.
type Query struct {
	From       TableRef    `json:"from"`
	Joins      []Join      `json:"joins,omitempty"`
	Distinct   bool        `json:"distinct,omitempty"`
	Projection []Ref       `json:"projection"`
	Where      []Predicate `json:"where,omitempty"`
}

// Column describes one schema column.
type Column struct {
	Name     string `json:"name"`
	Nullable bool   `json:"nullable"`
}

// Table is a schema table definition.
type Table struct {
	Name       string     `json:"name"`
	Columns    []Column   `json:"columns"`
	PrimaryKey []string   `json:"primary_key,omitempty"`
	UniqueKeys [][]string `json:"unique_keys,omitempty"`
}

// ForeignKey is a declared referential constraint.
//
// Table/Columns are the child (FK-bearing) side. ReferencedColumns may be empty
// to mean the referenced table's primary key.
type ForeignKey struct {
	Name              string   `json:"name,omitempty"`
	Table             string   `json:"table"`
	Columns           []string `json:"columns"`
	ReferencedTable   string   `json:"referenced_table"`
	ReferencedColumns []string `json:"referenced_columns,omitempty"`
}

// FunctionalDependency is a schema-level determinant relationship.
type FunctionalDependency struct {
	Table string   `json:"table"`
	LHS   []string `json:"lhs"`
	RHS   []string `json:"rhs"`
}

// Schema contains all constraints used by join elimination.
type Schema struct {
	Tables                 map[string]Table       `json:"tables"`
	ForeignKeys            []ForeignKey           `json:"foreign_keys,omitempty"`
	FunctionalDependencies []FunctionalDependency `json:"functional_dependencies,omitempty"`
}

// NewSchema returns an initialized schema.
func NewSchema() *Schema {
	return &Schema{Tables: map[string]Table{}}
}

// AddTable adds or replaces a table and returns the schema for chaining.
func (s *Schema) AddTable(table Table) *Schema {
	if s.Tables == nil {
		s.Tables = map[string]Table{}
	}
	s.Tables[table.Name] = table
	return s
}

// AddForeignKey adds a foreign key and returns the schema for chaining.
func (s *Schema) AddForeignKey(fk ForeignKey) *Schema {
	s.ForeignKeys = append(s.ForeignKeys, fk)
	return s
}

// AddFD adds a functional dependency and returns the schema for chaining.
func (s *Schema) AddFD(fd FunctionalDependency) *Schema {
	s.FunctionalDependencies = append(s.FunctionalDependencies, fd)
	return s
}

// Column looks up a physical column.
func (s *Schema) Column(table, name string) (Column, bool) {
	if s == nil {
		return Column{}, false
	}
	t, ok := s.Tables[table]
	if !ok {
		return Column{}, false
	}
	for _, c := range t.Columns {
		if c.Name == name {
			return c, true
		}
	}
	return Column{}, false
}

// FDClosure returns the deterministic attribute closure of seed under the
// table's declared functional dependencies.
func (s *Schema) FDClosure(table string, seed []string) []string {
	closure := map[string]bool{}
	for _, col := range seed {
		closure[col] = true
	}
	if s == nil {
		return seed
	}
	changed := true
	for changed {
		changed = false
		for _, fd := range s.FunctionalDependencies {
			if fd.Table != table {
				continue
			}
			lhsCovered := true
			for _, col := range fd.LHS {
				if !closure[col] {
					lhsCovered = false
					break
				}
			}
			if !lhsCovered {
				continue
			}
			for _, rhs := range fd.RHS {
				if !closure[rhs] {
					closure[rhs] = true
					changed = true
				}
			}
		}
	}
	out := make([]string, 0, len(closure))
	for col := range closure {
		out = append(out, col)
	}
	return out
}

func (s *Schema) String() string {
	if s == nil {
		return "<nil schema>"
	}
	return fmt.Sprintf("Schema(%d tables, %d FKs)", len(s.Tables), len(s.ForeignKeys))
}
