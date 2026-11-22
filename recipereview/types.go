// Package recipereview is a release-gate auditor for key-derivation recipes.
//
// A recipe binds a derivation purpose and version to a finite, typed field
// schema and to an encoding expression that turns a field assignment into the
// info string mixed into the KDF. Before a recipe set is released, Review runs
// three kinds of checks:
//
//   - every recipe must encode its field domain injectively (unique decoding),
//   - recipes that may coexist under the same root key and salt (different
//     purposes, or versions joined by the compatibility table) must never map
//     different parameters to the same info string,
//   - read-only legacy recipes must not be accepted as valid encodings by any
//     recipe serving a different, newer purpose.
//
// Each property is first attacked with structural symbolic checks. Anything
// the symbolic layer cannot discharge is settled, for small schemas, by full
// enumeration. When the state budget is exhausted before a property is settled
// the verdict is Unknown rather than a pass.
package recipereview

import (
	"errors"
	"fmt"
	"sort"
)

// Alphabet is the caller-supplied byte alphabet a field value is drawn from.
type Alphabet struct {
	has [256]bool
}

// NewAlphabet builds an alphabet from the distinct bytes in b.
func NewAlphabet(b []byte) (*Alphabet, error) {
	var a Alphabet
	for _, c := range b {
		a.has[c] = true
	}
	if a.Size() == 0 {
		return nil, errors.New("recipereview: alphabet must contain at least one byte")
	}
	return &a, nil
}

// Contains reports whether c is a member of the alphabet.
func (a *Alphabet) Contains(c byte) bool { return a.has[c] }

// Size returns the number of distinct bytes in the alphabet.
func (a *Alphabet) Size() int {
	n := 0
	for _, ok := range a.has {
		if ok {
			n++
		}
	}
	return n
}

// sortedBytes returns the alphabet members in ascending byte order.
func (a *Alphabet) sortedBytes() []byte {
	out := make([]byte, 0, a.Size())
	for c := 0; c < 256; c++ {
		if a.has[c] {
			out = append(out, byte(c))
		}
	}
	return out
}

// Field is a bounded string over a fixed byte alphabet.
type Field struct {
	// Name is the unique field name inside a recipe.
	Name string
	// Alphabet constrains every byte of every value.
	Alphabet *Alphabet
	// MinLen and MaxLen bound the value length in bytes; MinLen == MaxLen
	// marks a fixed-length field, which is required for raw references.
	MinLen int
	MaxLen int
}

func (f *Field) fixed() bool { return f.MinLen == f.MaxLen }

// Expr is one node of a recipe encoding expression.
type Expr interface {
	exprNode()
}

// ConstExpr emits a fixed constant byte string.
type ConstExpr struct{ Value []byte }

// RawExpr emits a fixed-length field verbatim with no framing.
type RawExpr struct{ Field string }

// PrefixExpr emits a one-byte big-endian length followed by the field bytes.
type PrefixExpr struct{ Field string }

// EscapeExpr emits the field with every special byte prefixed by Escape,
// followed by a bare Terminator that closes the (possibly empty) value.
type EscapeExpr struct {
	Field              string
	Escape, Terminator byte
}

// BranchExpr picks IfEmpty when the referenced field is the empty string and
// IfNonEmpty otherwise. The two arms must have disjoint encodings.
type BranchExpr struct {
	Field               string
	IfEmpty, IfNonEmpty Expr
}

// SeqExpr concatenates its parts in order.
type SeqExpr struct{ Parts []Expr }

func (*ConstExpr) exprNode()  {}
func (*RawExpr) exprNode()    {}
func (*PrefixExpr) exprNode() {}
func (*EscapeExpr) exprNode() {}
func (*BranchExpr) exprNode() {}
func (*SeqExpr) exprNode()    {}

// Recipe is one versioned derivation encoding for a single purpose.
type Recipe struct {
	// ID is the recipe number used as an enumeration tie-breaker.
	ID int
	// Purpose and Version identify the recipe inside a VersionRef.
	Purpose string
	Version string
	// ReadOnly marks a legacy recipe that must not be accepted by recipes of
	// other purposes.
	ReadOnly bool
	// Fields is the finite field schema keyed by field name.
	Fields map[string]*Field
	// Root is the encoding expression; every declared field must be used.
	Root Expr
}

// VersionRef identifies one recipe version of one purpose.
type VersionRef struct {
	Purpose string
	Version string
}

func (r *Recipe) ref() VersionRef {
	return VersionRef{Purpose: r.Purpose, Version: r.Version}
}

