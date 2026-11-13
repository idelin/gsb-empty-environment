package netsec

import "errors"

var (
	// ErrPolicyDenied means the effective subject failed the table policy.
	ErrPolicyDenied = errors.New("netsec: policy denied")

	// ErrPolicyAfterBefore means a BEFORE trigger changed the row into one the
	// effective subject is no longer allowed to write.
	ErrPolicyAfterBefore = errors.New("netsec: policy denied changed row")

	// ErrReentrant means a trigger re-issued the original DML event on the
	// original (table, row, statement).
	ErrReentrant = errors.New("netsec: re-entrant trigger DML")

	// ErrMaxDepth is the engine-wide nesting limit.
	ErrMaxDepth = errors.New("netsec: maximum trigger depth exceeded")

	// ErrTriggerMaxDepth is a trigger-specific nesting limit.
	ErrTriggerMaxDepth = errors.New("netsec: trigger maximum depth exceeded")

	// ErrTriggerRowOnce means in one statement a trigger ID already ran for the
	// same table row.
	ErrTriggerRowOnce = errors.New("netsec: trigger already ran for row in statement")

	// ErrDefinerTable means a definer trigger tried to write outside its
	// declared WritableTables set under the strict model.
	ErrDefinerTable = errors.New("netsec: definer write outside declared tables")

	// ErrDefinerMissing means a definer trigger has no Definer subject.
	ErrDefinerMissing = errors.New("netsec: definer trigger has no definer subject")

	// ErrDuplicateTrigger means trigger IDs are not unique across the engine.
	ErrDuplicateTrigger = errors.New("netsec: duplicate trigger ID")

	// ErrNoTable means a DML referenced an unregistered table.
	ErrNoTable = errors.New("netsec: unknown table")

	// ErrRowExists / ErrRowNotFound describe base-table shape errors.
	ErrRowExists   = errors.New("netsec: row already exists")
	ErrRowNotFound = errors.New("netsec: row not found")

	// ErrUnknownEvent means the DML event is not Insert/Update/Delete.
	ErrUnknownEvent = errors.New("netsec: unknown event")

	// ErrTriggerCannotChangeSession is returned by the forbidden context
	// operation.
	ErrTriggerCannotChangeSession = errors.New("netsec: trigger cannot change session subject")

	// ErrTriggerCannotCommit is returned by forbidden transaction-control
	// operations.
	ErrTriggerCannotCommit = errors.New("netsec: trigger cannot commit or roll back the statement")
)
