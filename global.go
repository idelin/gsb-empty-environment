package temporal

// defaultDB is the process-wide executor used by the package-level helper
// functions. It lets concise call sites use temporal.UpdatePortion(...)
// directly after temporal.CreateTable(...).
var defaultDB = NewExecutor()

// Default returns the process-wide Executor used by the package-level helpers.
func Default() *Executor { return defaultDB }

// ResetDefault replaces the process-wide executor with a fresh one.
func ResetDefault() { defaultDB = NewExecutor() }

// CreateTable creates a table on the default executor/schema.
func CreateTable(name string, keyColumns ...string) *Table {
	return defaultDB.CreateTable(name, keyColumns...)
}

// AddTable registers an existing table on the default schema.
func AddTable(t *Table) *Table { return defaultDB.AddTable(t) }

// AddForeignKey registers an FK coverage rule on the default schema.
func AddForeignKey(fk ForeignKeyCoverage) *Schema { return defaultDB.AddForeignKey(fk) }

// AddExclusion registers an exclusion rule on the default schema.
func AddExclusion(c ExclusionConstraint) *Schema { return defaultDB.AddExclusion(c) }

// UpdatePortion runs UpdatePortion on the default/sole table of the
// process-wide executor. For named-table routing use Default().UpdatePortion
// or Schema.UpdatePortion.
func UpdatePortion(key, targetRange, newPayload any) error {
	t, err := defaultDB.ResolveTable()
	if err != nil {
		return err
	}
	return t.UpdatePortion(key, targetRange, newPayload)
}

// Validate checks the default schema.
func Validate() error { return defaultDB.Validate() }
