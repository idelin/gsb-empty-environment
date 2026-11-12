package taskpkg

import (
	"errors"
	"fmt"
	"strings"
)

// Rule decides how the sibling field arrays of one level are combined.
type Rule int

const (
	// Zip pairs elements of the level's fields by position. Shorter arrays
	// are padded with a missing marker so every tuple has one ref per field.
	Zip Rule = iota
	// Cross emits the cartesian product of the level's fields.
	Cross
)

// Level describes one expansion level: the array fields to read from the
// current element and the rule used to combine them.
type Level struct {
	Name   string
	Fields []string
	Rule   Rule
}

// Spec is the expansion plan supplied by the caller.
type Spec struct {
	// Levels are applied from the document root downwards.
	Levels []Level
	// IDField names the object key holding the stable element ID.
	IDField string
	// MaxRows is the publish budget; 0 means unlimited. When a full
	// expansion would exceed it, Expand fails and nothing is published.
	MaxRows int
}

// ErrBudgetExceeded is returned when an expansion exceeds Spec.MaxRows.
var ErrBudgetExceeded = errors.New("taskpkg: row budget exceeded, nothing published")

// RefKind classifies one element reference inside a row path.
type RefKind int

const (
	// RefElement is a real array element.
	RefElement RefKind = iota
	// RefUnknown is the single unknown element produced by a null (or
	// absent) field value.
	RefUnknown
	// RefMissing is the padding marker produced by Zip for shorter arrays.
	RefMissing
)

// Ref points at one element along a row's source path.
type Ref struct {
	Field string
	// Index is the array index for RefElement, the tuple position for
	// RefMissing, and -1 for RefUnknown.
	Index int
	// ID is the stable element ID, empty when the element has none.
	ID    string
	Kind  RefKind
	Value any
}

// Row is one expanded output line with its full element source path.
type Row struct {
	Refs []Ref
	// Key identifies the row across expansions; it is built from field
	// names and stable element IDs, never from values.
	Key string
	// Hash fingerprints the values along the path so corrections to an
	// element with an unchanged ID still surface as retract+add.
	Hash string
	// Path is the human readable source path, e.g. items#i1/tiers#t2.
	Path string
}

func (s Spec) validate() error {
	if len(s.Levels) == 0 {
		return errors.New("taskpkg: spec has no levels")
	}
	for i, lvl := range s.Levels {
		if len(lvl.Fields) == 0 {
			return fmt.Errorf("taskpkg: level %d (%q) has no fields", i, lvl.Name)
		}
	}
	return nil
}

// Expand fully re-expands doc according to spec. Rows form a set keyed by
// Row.Key, so identical source paths collapse into one row. When the
// result would exceed Spec.MaxRows it returns ErrBudgetExceeded and no
// rows.
func Expand(doc any, spec Spec) ([]Row, error) {
	if err := spec.validate(); err != nil {
		return nil, err
	}
	chains := expandCtx(doc, 0, spec)
	rows := make([]Row, 0, len(chains))
	seen := make(map[string]struct{}, len(chains))
	for _, c := range chains {
		r := newRow(c)
		if _, dup := seen[r.Key]; dup {
			continue
		}
		seen[r.Key] = struct{}{}
		rows = append(rows, r)
	}
	if spec.MaxRows > 0 && len(rows) > spec.MaxRows {
		return nil, ErrBudgetExceeded
	}
	return rows, nil
}

// expandCtx expands level lvl of spec relative to obj and returns one ref
// chain per output row, covering levels lvl..len(levels)-1.
func expandCtx(obj any, lvl int, spec Spec) [][]Ref {
	level := spec.Levels[lvl]
	perField := make([][]Ref, len(level.Fields))
	for i, f := range level.Fields {
		v, present := resolvePath(obj, f)
		perField[i] = elementsOf(v, present, f, spec.IDField)
	}
	tuples := combine(level.Rule, level.Fields, perField)
	last := lvl == len(spec.Levels)-1
	var out [][]Ref
	for _, t := range tuples {
		if last {
			out = append(out, t)
			continue
		}
		// Deeper levels are expanded inside every element of the tuple;
		// unknown and missing elements recurse into nil so their deeper
		// fields resolve as unknown as well.
		for _, r := range t {
			var sub [][]Ref
			if r.Kind == RefElement {
				sub = expandCtx(r.Value, lvl+1, spec)
			} else {
				sub = expandCtx(nil, lvl+1, spec)
			}
			for _, s := range sub {
				chain := make([]Ref, 0, len(t)+len(s))
				chain = append(chain, t...)
				chain = append(chain, s...)
				out = append(out, chain)
			}
		}
	}
	return out
}

// resolvePath walks a dot separated field path through nested maps.
func resolvePath(obj any, path string) (any, bool) {
	cur := obj
	for _, seg := range strings.Split(path, ".") {
		m, ok := cur.(map[string]any)
		if !ok {
			return nil, false
		}
		cur, ok = m[seg]
		if !ok {
			return nil, false
		}
	}
	return cur, true
}

// elementsOf normalises a field value into element refs: null or absent
// becomes one unknown element, an empty array becomes the empty set, and
// any other non-array value is treated as a single element.
func elementsOf(v any, present bool, field, idField string) []Ref {
	if !present || v == nil {
		return []Ref{{Field: field, Index: -1, Kind: RefUnknown}}
	}
	arr, ok := v.([]any)
	if !ok {
		return []Ref{{Field: field, Index: 0, Kind: RefElement, ID: elementID(v, idField), Value: v}}
	}
	refs := make([]Ref, 0, len(arr))
	for i, e := range arr {
		refs = append(refs, Ref{Field: field, Index: i, Kind: RefElement, ID: elementID(e, idField), Value: e})
	}
	return refs
}

func elementID(v any, idField string) string {
	if idField == "" {
		return ""
	}
	if m, ok := v.(map[string]any); ok {
		if id, ok := m[idField]; ok && id != nil {
			return fmt.Sprint(id)
		}
	}
	return ""
}

// combine merges the per-field element lists of one level into tuples.
func combine(rule Rule, fields []string, perField [][]Ref) [][]Ref {
	switch rule {
	case Zip:
		n := 0
		for _, refs := range perField {
			if len(refs) > n {
				n = len(refs)
			}
		}
		tuples := make([][]Ref, 0, n)
		for i := 0; i < n; i++ {
			tuple := make([]Ref, len(fields))
			for j, refs := range perField {
				if i < len(refs) {
					tuple[j] = refs[i]
				} else {
					tuple[j] = Ref{Field: fields[j], Index: i, Kind: RefMissing}
				}
			}
			tuples = append(tuples, tuple)
		}
		return tuples
	default: // Cross
		tuples := [][]Ref{{}}
		for _, refs := range perField {
			next := make([][]Ref, 0, len(tuples)*len(refs))
			for _, t := range tuples {
				for _, r := range refs {
					nt := make([]Ref, 0, len(t)+1)
					nt = append(nt, t...)
					nt = append(nt, r)
					next = append(next, nt)
				}
			}
			tuples = next
		}
		return tuples
	}
}
