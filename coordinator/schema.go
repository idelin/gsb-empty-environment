package coordinator

import "sync"

// Column describes a single table column.
type Column struct {
	Name string
	Type string
}

// Schema is an immutable, versioned snapshot of a table's structure.
// DML events are always parsed against exactly one Schema snapshot.
type Schema struct {
	Table   string
	Version uint64
	Columns []Column
}

// Registry is the metadata cache holding the current schema per table.
// The coordinator cuts this cache at DDL barriers: DML dispatched before
// the barrier holds a snapshot of the old schema, DML dispatched after
// holds the new one.
type Registry struct {
	mu      sync.RWMutex
	schemas map[string]Schema
}

func NewRegistry() *Registry {
	return &Registry{schemas: make(map[string]Schema)}
}

// Register installs the initial schema for a table at version 1.
func (r *Registry) Register(table string, cols ...Column) Schema {
	r.mu.Lock()
	defer r.mu.Unlock()
	s := Schema{Table: table, Version: 1, Columns: append([]Column(nil), cols...)}
	r.schemas[table] = s
	return s
}

// Get returns a copy of the current schema snapshot for a table.
func (r *Registry) Get(table string) (Schema, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	s, ok := r.schemas[table]
	if !ok {
		return Schema{}, false
	}
	// Deep-copy the column slice so the snapshot is immutable even if a
	// later DDL replaces the registered schema.
	s.Columns = append([]Column(nil), s.Columns...)
	return s, true
}

// Apply executes a DDL event against the registry, bumping the schema
// version. It returns the new schema snapshot.
func (r *Registry) Apply(ddl DDLEvent) (Schema, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	s, ok := r.schemas[ddl.Table]
	if !ok {
		return Schema{}, &UnknownTableError{Table: ddl.Table}
	}
	switch ddl.Kind {
	case AddColumn:
		cols := append([]Column(nil), s.Columns...)
		cols = append(cols, ddl.Column)
		s = Schema{Table: s.Table, Version: s.Version + 1, Columns: cols}
		r.schemas[ddl.Table] = s
		return s, nil
	default:
		return Schema{}, &UnsupportedDDLError{Table: ddl.Table}
	}
}
