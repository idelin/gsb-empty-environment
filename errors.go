package netsec

import (
	"errors"
	"fmt"
	"sort"
)

var (
	// ErrNeedsFetch means the host must fetch the row (or the listed columns)
	// before this event can be evaluated. The errored event did not modify
	// transaction or committed state, so it can be retried with a complete tuple.
	ErrNeedsFetch = errors.New("netsec: row data needs fetch")

	// ErrUnknownTransaction means a row event referred to a transaction that
	// has not been started, or Commit/Abort referred to a missing transaction.
	ErrUnknownTransaction = errors.New("netsec: unknown transaction")

	// ErrTransactionExists means Begin was called twice for one transaction.
	ErrTransactionExists = errors.New("netsec: transaction already exists")

	// ErrUnknownRelation means no RelationChange has installed the relation.
	ErrUnknownRelation = errors.New("netsec: unknown relation")

	// ErrInvalidRelation means the relation metadata is malformed.
	ErrInvalidRelation = errors.New("netsec: invalid relation")

	// ErrInvalidConfig means the filter configuration is unusable.
	ErrInvalidConfig = errors.New("netsec: invalid configuration")

	// ErrInvalidTuple means a tuple violates the relation definition or event
	// contract (for example an UNCHANGED marker on a disallowed column or a
	// missing primary-key value).
	ErrInvalidTuple = errors.New("netsec: invalid tuple")

	// ErrRelationVersionReuse means the same relation version was presented
	// with a different definition.
	ErrRelationVersionReuse = errors.New("netsec: relation version reused")

	// ErrColumnDeleted means automatic migration was refused because a column
	// ID disappeared from the new relation version.
	ErrColumnDeleted = errors.New("netsec: column ID deleted")

	// ErrColumnReused means automatic migration was refused because a column
	// ID was reused with different type metadata or after deletion.
	ErrColumnReused = errors.New("netsec: column ID reused")

	// ErrCannotMigrate means automatic cache migration could not construct a
	// new-version primary key for an existing cached row.
	ErrCannotMigrate = errors.New("netsec: cannot migrate relation cache")
)

// NeedsFetchError describes the data required to evaluate a stream event.
// Key contains only primary-key columns. Missing contains the column IDs that
// could not be reconstructed from the exact relation version/key cache.
type NeedsFetchError struct {
	RelationID RelationID
	Version    RelationVersion
	Key        Key
	Missing    []ColumnID
	Reason     string
}

func (e *NeedsFetchError) Error() string {
	missing := append([]ColumnID(nil), e.Missing...)
	sort.Slice(missing, func(i, j int) bool { return missing[i] < missing[j] })
	reason := e.Reason
	if reason == "" {
		reason = "cache miss"
	}
	return fmt.Sprintf("%v (relation=%d version=%d key=%s missing=%v reason=%s)",
		ErrNeedsFetch, e.RelationID, e.Version, keyString(e.Key), missing, reason)
}

func (e *NeedsFetchError) Unwrap() error { return ErrNeedsFetch }

// ColumnError describes a refused column migration.
type ColumnError struct {
	ColumnID ColumnID
	Kind     error
	Reason   string
}

func (e *ColumnError) Error() string {
	kind := e.Kind
	if kind == nil {
		kind = ErrColumnReused
	}
	return fmt.Sprintf("%v: column %d: %s", kind, e.ColumnID, e.Reason)
}

func (e *ColumnError) Unwrap() error {
	if e.Kind == nil {
		return ErrColumnReused
	}
	return e.Kind
}
