package bridge

import (
	"crypto/sha256"
	"fmt"
)

// Action is the per-field conversion classification.
type Action int

const (
	ActCopy   Action = iota // lossless: same ID and type on both sides (renames are lossless)
	ActExtend               // representable only via the target extension carrier
	ActDrop                 // unrepresentable and droppable (optional, no carrier)
)

func (a Action) String() string {
	switch a {
	case ActCopy:
		return "lossless"
	case ActExtend:
		return "preserve-extension"
	case ActDrop:
		return "unrepresentable"
	}
	return "?"
}

type FieldPlan struct {
	SourceID uint32
	Action   Action
	Reason   string
}

type Injection struct {
	TargetID uint32
	Field    Field
}

// Plan is a compiled one-direction conversion plan.
type Plan struct {
	From, To  int
	Steps     []FieldPlan
	Injects   []Injection
	stepByID  map[uint32]FieldPlan
	to        *Schema
	extID     uint32
	hasExt    bool
	srcExtID  uint32
	hasSrcExt bool
	digest    [32]byte
}

// Compile builds the conversion plan from one schema to the other. It fails
// if a required field on either side cannot be satisfied.
func Compile(from, to *Schema) (*Plan, error) {
	p := &Plan{From: from.Version, To: to.Version, to: to, stepByID: map[uint32]FieldPlan{}}
	if ext, ok := to.extension(); ok {
		p.extID, p.hasExt = ext.ID, true
	}
	if ext, ok := from.extension(); ok {
		p.srcExtID, p.hasSrcExt = ext.ID, true
	}
	matched := map[uint32]bool{}
	for _, f := range from.Fields {
		if f.Extension {
			continue // carrier handled dynamically at conversion time
		}
		var step FieldPlan
		if g, ok := to.byID[f.ID]; ok && !g.Extension && g.Type == f.Type {
			// Matched by ID: a rename keeps the ID, so it stays lossless.
			step = FieldPlan{SourceID: f.ID, Action: ActCopy}
		} else if p.hasExt {
			step = FieldPlan{SourceID: f.ID, Action: ActExtend,
				Reason: "changed or absent in target; preserved via extension"}
		} else if f.Required {
			return nil, fmt.Errorf("bridge: required field %d (%s) is unrepresentable in v%d",
				f.ID, f.Name, to.Version)
		} else {
			step = FieldPlan{SourceID: f.ID, Action: ActDrop,
				Reason: "unrepresentable in target and no extension carrier"}
		}
		if step.Action == ActCopy {
			matched[f.ID] = true
		}
		p.Steps = append(p.Steps, step)
		p.stepByID[f.ID] = step
	}
	for _, g := range to.Fields {
		if g.Extension || !g.Required || matched[g.ID] {
			continue
		}
		if !g.Rule.HasDefault {
			return nil, fmt.Errorf("bridge: v%d requires field %d (%s) which v%d cannot provide",
				to.Version, g.ID, g.Name, from.Version)
		}
		p.Injects = append(p.Injects, Injection{TargetID: g.ID, Field: defaultField(g)})
	}
	h := sha256.New()
	fmt.Fprintf(h, "plan v%d->v%d ext=%d:%v\n", p.From, p.To, p.extID, p.hasExt)
	for _, s := range p.Steps {
		fmt.Fprintf(h, "%d:%d\n", s.SourceID, s.Action)
	}
	for _, inj := range p.Injects {
		fmt.Fprintf(h, "inj %d=%v\n", inj.TargetID, inj.Field)
	}
	copy(p.digest[:], h.Sum(nil))
	return p, nil
}

func defaultField(g FieldDef) Field {
	if g.Type == TVarint {
		return Field{ID: g.ID, Kind: KindVarint, Num: g.Rule.DefaultNum}
	}
	raw := make([]byte, len(g.Rule.DefaultByte))
	copy(raw, g.Rule.DefaultByte)
	return Field{ID: g.ID, Kind: KindBytes, Raw: raw}
}

// Classification groups source field IDs by conversion action.
func (p *Plan) Classification() (lossless, extended, unrepresentable []uint32) {
	for _, s := range p.Steps {
		switch s.Action {
		case ActCopy:
			lossless = append(lossless, s.SourceID)
		case ActExtend:
			extended = append(extended, s.SourceID)
		case ActDrop:
			unrepresentable = append(unrepresentable, s.SourceID)
		}
	}
	return
}

// Digest identifies the plan; sessions bind to it during negotiation.
func (p *Plan) Digest() [32]byte { return p.digest }

// PlanSet holds both directions of a schema pair.
type PlanSet struct {
	S1, S2 *Schema
	Fwd    *Plan // v1 -> v2
	Rev    *Plan // v2 -> v1
	digest [32]byte
}

func NewPlanSet(s1, s2 *Schema) (*PlanSet, error) {
	fwd, err := Compile(s1, s2)
	if err != nil {
		return nil, err
	}
	rev, err := Compile(s2, s1)
	if err != nil {
		return nil, err
	}
	h := sha256.New()
	h.Write(fwd.digest[:])
	h.Write(rev.digest[:])
	ps := &PlanSet{S1: s1, S2: s2, Fwd: fwd, Rev: rev}
	copy(ps.digest[:], h.Sum(nil))
	return ps, nil
}

// Digest binds a session to exactly this pair of plans.
func (ps *PlanSet) Digest() [32]byte { return ps.digest }
