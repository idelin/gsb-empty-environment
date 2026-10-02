package cdcsched

import (
	"fmt"
	"sort"
)

// ForeignKey describes a child -> parent reference between two tables.
type ForeignKey struct {
	ChildTable   string
	ChildColumn  string
	ParentTable  string
	ParentColumn string
}

// Table describes a replicated table and its outgoing foreign keys.
type Table struct {
	Name       string
	PrimaryKey string
	FKs        []ForeignKey
}

// Schema holds the table metadata the scheduler uses to derive dependencies.
type Schema struct {
	tables map[string]*Table
}

func NewSchema() *Schema {
	return &Schema{tables: make(map[string]*Table)}
}

func (s *Schema) AddTable(name, primaryKey string, fks ...ForeignKey) *Schema {
	s.tables[name] = &Table{Name: name, PrimaryKey: primaryKey, FKs: fks}
	return s
}

func (s *Schema) Table(name string) (*Table, bool) {
	t, ok := s.tables[name]
	return t, ok
}

// TopoSort returns table names in parent-first order (Kahn's algorithm),
// which is the safe order for inserts. It reports an error on FK cycles.
func (s *Schema) TopoSort() ([]string, error) {
	indeg := make(map[string]int, len(s.tables))
	children := make(map[string][]string, len(s.tables))
	for name := range s.tables {
		indeg[name] = 0
	}
	for _, t := range s.tables {
		for _, fk := range t.FKs {
			if _, ok := s.tables[fk.ParentTable]; !ok {
				return nil, fmt.Errorf("table %q references unknown parent table %q", t.Name, fk.ParentTable)
			}
			if fk.ParentTable == t.Name {
				continue // self reference: irrelevant at table granularity
			}
			indeg[t.Name]++
			children[fk.ParentTable] = append(children[fk.ParentTable], t.Name)
		}
	}

	var ready []string
	for name, d := range indeg {
		if d == 0 {
			ready = append(ready, name)
		}
	}
	sort.Strings(ready) // deterministic output

	order := make([]string, 0, len(s.tables))
	for len(ready) > 0 {
		n := ready[0]
		ready = ready[1:]
		order = append(order, n)
		for _, c := range children[n] {
			indeg[c]--
			if indeg[c] == 0 {
				ready = append(ready, c)
			}
		}
	}
	if len(order) != len(s.tables) {
		return nil, fmt.Errorf("foreign key dependency cycle detected among tables")
	}
	return order, nil
}

// DeleteOrder returns table names in child-first order, the safe order for deletes.
func (s *Schema) DeleteOrder() ([]string, error) {
	order, err := s.TopoSort()
	if err != nil {
		return nil, err
	}
	for i, j := 0, len(order)-1; i < j; i, j = i+1, j-1 {
		order[i], order[j] = order[j], order[i]
	}
	return order, nil
}
