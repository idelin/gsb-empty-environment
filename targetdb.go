package cdcsched

import (
	"fmt"
	"sync"
)

// TargetDB is an in-memory stand-in for a downstream database. It enforces
// the schema's foreign key constraints exactly like a real database would,
// so any ordering mistake by the scheduler surfaces as an error here.
type TargetDB struct {
	schema *Schema

	mu     sync.Mutex
	tables map[string]map[string]map[string]string // table -> pk -> row
	log    []string                                // applied operations, in apply order
}

func NewTargetDB(schema *Schema) *TargetDB {
	return &TargetDB{schema: schema, tables: make(map[string]map[string]map[string]string)}
}

// Apply executes one event against the database, enforcing FK constraints.
func (db *TargetDB) Apply(ev Event) error {
	db.mu.Lock()
	defer db.mu.Unlock()

	tbl, ok := db.schema.Table(ev.Table)
	if !ok {
		return fmt.Errorf("unknown table %q", ev.Table)
	}
	store := db.tables[ev.Table]
	if store == nil {
		store = make(map[string]map[string]string)
		db.tables[ev.Table] = store
	}

	switch ev.Op {
	case OpInsert, OpUpdate:
		for _, fk := range tbl.FKs {
			parentKey, ok := ev.Row[fk.ChildColumn]
			if !ok {
				continue
			}
			if _, ok := db.tables[fk.ParentTable][parentKey]; !ok {
				return fmt.Errorf("foreign key violation: %s(%s=%s) references missing %s row",
					ev.Table, fk.ChildColumn, parentKey, fk.ParentTable)
			}
		}
		row := make(map[string]string, len(ev.Row)+1)
		for k, v := range ev.Row {
			row[k] = v
		}
		row[tbl.PrimaryKey] = ev.Key
		store[ev.Key] = row
		db.log = append(db.log, fmt.Sprintf("%s %s/%s", ev.Op, ev.Table, ev.Key))
	case OpDelete:
		for _, child := range db.schema.tables {
			for _, fk := range child.FKs {
				if fk.ParentTable != ev.Table {
					continue
				}
				for key, row := range db.tables[child.Name] {
					if row[fk.ChildColumn] == ev.Key {
						return fmt.Errorf("foreign key violation: cannot delete %s/%s, still referenced by %s/%s",
							ev.Table, ev.Key, child.Name, key)
					}
				}
			}
		}
		delete(store, ev.Key)
		db.log = append(db.log, fmt.Sprintf("%s %s/%s", ev.Op, ev.Table, ev.Key))
	}
	return nil
}

// Log returns the applied operations in the order they were executed.
func (db *TargetDB) Log() []string {
	db.mu.Lock()
	defer db.mu.Unlock()
	return append([]string(nil), db.log...)
}

// RowCount returns the number of rows currently held by a table.
func (db *TargetDB) RowCount(table string) int {
	db.mu.Lock()
	defer db.mu.Unlock()
	return len(db.tables[table])
}
