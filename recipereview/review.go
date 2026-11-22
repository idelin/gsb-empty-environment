package recipereview

import (
	"errors"
	"fmt"
	"sort"
)

// VerdictKind is the release-gate decision for one property.
type VerdictKind int

const (
	// VerdictSafe means the property was proven (symbolically or by full
	// enumeration) for every allowed input.
	VerdictSafe VerdictKind = iota
	// VerdictConflict means a concrete violating input pair was found.
	VerdictConflict
	// VerdictUnknown means the state budget was exhausted before the property
	// could be settled.
	VerdictUnknown
)

// WitnessKind identifies which property a witness violates.
type WitnessKind int

const (
	// WitnessCollision is equal info produced by two distinct assignments of
	// coexisting recipes (or two assignments of one recipe).
	WitnessCollision WitnessKind = iota
	// WitnessReadOnly is an encoding of a read-only legacy recipe that a
	// different-purpose recipe accepts as its own valid encoding.
	WitnessReadOnly
)

// Witness is a concrete, independently re-checkable violation.
type Witness struct {
	Kind           WitnessKind
	FirstRecipeID  int
	SecondRecipeID int
	First          Assignment
	Second         Assignment
	FirstInfo      []byte
	SecondInfo     []byte
	Pair           pairKey
}

// Verdict reports one release-gate property.
type Verdict struct {
	Kind       VerdictKind
	Property   string
	Witness    *Witness
	Symbolic   ProofResult
	Enumerated bool
	Reason     string
}

// Report is the full prescreen result over a recipe set.
type Report struct {
	Verdicts []*Verdict
	Budget   *Budget
}

// Safe reports whether every property was proven and no conflict was found.
func (rep *Report) Safe() bool {
	for _, v := range rep.Verdicts {
		if v.Kind != VerdictSafe {
			return false
		}
	}
	return true
}

type propertyKind int

const (
	propInjective propertyKind = iota
	propDistinct
	propReadOnly
)

type property struct {
	kind      propertyKind
	label     string
	r1, r2    *Recipe
	symbolic  ProofResult
	enumerate bool
}

func (w *Witness) matches(p *property) bool {
	switch p.kind {
	case propInjective:
		return w.Kind == WitnessCollision && w.FirstRecipeID == p.r1.ID && w.SecondRecipeID == p.r1.ID
	case propDistinct:
		return w.Kind == WitnessCollision &&
			((w.FirstRecipeID == p.r1.ID && w.SecondRecipeID == p.r2.ID) ||
				(w.FirstRecipeID == p.r2.ID && w.SecondRecipeID == p.r1.ID))
	case propReadOnly:
		return w.Kind == WitnessReadOnly &&
			((w.FirstRecipeID == p.r1.ID && w.SecondRecipeID == p.r2.ID) ||
				(w.FirstRecipeID == p.r2.ID && w.SecondRecipeID == p.r1.ID))
	}
	return false
}

// Options configures a release-gate review.
type Options struct {
	// Budget bounds enumeration state; nil selects the default budget.
	Budget *Budget
}

// Review prescreens a set of recipes against the compatibility table.
func Review(recipes []*Recipe, compat *CompatTable, opts *Options) (*Report, error) {
	if compat == nil {
		compat = NewCompatTable()
	}
	budget := defaultBudget(opts)
	if err := validateSet(recipes); err != nil {
		return nil, err
	}
	ordered := append([]*Recipe(nil), recipes...)
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].ID < ordered[j].ID })

	props := buildProperties(ordered, compat)
	report := &Report{Budget: budget}
	report.Verdicts = make([]*Verdict, 0, len(props))

	enumeratedProps := make([]*property, 0)
	for _, p := range props {
		v := &Verdict{Property: p.label, Symbolic: p.symbolic}
		if p.symbolic.Status == SymbolicProven {
			v.Kind = VerdictSafe
			v.Reason = "discharged by structural symbolic proof"
		} else {
			v.Reason = p.symbolic.Reason
			enumeratedProps = append(enumeratedProps, p)
		}
		report.Verdicts = append(report.Verdicts, v)
	}

	if len(enumeratedProps) == 0 {
		return report, nil
	}

	assignments := make(map[*Recipe][]Assignment, len(ordered))
	for _, r := range ordered {
		list, ok := enumerateAssignments(r, budget)
		if !ok {
			markUnknown(report, enumeratedProps, "state budget exhausted while enumerating field domain")
			for _, v := range report.Verdicts {
				if v.Kind == VerdictUnknown {
					v.Enumerated = true
				}
			}
			return report, nil
		}
		assignments[r] = list
	}

	runEnumeration(report, ordered, enumeratedProps, assignments, budget)
	return report, nil
}

func defaultBudget(opts *Options) *Budget {
	if opts != nil && opts.Budget != nil {
		return opts.Budget
	}
	return NewBudget(0)
}

func validateSet(recipes []*Recipe) error {
	if len(recipes) == 0 {
		return errors.New("recipereview: review requires at least one recipe")
	}
	seenIDs := map[int]bool{}
	seenRefs := map[VersionRef]bool{}
	for _, r := range recipes {
		if err := ValidateRecipe(r); err != nil {
			return err
		}
		if r.ID == 0 {
			return fmt.Errorf("recipereview: recipe %s/%s must carry a non-zero ID", r.Purpose, r.Version)
		}
		if seenIDs[r.ID] {
			return fmt.Errorf("recipereview: duplicate recipe ID %d", r.ID)
		}
		seenIDs[r.ID] = true
		ref := r.ref()
		if seenRefs[ref] {
			return fmt.Errorf("recipereview: duplicate recipe %s/%s", r.Purpose, r.Version)
		}
		seenRefs[ref] = true
	}
	return nil
}

func markUnknown(report *Report, props []*property, reason string) {
	index := map[string]*Verdict{}
	for _, v := range report.Verdicts {
		index[v.Property] = v
	}
	for _, p := range props {
		v := index[p.label]
		if v.Kind == VerdictSafe {
			continue
		}
		v.Kind = VerdictUnknown
		v.Enumerated = true
		v.Reason = reason
	}
}