// ValidateRecipe checks the schema and expression for well-formedness.
func ValidateRecipe(r *Recipe) error {
	if r == nil {
		return errors.New("recipereview: nil recipe")
	}
	if r.Purpose == "" {
		return errors.New("recipereview: recipe purpose must be non-empty")
	}
	if r.Version == "" {
		return errors.New("recipereview: recipe version must be non-empty")
	}
	if len(r.Fields) == 0 {
		return errors.New("recipereview: recipe must declare at least one field")
	}
	names := make(map[string]bool, len(r.Fields))
	for name, f := range r.Fields {
		if name == "" {
			return errors.New("recipereview: field name must be non-empty")
		}
		if names[name] {
			return fmt.Errorf("recipereview: duplicate field %q", name)
		}
		names[name] = true
		if f == nil || f.Alphabet == nil {
			return fmt.Errorf("recipereview: field %q has no alphabet", name)
		}
		if f.Name != name {
			return fmt.Errorf("recipereview: field %q stored under mismatched name %q", f.Name, name)
		}
		if f.MinLen < 0 || f.MaxLen < f.MinLen {
			return fmt.Errorf("recipereview: field %q has invalid length bounds", name)
		}
		if f.MaxLen > 255 {
			return fmt.Errorf("recipereview: field %q exceeds 255 byte length-prefix limit", name)
		}
	}
	referenced := map[string]int{}
	if err := validateExpr(r, r.Root, referenced, map[string]bool{}); err != nil {
		return err
	}
	for name := range r.Fields {
		if referenced[name] == 0 {
			return fmt.Errorf("recipereview: field %q is never referenced by Root", name)
		}
	}
	return nil
}

func validateExpr(r *Recipe, e Expr, referenced map[string]int, knownNonEmpty map[string]bool) error {
	switch n := e.(type) {
	case *ConstExpr:
		if n == nil {
			return errors.New("recipereview: nil const expression")
		}
	case *RawExpr:
		f, err := requireField(r, n.Field)
		if err != nil {
			return err
		}
		if !f.fixed() {
			return fmt.Errorf("recipereview: raw reference to variable-length field %q", n.Field)
		}
		referenced[n.Field]++
	case *PrefixExpr:
		if _, err := requireField(r, n.Field); err != nil {
			return err
		}
		referenced[n.Field]++
	case *EscapeExpr:
		f, err := requireField(r, n.Field)
		if err != nil {
			return err
		}
		if n.Escape == n.Terminator {
			return fmt.Errorf("recipereview: escape expression on %q uses equal escape and terminator", n.Field)
		}
		if !f.Alphabet.Contains(n.Escape) || !f.Alphabet.Contains(n.Terminator) {
			return fmt.Errorf("recipereview: escape expression on %q uses bytes outside its alphabet", n.Field)
		}
		referenced[n.Field]++
	case *BranchExpr:
		if _, err := requireField(r, n.Field); err != nil {
			return err
		}
		if n.IfEmpty == nil || n.IfNonEmpty == nil {
			return fmt.Errorf("recipereview: branch on %q has a nil arm", n.Field)
		}
		if knownNonEmpty[n.Field] {
			return fmt.Errorf("recipereview: branch on %q has an unreachable empty arm", n.Field)
		}
		referenced[n.Field]++
		if err := validateExpr(r, n.IfEmpty, referenced, knownNonEmpty); err != nil {
			return err
		}
		childNonEmpty := cloneWith(knownNonEmpty, n.Field)
		if err := validateExpr(r, n.IfNonEmpty, referenced, childNonEmpty); err != nil {
			return err
		}
	case *SeqExpr:
		if len(n.Parts) == 0 {
			return errors.New("recipereview: empty sequence expression")
		}
		for _, part := range n.Parts {
			if part == nil {
				return errors.New("recipereview: sequence contains a nil part")
			}
			if err := validateExpr(r, part, referenced, knownNonEmpty); err != nil {
				return err
			}
		}
	default:
		return fmt.Errorf("recipereview: unknown expression type %T", e)
	}
	return nil
}

func requireField(r *Recipe, name string) (*Field, error) {
	f, ok := r.Fields[name]
	if !ok {
		return nil, fmt.Errorf("recipereview: expression references unknown field %q", name)
	}
	return f, nil
}

func cloneWith(in map[string]bool, key string) map[string]bool {
	out := make(map[string]bool, len(in)+1)
	for k := range in {
		out[k] = true
	}
	out[key] = true
	return out
}

// sortedFieldNames returns schema field names in deterministic order.
func (r *Recipe) sortedFieldNames() []string {
	names := make([]string, 0, len(r.Fields))
	for name := range r.Fields {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// CompatTable lists version pairs of one purpose that may coexist, i.e. may
// derive keys under the same root key and salt at the same time.
type CompatTable struct {
	entries map[compatKey]bool
}

type compatKey struct {
	purpose string
	v1, v2  string
}

// NewCompatTable builds a table from unordered coexistence pairs.
func NewCompatTable(pairs ...[2]VersionRef) *CompatTable {
	t := &CompatTable{entries: map[compatKey]bool{}}
	for _, p := range pairs {
		t.entries[normalizePair(p[0], p[1])] = true
	}
	return t
}

func normalizePair(a, b VersionRef) compatKey {
	ka := a.Purpose + "\x00" + a.Version
	kb := b.Purpose + "\x00" + b.Version
	if ka <= kb {
		return compatKey{a.Purpose, a.Version, b.Version}
	}
	return compatKey{b.Purpose, b.Version, a.Version}
}

// Allows reports whether the two recipe versions may coexist and therefore
// must have disjoint info encodings. A version is always compatible with
// itself.
func (t *CompatTable) Allows(a, b VersionRef) bool {
	if a == b {
		return true
	}
	return t.entries[normalizePair(a, b)]
}
