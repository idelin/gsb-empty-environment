package pgoutput

import "fmt"

// Change is one assembled row change inside a Transaction.
type Change struct {
	Op       Op
	Relation *Relation
	// Old is the full-width old row. It is nil for inserts and when replica
	// identity NOTHING sends no old row. Unchanged TOAST columns have already
	// been merged from New for updates.
	Old []TupleColumn
	// New is the full-width new row for inserts and updates; nil for deletes.
	New []TupleColumn
	// SubXID tags the producing subtransaction (zero for the top-level
	// transaction). It enables subtransaction-abort rollback.
	SubXID uint32
}

// NamespacedName returns "namespace.name" for diagnostics.
func (r *Relation) NamespacedName() string {
	return r.Namespace + "." + r.Name
}

// LookupType resolves a column's data type through the catalog cache.
func (c *Relation) LookupType(col int, types map[uint32]Type) (Type, bool) {
	if col < 0 || col >= len(c.Columns) {
		return Type{}, false
	}
	t, ok := types[c.Columns[col].DataType]
	return t, ok
}

// Catalog caches Relation ('R') and Type ('Y') messages received over the
// replication stream. Postgres re-sends these whenever a relation or type is
// first used in a session or changes, so the cache is updated in place.
type Catalog struct {
	relations map[uint32]Relation
	types     map[uint32]Type
}

// NewCatalog creates an empty relation/type cache.
func NewCatalog() *Catalog {
	return &Catalog{
		relations: make(map[uint32]Relation),
		types:     make(map[uint32]Type),
	}
}

// AddRelation inserts or replaces a cached relation.
func (c *Catalog) AddRelation(r Relation) {
	c.relations[r.ID] = r
}

// Relation returns the cached relation for a relation OID.
func (c *Catalog) Relation(id uint32) (Relation, bool) {
	r, ok := c.relations[id]
	return r, ok
}

// AddType inserts or replaces a cached data type.
func (c *Catalog) AddType(t Type) {
	c.types[t.ID] = t
}

// Type returns the cached type for a type OID.
func (c *Catalog) Type(id uint32) (Type, bool) {
	t, ok := c.types[id]
	return t, ok
}

// expandKeyTuple widens a replica-identity-only tuple (marker 'K') to the full
// column count of the relation by filling non-key positions with NULL columns.
func expandKeyTuple(rel *Relation, keyTuple []TupleColumn) []TupleColumn {
	if len(keyTuple) == len(rel.Columns) {
		return keyTuple
	}
	full := make([]TupleColumn, len(rel.Columns))
	ki := 0
	for i, col := range rel.Columns {
		if col.Key && ki < len(keyTuple) {
			full[i] = keyTuple[ki]
			ki++
		} else {
			full[i] = TupleColumn{Flag: tupleNull}
		}
	}
	return full
}

// mergeToast fills unchanged-TOAST entries of oldRow with the corresponding
// values of newRow. Postgres marks a column 'u' when its (possibly TOASTed)
// value was not modified and therefore not included in the old tuple; the
// current value must be carried over instead of being treated as NULL.
//
// The returned slice is a copy; neither input is modified.
func mergeToast(rel *Relation, oldRow, newRow []TupleColumn) ([]TupleColumn, error) {
	if len(oldRow) != len(rel.Columns) {
		return nil, fmt.Errorf(
			"pgoutput: old tuple width %d does not match relation %s column count %d",
			len(oldRow), rel.NamespacedName(), len(rel.Columns))
	}
	if len(newRow) != len(rel.Columns) {
		return nil, fmt.Errorf(
			"pgoutput: new tuple width %d does not match relation %s column count %d",
			len(newRow), rel.NamespacedName(), len(rel.Columns))
	}
	merged := make([]TupleColumn, len(oldRow))
	for i := range oldRow {
		switch {
		case oldRow[i].Unchanged && newRow[i].Unchanged:
			// Neither side carries the value; preserve the unchanged marker so
			// downstream consumers know the column is opaque, not NULL.
			merged[i] = oldRow[i]
		case oldRow[i].Unchanged:
			merged[i] = newRow[i]
		default:
			merged[i] = oldRow[i]
		}
	}
	return merged, nil
}
