package netsec

import "fmt"

// TriggerContext is passed to trigger callbacks. It deliberately does not
// expose session-changing or transaction-control operations.
type TriggerContext struct {
	Frame Frame
	row   Row

	engine *Engine
	st     *statement
	search *searchTrial
}

// Row returns a copy of the current trigger row.
func (c *TriggerContext) Row() Row { return CloneRow(c.row) }

// SetRow replaces the row seen by subsequent Body/Action code. For BEFORE
// triggers, the engine uses the value returned by Modify; SetRow is useful in
// Body for annotation.
func (c *TriggerContext) SetRow(row Row) {
	c.row = CloneRow(row)
}

// EffectiveSubject is the subject used for policy checks during this trigger.
func (c *TriggerContext) EffectiveSubject() string { return c.Frame.EffectiveSubject }

// InvokerSubject is the subject of the caller that fired this trigger.
func (c *TriggerContext) InvokerSubject() string { return c.Frame.InvokerSubject }

// OriginSubject is the subject that began the statement.
func (c *TriggerContext) OriginSubject() string { return c.Frame.OriginSubject }

// StatementID is the statement this trigger belongs to.
func (c *TriggerContext) StatementID() string { return c.Frame.StatementID }

// Depth is this trigger's nesting depth.
func (c *TriggerContext) Depth() int { return c.Frame.Depth }

// WritableTables returns the definer whitelist declared by this trigger.
func (c *TriggerContext) WritableTables() []string {
	return append([]string(nil), c.Frame.WritableTables...)
}

// Exec performs a nested DML in the same statement.
func (c *TriggerContext) Exec(dml DML) error {
	dml = cloneDML(dml)
	if c.search != nil {
		return c.search.record(dml)
	}
	if c.engine == nil || c.st == nil {
		return fmt.Errorf("netsec: trigger context is not attached to an engine")
	}
	return c.engine.executeNested(c.st, &c.Frame, dml)
}

// Insert is a convenience wrapper for Exec.
func (c *TriggerContext) Insert(table string, id RowID, row Row) error {
	return c.Exec(DML{Event: EventInsert, Table: table, RowID: id, Row: CloneRow(row)})
}

// Update is a convenience wrapper for Exec.
func (c *TriggerContext) Update(table string, id RowID, row Row) error {
	return c.Exec(DML{Event: EventUpdate, Table: table, RowID: id, Row: CloneRow(row)})
}

// Delete is a convenience wrapper for Exec.
func (c *TriggerContext) Delete(table string, id RowID) error {
	return c.Exec(DML{Event: EventDelete, Table: table, RowID: id})
}

// Query reads a row. In runtime mode it reads the engine's current row; in
// search mode it reads the branch's snapshot.
func (c *TriggerContext) Query(table string, id RowID) (Row, bool) {
	if c.search != nil {
		return c.search.query(table, id)
	}
	if c.engine == nil {
		return nil, false
	}
	t, ok := c.engine.Tables[table]
	if !ok {
		return nil, false
	}
	r, ok := t.Rows[id]
	if !ok {
		return nil, false
	}
	return CloneRow(r), true
}

// SetSessionSubject always fails. Triggers cannot change the statement origin
// or the session identity.
func (c *TriggerContext) SetSessionSubject(string) error {
	return ErrTriggerCannotChangeSession
}

// Commit always fails. Trigger bodies run inside the caller's statement and
// have no transaction-control authority.
func (c *TriggerContext) Commit() error {
	return ErrTriggerCannotCommit
}

// Rollback always fails for the same reason as Commit.
func (c *TriggerContext) Rollback() error {
	return ErrTriggerCannotCommit
}
