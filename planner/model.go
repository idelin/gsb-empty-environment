package planner

import (
	"fmt"
	"sort"
	"time"
)

// DefaultMaxEvidence is the upper bound on collected evidence items per plan.
const DefaultMaxEvidence = 25

// Evidence describes a single piece of collectible evidence.
type Evidence struct {
	ID         string   `json:"id"`
	Cost       float64  `json:"cost"`
	ValidFrom  string   `json:"validFrom,omitempty"`  // YYYY-MM-DD, empty = no lower bound
	ValidUntil string   `json:"validUntil,omitempty"` // YYYY-MM-DD, empty = never expires
	MutexWith  []string `json:"mutexWith,omitempty"`  // explicit pairwise exclusions
	Group      string   `json:"group,omitempty"`      // at most one evidence per group
}

// Criterion is one acceptance entry guarded by a boolean expression.
type Criterion struct {
	ID   string `json:"id"`
	Expr string `json:"expr"`
}

// Spec is the full planner input.
type Spec struct {
	AsOf        string      `json:"asOf"` // YYYY-MM-DD, plan date; empty = today
	MaxEvidence int         `json:"maxEvidence,omitempty"`
	Evidence    []Evidence  `json:"evidence"`
	Criteria    []Criterion `json:"criteria"`
}

// EvidenceInfo is the resolved, validated view of one evidence item.
type EvidenceInfo struct {
	Ev         Evidence
	ValidFrom  time.Time
	HasFrom    bool
	ValidUntil time.Time
	HasUntil   bool
	Mutex      map[string]bool
}

// Instance is the validated planning problem.
type Instance struct {
	AsOf        time.Time
	MaxEvidence int
	Evidence    map[string]*EvidenceInfo
	Order       []string // stable evidence id order
	Criteria    []*CompiledCriterion
}

// CompiledCriterion pairs a criterion with its parsed expression.
type CompiledCriterion struct {
	ID   string
	Expr string
	Root *Node
}

const dateLayout = "2006-01-02"

func parseDate(s string) (time.Time, error) {
	return time.ParseInLocation(dateLayout, s, time.UTC)
}

// Valid reports whether the evidence is usable on the given date.
func (e *EvidenceInfo) Valid(asOf time.Time) bool {
	if e.HasFrom && asOf.Before(e.ValidFrom) {
		return false
	}
	if e.HasUntil && asOf.After(e.ValidUntil) {
		return false
	}
	return true
}

// Compile validates the spec and builds the planning instance.
func Compile(spec *Spec) (*Instance, error) {
	inst := &Instance{
		Evidence: make(map[string]*EvidenceInfo),
	}
	asOf := spec.AsOf
	if asOf == "" {
		asOf = time.Now().UTC().Format(dateLayout)
	}
	t, err := parseDate(asOf)
	if err != nil {
		return nil, fmt.Errorf("invalid asOf date %q: %v", spec.AsOf, err)
	}
	inst.AsOf = t

	inst.MaxEvidence = spec.MaxEvidence
	if inst.MaxEvidence <= 0 {
		inst.MaxEvidence = DefaultMaxEvidence
	}

	for _, ev := range spec.Evidence {
		if ev.ID == "" {
			return nil, fmt.Errorf("evidence with empty id")
		}
		if _, dup := inst.Evidence[ev.ID]; dup {
			return nil, fmt.Errorf("duplicate evidence id %q", ev.ID)
		}
		if ev.Cost < 0 {
			return nil, fmt.Errorf("evidence %q has negative cost", ev.ID)
		}
		info := &EvidenceInfo{Ev: ev, Mutex: make(map[string]bool)}
		if ev.ValidFrom != "" {
			vf, err := parseDate(ev.ValidFrom)
			if err != nil {
				return nil, fmt.Errorf("evidence %q: invalid validFrom: %v", ev.ID, err)
			}
			info.ValidFrom, info.HasFrom = vf, true
		}
		if ev.ValidUntil != "" {
			vu, err := parseDate(ev.ValidUntil)
			if err != nil {
				return nil, fmt.Errorf("evidence %q: invalid validUntil: %v", ev.ID, err)
			}
			info.ValidUntil, info.HasUntil = vu, true
		}
		if info.HasFrom && info.HasUntil && info.ValidUntil.Before(info.ValidFrom) {
			return nil, fmt.Errorf("evidence %q: validUntil before validFrom", ev.ID)
		}
		for _, other := range ev.MutexWith {
			info.Mutex[other] = true
		}
		inst.Evidence[ev.ID] = info
		inst.Order = append(inst.Order, ev.ID)
	}

	// Symmetrize explicit mutex pairs.
	for _, id := range inst.Order {
		info := inst.Evidence[id]
		for other := range info.Mutex {
			oi, ok := inst.Evidence[other]
			if !ok {
				return nil, fmt.Errorf("evidence %q: mutexWith references unknown evidence %q", id, other)
			}
			oi.Mutex[id] = true
		}
	}

	// Group-based mutual exclusion: at most one per group.
	groups := make(map[string][]string)
	for _, id := range inst.Order {
		if g := inst.Evidence[id].Ev.Group; g != "" {
			groups[g] = append(groups[g], id)
		}
	}
	for _, members := range groups {
		for i := 0; i < len(members); i++ {
			for j := i + 1; j < len(members); j++ {
				inst.Evidence[members[i]].Mutex[members[j]] = true
				inst.Evidence[members[j]].Mutex[members[i]] = true
			}
		}
	}

	for _, c := range spec.Criteria {
		if c.ID == "" {
			return nil, fmt.Errorf("criterion with empty id")
		}
		root, err := ParseExpr(c.Expr)
		if err != nil {
			return nil, fmt.Errorf("criterion %q: %v", c.ID, err)
		}
		for _, ref := range ReferencedEvidence(root) {
			if _, ok := inst.Evidence[ref]; !ok {
				return nil, fmt.Errorf("criterion %q: expression references unknown evidence %q", c.ID, ref)
			}
		}
		inst.Criteria = append(inst.Criteria, &CompiledCriterion{ID: c.ID, Expr: c.Expr, Root: root})
	}
	return inst, nil
}

// Conflicts returns the first mutex violation between the two evidence sets.
func (inst *Instance) Conflicts(a, b map[string]bool) (string, string, bool) {
	ids := make([]string, 0, len(a))
	for id := range a {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		info := inst.Evidence[id]
		others := make([]string, 0, len(b))
		for o := range b {
			others = append(others, o)
		}
		sort.Strings(others)
		for _, o := range others {
			if id != o && info.Mutex[o] {
				return id, o, true
			}
		}
	}
	return "", "", false
}
