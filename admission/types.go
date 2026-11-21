// Package admission implements configuration admission control for a
// deployment system that merges platform policy, tenant configuration and
// plugin defaults into a single configuration tree.
//
// Paths are ordered arrays of string components. A component is never split
// on dots or any other separator: ["a.b", "c"] is a two-component path.
//
// The package exposes two independently implemented evaluation engines: an
// incremental tree engine (Tree.Apply) and a whole-tree interpreter (see
// interpreter.go). CrossCheckApply runs both and compares every effective
// leaf, including its full provenance closure.
package admission

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
)

// Path is an absolute configuration path expressed as ordered components.
type Path []string

// Source is the signed provenance of a piece of configuration. The same
// literal written by different principals carries different provenance and
// is never treated as interchangeable.
type Source struct {
	Principal string // signing management principal
	Signature string // opaque attestation / signature identifier
}

// LitKind identifies the scalar kind of a Literal.
type LitKind uint8

const (
	LitNil LitKind = iota
	LitBool
	LitNumber
	LitString
)

// Literal is a scalar configuration value. Numbers keep their original
// lexical representation so that "1" and "1.0" stay distinguishable.
type Literal struct {
	kind    LitKind
	boolVal bool
	numText string
	strVal  string
}

// NilLiteral returns the null literal.
func NilLiteral() Literal { return Literal{kind: LitNil} }

// BoolLiteral returns a boolean literal.
func BoolLiteral(v bool) Literal { return Literal{kind: LitBool, boolVal: v} }

// StringLiteral returns a string literal.
func StringLiteral(v string) Literal {
	return Literal{kind: LitString, strVal: v}
}

// NumberLiteral parses a JSON number text. Non-numeric text is rejected.
func NumberLiteral(text string) (Literal, error) {
	var probe float64
	if err := json.Unmarshal([]byte(text), &probe); err != nil {
		return Literal{}, fmt.Errorf("admission: %q is not a JSON number: %w", text, err)
	}
	return Literal{kind: LitNumber, numText: text}, nil
}

// Kind returns the literal kind.
func (l Literal) Kind() LitKind { return l.kind }

// Bool returns the boolean value.
func (l Literal) Bool() bool { return l.boolVal }

// NumberText returns the canonical number text.
func (l Literal) NumberText() string { return l.numText }

// String returns the string value.
func (l Literal) String() string { return l.strVal }

// Equal reports whether two literals are the same typed value.
func (l Literal) Equal(other Literal) bool {
	if l.kind != other.kind {
		return false
	}
	switch l.kind {
	case LitBool:
		return l.boolVal == other.boolVal
	case LitNumber:
		return l.numText == other.numText
	case LitString:
		return l.strVal == other.strVal
	default:
		return true
	}
}

// OpKind selects the operation semantics.
type OpKind uint8

const (
	// OpSet writes a scalar Literal at Path.
	OpSet OpKind = iota
	// OpLink turns Path into a reference to the absolute Target path.
	OpLink
	// OpReplace replaces the whole table subtree at Path with Table.
	OpReplace
	// OpDelete removes Path, whether it is a leaf or a subtree.
	OpDelete
)

// Op is one signed admission operation.
type Op struct {
	Kind      OpKind
	Path      Path     // absolute target path, must be non-empty
	Principal string   // acting management principal
	Source    Source   // signed origin for OpSet / OpLink
	Literal   Literal  // value for OpSet
	Target    Path     // absolute referenced path for OpLink
	Table     *Subtree // replacement fragment for OpReplace
}

// ChainHop records one reference traversal: the stored reference path and
// the source that signed that reference.
type ChainHop struct {
	At     Path
	Source Source
}

// EffectiveLeaf is one leaf as visible in the fully resolved tree.
type EffectiveLeaf struct {
	// Path is the vantage path at which the leaf is observed.
	Path Path
	// Value is the resolved scalar value.
	Value Literal
	// Terminal is the source that signed the literal at the chain end.
	Terminal Source
	// Chain lists the references traversed, outermost first. Empty for a
	// literal stored directly at Path.
	Chain []ChainHop
	// Sources is the trusted-source closure: every source along Chain plus
	// Terminal, in traversal order with duplicates removed.
	Sources []Source
}

// ErrorKind classifies an admission failure.
type ErrorKind string

