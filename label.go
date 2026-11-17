package netsec

import "fmt"

// Label is a hierarchical security label. Level defines the label's rank in
// the hierarchy: a larger Level is more restrictive than a smaller one. Name
// is the human-readable classification and must be non-empty for a label to be
// usable.
//
// A zero Label is missing, not "public". Callers must explicitly attach a
// label before an object can be released or read.
type Label struct {
	Name  string
	Level int
}

// Valid reports whether the label has a classification name.
func (l Label) Valid() bool {
	return l.Name != ""
}

// String implements fmt.Stringer.
func (l Label) String() string {
	if !l.Valid() {
		return "<missing>"
	}
	return fmt.Sprintf("%s(%d)", l.Name, l.Level)
}
