package temporal

import "fmt"

// Schema binds tables to global exclusion and foreign-key coverage
// constraints. UpdatePortion on a table attached to a Schema validates the
// whole post-update row set, including every FK temporal coverage rule.
type Schema struct {
	// Tables by name.
	Tables map[string]*Table

	// DefaultTable is used by the three-argument Executor.UpdatePortion.
	// When empty and exactly one table exists, that table is used.
	DefaultTable string

	// Exclusions are cross-checks evaluated over all gathered rows.
	Exclusions []ExclusionConstraint

	// ForeignKeys are the temporal coverage rules.
	ForeignKeys []ForeignKeyCoverage
}

// NewSchema creates an empty schema.
func NewSchema() *Schema {
	return &Schema{Tables: map[string]*Table{}}
}

// AddTable registers a table and attaches it to the schema.
func (s *Schema) AddTable(t *Table) *Table {
	if s.Tables == nil {
		s.Tables = map[string]*Table{}
	}
	t.schema = s
	s.Tables[t.Name] = t
	return t
}

// CreateTable creates, registers and returns a table.
func (s *Schema) CreateTable(name string, keyColumns ...string) *Table {
	t := s.AddTable(NewTable(name, keyColumns...))
	if s.DefaultTable == "" {
		s.DefaultTable = name
	}
	return t
}

// Table returns the registered table or nil.
func (s *Schema) Table(name string) *Table {
	return s.Tables[name]
}

// MustTable returns the table or panics.
func (s *Schema) MustTable(name string) *Table {
	t := s.Tables[name]
	if t == nil {
		panic(fmt.Errorf("%w: %s", ErrUnknownTable, name))
	}
	return t
}

// AddExclusion registers an exclusion constraint.
func (s *Schema) AddExclusion(c ExclusionConstraint) *Schema {
	s.Exclusions = append(s.Exclusions, c)
	return s
}

// AddForeignKey registers a temporal coverage rule.
func (s *Schema) AddForeignKey(fk ForeignKeyCoverage) *Schema {
	s.ForeignKeys = append(s.ForeignKeys, fk)
	return s
}

// combinedRows gathers all rows, replacing one table's committed rows with a
// candidate slice for transactional validation.
func (s *Schema) combinedRows(overrideTable string, candidate []Row) []Row {
	var all []Row
	for name, t := range s.Tables {
		rows := t.Rows
		if name == overrideTable {
			rows = candidate
		}
		for _, r := range rows {
			if r.Table == "" {
				r.Table = name
			}
			all = append(all, r)
		}
	}
	return all
}

// Validate evaluates every schema-wide constraint over all committed rows.
func (s *Schema) Validate() error {
	ve := &ValidationError{}
	for name, t := range s.Tables {
		ve.Empties = append(ve.Empties, CheckEmptyRanges(name, t.Rows)...)
		ve.Exclusion = append(ve.Exclusion, CheckExclusion(t.Rows, t.effectiveExclusion())...)
	}
	all := s.combinedRows("", nil)
	for _, c := range s.Exclusions {
		ve.Exclusion = append(ve.Exclusion, CheckExclusion(all, c)...)
	}
	for _, fk := range s.ForeignKeys {
		ve.Coverage = append(ve.Coverage, CheckForeignKeyCoverage(all, fk)...)
	}
	return ve.OrNil()
}

// UpdatePortion accepts both the prompt's three-argument form
// UpdatePortion(key, targetRange, newPayload) (applied to the default/sole
// table) and the named-table form
// UpdatePortion(tableName, key, targetRange, newPayload). A *Table may be
// passed as the first argument of the four-argument form.
func (s *Schema) UpdatePortion(args ...any) error {
	switch len(args) {
	case 3:
		t, err := s.ResolveTable()
		if err != nil {
			return err
		}
		return t.UpdatePortion(args[0], args[1], args[2])
	case 4:
		switch first := args[0].(type) {
		case string:
			t, err := s.ResolveTable(first)
			if err != nil {
				return err
			}
			return t.UpdatePortion(args[1], args[2], args[3])
		case *Table:
			return first.UpdatePortion(args[1], args[2], args[3])
		case Table:
			return first.UpdatePortion(args[1], args[2], args[3])
		default:
			return fmt.Errorf("temporal: four-argument UpdatePortion expects a table name or *Table as first argument")
		}
	default:
		return fmt.Errorf("temporal: UpdatePortion expects 3 or 4 arguments, got %d", len(args))
	}
}

// ResolveTable returns the named table, the explicit default or the sole
// registered table.
func (s *Schema) ResolveTable(name ...string) (*Table, error) {
	n := ""
	if len(name) > 0 {
		n = name[0]
	}
	if n == "" {
		n = s.DefaultTable
	}
	if n != "" {
		t := s.Tables[n]
		if t == nil {
			return nil, fmt.Errorf("%w: %s", ErrUnknownTable, n)
		}
		return t, nil
	}
	if len(s.Tables) == 1 {
		for _, t := range s.Tables {
			return t, nil
		}
	}
	return nil, fmt.Errorf("temporal: no table selected for UpdatePortion")
}

// Executor is the relational-executor facing facade. It currently embeds
// Schema and adds constructor aliases expected by executor call sites.
type Executor struct {
	*Schema
}

// NewExecutor creates an executor with an empty schema.
func NewExecutor() *Executor {
	return &Executor{Schema: NewSchema()}
}

// NewStore is an alias for NewExecutor.
func NewStore() *Executor { return NewExecutor() }

// NewDB is an alias for NewExecutor.
func NewDB() *Executor { return NewExecutor() }

// NewDatabase is an alias for NewExecutor.
func NewDatabase() *Executor { return NewExecutor() }

// UpdatePortionOn runs the portion update on the named table and returns the
// detailed report.
func (e *Executor) UpdatePortionOn(tableName string, key, targetRange, newPayload any) (*PortionReport, error) {
	t, err := e.ResolveTable(tableName)
	if err != nil {
		return nil, err
	}
	return t.UpdatePortionReport(key, targetRange, newPayload)
}

// OnTable returns the named table (alias of Schema.Table).
func (e *Executor) OnTable(name string) *Table { return e.Table(name) }

// Use sets the default table and returns it.
func (e *Executor) Use(name string) *Table {
	e.DefaultTable = name
	return e.Table(name)
}

// Store is an alias of Executor.
type Store = Executor

// DB is an alias of Executor.
type DB = Executor

// Database is an alias of Executor.
type Database = Executor

// Engine is an alias of Executor.
type Engine = Executor

// NewEngine is an alias of NewExecutor.
func NewEngine() *Executor { return NewExecutor() }
