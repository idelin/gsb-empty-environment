package policy

// Row is one stored record keyed by column name. nil values represent SQL
// NULL.
type Row map[string]any

// Txn is a serializable transaction over a single table. Implementations
// snapshot rows on Begin and publish them only on Commit; Rollback discards
// every mutation.
type Txn interface {
	// Columns returns the table's column names.
	Columns() []string
	// Snapshot returns a defensive copy of every row in the table.
	Snapshot() []Row
	// Update replaces the row currently stored at rowID with next.
	Update(rowID int, next Row) error
	// Delete removes the row stored at rowID.
	Delete(rowID int) error
	// Insert appends a new row.
	Insert(row Row) error
	// Commit publishes all mutations.
	Commit() error
	// Rollback discards all mutations.
	Rollback() error
}

// Store is the execution layer's table engine as seen by the enforcer.
type Store interface {
	// Begin opens a transaction against the named table.
	Begin(table string) (Txn, error)
}
