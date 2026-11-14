package netsec

import (
	"fmt"
	"strings"
)

// Tag is the abstract byte-order class of an IR value.
//
// TagRaw means the value is a byte sequence whose integer interpretation has
// not been established. TagBE and TagLE are integers decoded (or intended to be
// encoded) with that byte order. TagConst is an order-neutral constant. A join
// of incompatible classes produces TagConflict; in particular it is never
// legal to silently choose one of two conflicting orders at a merge.
type Tag uint8

const (
	TagInvalid Tag = iota
	TagRaw
	TagBE
	TagLE
	TagConst
	TagConflict
	TagUnknown
)

func (t Tag) String() string {
	switch t {
	case TagRaw:
		return "raw"
	case TagBE:
		return "be"
	case TagLE:
		return "le"
	case TagConst:
		return "const"
	case TagConflict:
		return "conflict"
	case TagUnknown:
		return "unknown"
	case TagInvalid:
		return "invalid"
	default:
		return fmt.Sprintf("tag(%d)", uint8(t))
	}
}

// IsOrder reports whether t is one of the two concrete byte orders.
func (t Tag) IsOrder() bool {
	return t == TagBE || t == TagLE
}

// Order describes the byte order used by an IR operation.
type Order uint8

const (
	OrderNone Order = iota
	OrderBig
	OrderLittle
)

func (o Order) String() string {
	switch o {
	case OrderBig:
		return "be"
	case OrderLittle:
		return "le"
	case OrderNone:
		return "none"
	default:
		return fmt.Sprintf("order(%d)", uint8(o))
	}
}

// Tag returns the Tag corresponding to o.
func (o Order) Tag() Tag {
	switch o {
	case OrderBig:
		return TagBE
	case OrderLittle:
		return TagLE
	default:
		return TagInvalid
	}
}

// Position is an optional source location carried by IR instructions and
// provenance steps.
type Position struct {
	File string
	Line int
	Col  int
}

func (p Position) String() string {
	if p.File == "" {
		return ""
	}
	if p.Line <= 0 {
		return p.File
	}
	if p.Col <= 0 {
		return fmt.Sprintf("%s:%d", p.File, p.Line)
	}
	return fmt.Sprintf("%s:%d:%d", p.File, p.Line, p.Col)
}

// TraceStep records one operation in a value's provenance chain.
type TraceStep struct {
	Inst int
	Op   Op
	Tag  Tag
	Text string
	Pos  Position
	Note string
}

// Trace is an immutable provenance node. Parents are the values consumed by
// the step. A trace can therefore explain both a linear source chain and a
// merge of multiple sources.
type Trace struct {
	Step    TraceStep
	Parents []*Trace
}

func (t *Trace) label() string {
	if t == nil {
		return "<no provenance>"
	}
	var b strings.Builder
	b.WriteString(t.Step.Text)
	if t.Step.Tag != TagInvalid {
		fmt.Fprintf(&b, " [%s]", t.Step.Tag)
	}
	if t.Step.Note != "" {
		fmt.Fprintf(&b, " (%s)", t.Step.Note)
	}
	if pos := t.Step.Pos.String(); pos != "" {
		fmt.Fprintf(&b, " at %s", pos)
	}
	return b.String()
}

// Lines renders the provenance tree root-first.
func (t *Trace) Lines() []string {
	if t == nil {
		return nil
	}
	out := []string{t.label()}
	var walkChildren func(*Trace, string)
	walkChildren = func(n *Trace, prefix string) {
		for i, parent := range n.Parents {
			connector := "|-- "
			nextPrefix := prefix + "|   "
			if i == len(n.Parents)-1 {
				connector = "`-- "
				nextPrefix = prefix + "    "
			}
			if parent == nil {
				out = append(out, prefix+connector+"<no provenance>")
				continue
			}
			out = append(out, prefix+connector+parent.label())
			walkChildren(parent, nextPrefix)
		}
	}
	walkChildren(t, "")
	return out
}

func (t *Trace) String() string {
	return strings.Join(t.Lines(), "\n")
}

// Chain returns one root-first path. When a trace has multiple parents, the
// first parent is followed; use Lines or a Finding to inspect a full merge.
func (t *Trace) Chain() []TraceStep {
	if t == nil {
		return nil
	}
	var steps []TraceStep
	for n := t; n != nil; n = firstParent(n) {
		steps = append(steps, n.Step)
	}
	return steps
}

func firstParent(t *Trace) *Trace {
	if len(t.Parents) == 0 {
		return nil
	}
	return t.Parents[0]
}

// Value is an abstract IR value.
//
// Bytes is meaningful only when HasBytes is true. Overflow records that the
// last arithmetic step could overflow the host integer range.
type Value struct {
	Tag      Tag
	HasConst bool
	Const    int64
	HasBytes bool
	Bytes    int64
	Overflow bool
	Trace    *Trace
}

// Join computes the least conservative merge of two tags. Const is neutral,
// equal classes stay equal, and every incompatible pair becomes a conflict.
// In particular Join(TagBE, TagLE) is TagConflict, not either input.
func Join(a, b Tag) Tag {
	if a == TagInvalid {
		return b
	}
	if b == TagInvalid {
		return a
	}
	if a == TagUnknown || b == TagUnknown {
		return TagUnknown
	}
	if a == TagConflict || b == TagConflict {
		return TagConflict
	}
	if a == b {
		return a
	}
	if a == TagConst {
		return b
	}
	if b == TagConst {
		return a
	}
	// BE+LE, raw+integer, and any other cross-class merge are unresolved.
	return TagConflict
}

// JoinAll folds Join over tags.
func JoinAll(tags ...Tag) Tag {
	out := TagInvalid
	for _, t := range tags {
		out = Join(out, t)
	}
	return out
}

// SwapTag models a byte-swap instruction at the type level. A byte swap toggles
// concrete orders but cannot repair a merge conflict.
func SwapTag(t Tag) Tag {
	switch t {
	case TagBE:
		return TagLE
	case TagLE:
		return TagBE
	default:
		return t
	}
}

// ConvertTag models an explicit order conversion. It is the only operation
// that can resolve TagConflict: the programmer states the intended order.
// Unknown input remains unknown because no concrete order is available.
func ConvertTag(t, target Tag) Tag {
	if target != TagBE && target != TagLE {
		return t
	}
	switch t {
	case TagUnknown:
		return TagUnknown
	case TagConflict, TagRaw, TagBE, TagLE, TagConst, TagInvalid:
		return target
	default:
		return TagUnknown
	}
}

// joinValues merges abstract values. It is used by OpPhi.
func joinValues(values []Value) Value {
	if len(values) == 0 {
		return Value{Tag: TagUnknown}
	}
	out := values[0]
	out.Trace = nil
	for _, v := range values[1:] {
		out.Tag = Join(out.Tag, v.Tag)
		out.Overflow = out.Overflow || v.Overflow
		if !(out.HasConst && v.HasConst && out.Const == v.Const) {
			out.HasConst = false
		}
		if !(out.HasBytes && v.HasBytes && out.Bytes == v.Bytes) {
			out.HasBytes = false
		}
	}
	return out
}

func cloneValue(v Value) Value {
	return v
}
