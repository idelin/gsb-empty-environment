// Package phyprop implements bottom-up physical property (sort order)
// tracking and enforcement for a query plan tree, using only the Go
// standard library.
package phyprop

import "strings"

// OrderColumn is one column of an ordering specification.
type OrderColumn struct {
	Column string
	Desc   bool
}

func (c OrderColumn) String() string {
	if c.Desc {
		return c.Column + " DESC"
	}
	return c.Column + " ASC"
}

// Asc is a helper to build an ascending OrderColumn.
func Asc(col string) OrderColumn { return OrderColumn{Column: col} }

// Desc is a helper to build a descending OrderColumn.
func Desc(col string) OrderColumn { return OrderColumn{Column: col, Desc: true} }

// Order is a physical property: the guarantee (or requirement) that rows
// are sorted by the listed columns, in sequence.
type Order []OrderColumn

func (o Order) String() string {
	if len(o) == 0 {
		return "<any>"
	}
	parts := make([]string, len(o))
	for i, c := range o {
		parts[i] = c.String()
	}
	return strings.Join(parts, ", ")
}

// IsEmpty reports whether the order imposes no constraint.
func (o Order) IsEmpty() bool { return len(o) == 0 }

// SatisfiedBy reports whether a delivered order satisfies this required
// order. A requirement is satisfied when it is a prefix of the delivered
// order (matching column and direction), e.g. ORDER BY id is satisfied by
// an index on (id, name). An empty requirement is always satisfied.
func (required Order) SatisfiedBy(delivered Order) bool {
	if len(required) == 0 {
		return true
	}
	if len(delivered) < len(required) {
		return false
	}
	for i, rc := range required {
		dc := delivered[i]
		if rc.Column != dc.Column || rc.Desc != dc.Desc {
			return false
		}
	}
	return true
}
