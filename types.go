package netsec

// RelationID identifies a replicated relation. It is not a table name: the
// host must keep it stable for the lifetime of the replication stream.
type RelationID uint32

// RelationVersion identifies one schema definition of a relation. Reusing a
// version number with a different definition is rejected.
type RelationVersion uint64

// ColumnID is a stable logical column identifier. Column positions are
// deliberately not used anywhere in this package because positions are not
// stable across schema changes.
type ColumnID uint32

// TransactionID identifies a streamed transaction.
type TransactionID uint64

// DataType is the logical type recorded in the relation metadata. The filter
// compares it during automatic schema migration; it does not coerce row
// values.
type DataType string

// Column describes one column in a relation version.
type Column struct {
	ID   ColumnID
	Name string
	Type DataType

	// AllowUnchanged permits an Update tuple to carry Unchanged for this
	// column. An Unchanged marker on any other column is rejected.
	AllowUnchanged bool
}

// Relation is one version of a replicated relation definition.
type Relation struct {
	ID         RelationID
	Version    RelationVersion
	Columns    []Column
	PrimaryKey []ColumnID
}

// Row contains values by stable column ID. A present nil value means SQL NULL;
// an absent column means that the tuple did not carry that column. Update
// tuples can use Unchanged for columns whose relation metadata permits it.
type Row map[ColumnID]any

// Key contains only the primary-key columns of a row.
type Key map[ColumnID]any

// Record is a convenient synonym for Row for callers coming from a CDC API.
type Record = Row

// UnchangedMarker is the sentinel used in an Update tuple when the new value
// was not transmitted (for example an unchanged TOAST value).
type UnchangedMarker struct{}

// Unchanged is the value to place in a tuple for an unchanged column. The
// marker is never emitted or cached as a real value.
var Unchanged = UnchangedMarker{}

// UnchangedValue is an alias for Unchanged for callers that prefer an
// explicit value name.
var UnchangedValue = UnchangedMarker{}

// IsUnchanged reports whether value is the UNCHANGED marker.
func IsUnchanged(value any) bool {
	_, ok := value.(UnchangedMarker)
	return ok
}

// ChangeKind is the kind of a committed output change.
type ChangeKind string

const (
	InsertChange ChangeKind = "insert"
	UpdateChange ChangeKind = "update"
	DeleteChange ChangeKind = "delete"
)

// Change is the only value passed to the output callback.
//
// Insert carries Key and a complete Row.
// Update carries OldKey, Key, and a complete new Row. OldKey is a key only;
// the old row's non-key values are never exposed.
// Delete carries OldKey only. Row is nil and Key is nil.
//
// Row is always a complete relation row whose predicate evaluated to true.
// Delete never contains a row.
type Change struct {
	Kind            ChangeKind
	RelationID      RelationID
	RelationVersion RelationVersion
	OldKey          Key
	Key             Key
	Row             Row
}
