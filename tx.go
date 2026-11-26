package temporal

import "fmt"

// Tx is an explicit unit of work over an isolated copy of a Schema. Changes
// only reach the originating schema on Commit; Rollback simply discards the
// working copy. Every Table.UpdatePortion inside a transaction validates
// against the in-progress transaction state.
type Tx struct {
	root *Schema
	work *Schema
	done bool
}

// Begin starts a transaction with a deep-copied working schema.
func (s *Schema) Begin() *Tx {
	work := NewSchema()
	work.DefaultTable = s.DefaultTable
	work.Exclusions = append([]ExclusionConstraint(nil), s.Exclusions...)
	work.ForeignKeys = append([]ForeignKeyCoverage(nil), s.ForeignKeys...)
	for name, t := range s.Tables {
		c := NewTable(t.Name, t.KeyColumns...)
		c.RejectEmpty = t.RejectEmpty
		c.CoalesceAdjacent = t.CoalesceAdjacent
		c.Rows = t.Snapshot()
		work.AddTable(c)
		_ = name
	}
	return &Tx{root: s, work: work}
}

// Table returns the transaction's working copy of a table.
func (tx *Tx) Table(name string) *Table { return tx.work.Table(name) }

// Schema returns the isolated working schema.
func (tx *Tx) Schema() *Schema { return tx.work }

// UpdatePortion runs a named-table portion update inside the transaction.
func (tx *Tx) UpdatePortion(tableName string, key, targetRange, newPayload any) error {
	if tx.done {
		return ErrTxDone
	}
	t := tx.work.Table(tableName)
	if t == nil {
		return fmt.Errorf("%w: %s", ErrUnknownTable, tableName)
	}
	return t.UpdatePortion(key, targetRange, newPayload)
}

// Validate checks all constraints against in-transaction state.
func (tx *Tx) Validate() error { return tx.work.Validate() }

// Commit validates once more and publishes working rows to the root schema.
func (tx *Tx) Commit() error {
	if tx.done {
		return ErrTxDone
	}
	if err := tx.work.Validate(); err != nil {
		return err
	}
	for name, wt := range tx.work.Tables {
		rt := tx.root.Tables[name]
		if rt == nil {
			continue
		}
		rt.Rows = make([]Row, len(wt.Rows))
		for i, r := range wt.Rows {
			rt.Rows[i] = r.Clone()
		}
		if wt.LastReport != nil {
			rt.LastReport = wt.LastReport
		}
	}
	tx.done = true
	return nil
}

// Rollback discards all in-transaction changes.
func (tx *Tx) Rollback() error {
	if tx.done {
		return ErrTxDone
	}
	tx.done = true
	return nil
}