const (
	ErrInvalidInput        ErrorKind = "invalid input"
	ErrLimitExceeded       ErrorKind = "input limit exceeded"
	ErrDenied              ErrorKind = "permission denied"
	ErrUntrusted           ErrorKind = "untrusted provenance"
	ErrDangling            ErrorKind = "dangling reference"
	ErrCycle               ErrorKind = "reference cycle"
	ErrMergeConflict       ErrorKind = "batch merge conflict"
	ErrInterpreterMismatch ErrorKind = "interpreter mismatch"
)

// Error is a structured admission error.
type Error struct {
	Kind    ErrorKind
	OpIndex int // -1 when the failure is not tied to one operation
	Path    Path
	Message string
}

func (e *Error) Error() string {
	if e.OpIndex >= 0 {
		return fmt.Sprintf("admission: %s at op %d path %v: %s",
			e.Kind, e.OpIndex, []string(e.Path), e.Message)
	}
	if len(e.Path) > 0 {
		return fmt.Sprintf("admission: %s at path %v: %s", e.Kind, []string(e.Path), e.Message)
	}
	return fmt.Sprintf("admission: %s: %s", e.Kind, e.Message)
}

func newError(kind ErrorKind, opIndex int, path Path, format string, args ...any) *Error {
	return &Error{
		Kind:    kind,
		OpIndex: opIndex,
		Path:    clonePath(path),
		Message: fmt.Sprintf(format, args...),
	}
}

// withOpIndex annotates an admission error with its operation index.
func withOpIndex(err error, opIndex int) error {
	if admErr, ok := err.(*Error); ok {
		cp := *admErr
		cp.OpIndex = opIndex
		return &cp
	}
	return err
}

// clonePath returns an independent copy of p.
func clonePath(p Path) Path {
	if p == nil {
		return nil
	}
	out := make(Path, len(p))
	copy(out, p)
	return out
}

// pathEqual reports whether two paths have identical components.
func pathEqual(a, b Path) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// pathPrefix reports whether prefix is an ancestor of, or equal to, p.
func pathPrefix(prefix, p Path) bool {
	if len(prefix) > len(p) {
		return false
	}
	for i := range prefix {
		if prefix[i] != p[i] {
			return false
		}
	}
	return true
}

// joinPath returns prefix joined with suffix.
func joinPath(prefix, suffix Path) Path {
	out := make(Path, 0, len(prefix)+len(suffix))
	out = append(out, prefix...)
	out = append(out, suffix...)
	return out
}

// comparePath defines a deterministic lexicographic order over paths.
func comparePath(a, b Path) int {
	n := len(a)
	if len(b) < n {
		n = len(b)
	}
	for i := 0; i < n; i++ {
		if a[i] < b[i] {
			return -1
		}
		if a[i] > b[i] {
			return 1
		}
	}
	switch {
	case len(a) < len(b):
		return -1
	case len(a) > len(b):
		return 1
	default:
		return 0
	}
}

// encodePath produces a collision-free binary key for a path. Components
// may contain any bytes, including dots and NULs.
func encodePath(p Path) string {
	size := 0
	for _, comp := range p {
		size += 4 + len(comp)
	}
	buf := make([]byte, 0, size)
	lenBytes := make([]byte, 4)
	for _, comp := range p {
		binary.BigEndian.PutUint32(lenBytes, uint32(len(comp)))
		buf = append(buf, lenBytes...)
		buf = append(buf, comp...)
	}
	return string(buf)
}

// closureSources builds the de-duplicated source closure for a chain.
func closureSources(hops []ChainHop, terminal Source) []Source {
	out := make([]Source, 0, len(hops)+1)
	seen := map[Source]bool{}
	for _, hop := range hops {
		if !seen[hop.Source] {
			seen[hop.Source] = true
			out = append(out, hop.Source)
		}
	}
	if !seen[terminal] {
		out = append(out, terminal)
	}
	return out
}

// leavesEqual compares two materialized leaf sets including full provenance.
func leavesEqual(a, b []EffectiveLeaf) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if !leafEqual(a[i], b[i]) {
			return false
		}
	}
	return true
}

func leafEqual(a, b EffectiveLeaf) bool {
	if !pathEqual(a.Path, b.Path) || !a.Value.Equal(b.Value) || a.Terminal != b.Terminal {
		return false
	}
	if len(a.Chain) != len(b.Chain) {
		return false
	}
	for i := range a.Chain {
		if !pathEqual(a.Chain[i].At, b.Chain[i].At) || a.Chain[i].Source != b.Chain[i].Source {
			return false
		}
	}
	if len(a.Sources) != len(b.Sources) {
		return false
	}
	for i := range a.Sources {
		if a.Sources[i] != b.Sources[i] {
			return false
		}
	}
	return true
}
