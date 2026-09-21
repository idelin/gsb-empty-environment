package bridge

import (
	"crypto/sha256"
	"fmt"
	"io"
	"sort"
	"strings"
)

// FieldClass is the conversion classification of one field.
type FieldClass int

const (
	// ClassLossless converts without loss (direct, renamed, defaulted,
	// or optional-and-absent).
	ClassLossless FieldClass = iota
	// ClassExtension is not representable in the target schema and is
	// preserved inside the extension blob.
	ClassExtension
	// ClassUnrepresentable cannot be provided at all (e.g. a required
	// target field the source cannot supply); it rejects negotiation.
	ClassUnrepresentable
)

func (c FieldClass) String() string {
	switch c {
	case ClassLossless:
		return "lossless"
	case ClassExtension:
		return "extension"
	case ClassUnrepresentable:
		return "unrepresentable"
	}
	return "?"
}

// PlanEntry classifies one field of a compiled plan.
type PlanEntry struct {
	FieldID uint32
	Class   FieldClass
	Reason  string
}

// Plan is a compiled one-direction conversion plan.
type Plan struct {
	From     *Schema
	To       *Schema
	Entries  []PlanEntry
	Defaults map[uint32]Value
}

// Compile builds the conversion plan from one schema to another.
func Compile(from, to *Schema) (*Plan, error) {
	if err := from.Validate(); err != nil {
		return nil, err
	}
	if err := to.Validate(); err != nil {
		return nil, err
	}
	p := &Plan{From: from, To: to, Defaults: map[uint32]Value{}}
	for _, f := range to.sortedFields() {
		sf, ok := from.Fields[f.ID]
		switch {
		case ok && sf.Type == f.Type:
			reason := ""
			if sf.Name != f.Name {
				reason = fmt.Sprintf("renamed %q -> %q", sf.Name, f.Name)
			}
			p.Entries = append(p.Entries, PlanEntry{f.ID, ClassLossless, reason})
		case ok:
			p.Entries = append(p.Entries, PlanEntry{f.ID, ClassUnrepresentable,
				fmt.Sprintf("type mismatch %s -> %s", sf.Type, f.Type)})
		default:
			if def, has := to.Defaults[f.ID]; has {
				p.Defaults[f.ID] = def
				p.Entries = append(p.Entries, PlanEntry{f.ID, ClassLossless, "defaulted by migration rule"})
			} else if f.Required {
				p.Entries = append(p.Entries, PlanEntry{f.ID, ClassUnrepresentable,
					"required by target, source cannot provide"})
			} else {
				p.Entries = append(p.Entries, PlanEntry{f.ID, ClassLossless, "optional, omitted when absent"})
			}
		}
	}
	for _, f := range from.sortedFields() {
		if _, ok := to.Fields[f.ID]; !ok {
			p.Entries = append(p.Entries, PlanEntry{f.ID, ClassExtension, "preserved in extension blob"})
		}
	}
	sort.Slice(p.Entries, func(i, j int) bool { return p.Entries[i].FieldID < p.Entries[j].FieldID })
	return p, nil
}

func (p *Plan) unrepresentable() []PlanEntry {
	var out []PlanEntry
	for _, e := range p.Entries {
		if e.Class == ClassUnrepresentable {
			out = append(out, e)
		}
	}
	return out
}

// PlanPair is a compiled bidirectional plan with a binding digest.
type PlanPair struct {
	AB, BA *Plan
	digest [32]byte
}

// Digest is the plan summary a session negotiates and binds to.
func (pp *PlanPair) Digest() [32]byte { return pp.digest }

// CompilePair compiles both directions and rejects the pair if either
// direction contains an unrepresentable field.
func CompilePair(a, b *Schema) (*PlanPair, error) {
	ab, err := Compile(a, b)
	if err != nil {
		return nil, err
	}
	ba, err := Compile(b, a)
	if err != nil {
		return nil, err
	}
	var bad []string
	for _, e := range ab.unrepresentable() {
		bad = append(bad, fmt.Sprintf("%s->%s field %d (%s)", a.Name, b.Name, e.FieldID, e.Reason))
	}
	for _, e := range ba.unrepresentable() {
		bad = append(bad, fmt.Sprintf("%s->%s field %d (%s)", b.Name, a.Name, e.FieldID, e.Reason))
	}
	if len(bad) > 0 {
		return nil, fmt.Errorf("bridge: negotiation rejected: %s", strings.Join(bad, "; "))
	}
	pp := &PlanPair{AB: ab, BA: ba}
	h := sha256.New()
	writePlanSummary(h, ab)
	writePlanSummary(h, ba)
	copy(pp.digest[:], h.Sum(nil))
	return pp, nil
}

func writePlanSummary(w io.Writer, p *Plan) {
	fmt.Fprintf(w, "plan %s v%d -> %s v%d\n", p.From.Name, p.From.Version, p.To.Name, p.To.Version)
	for _, e := range p.Entries {
		fmt.Fprintf(w, "field %d class %d %s\n", e.FieldID, e.Class, e.Reason)
	}
}
