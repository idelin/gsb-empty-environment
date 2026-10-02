package decisiontable

import (
	"fmt"
	"sort"
	"strings"
	"text/tabwriter"
)

// Dim describes one condition dimension of the decision table.
type Dim struct {
	Name    string
	Domain  []Value // non-nil for categorical dimensions (enables wildcard folding)
	Numeric bool    // true for range-valued dimensions
}

// Rule is one row of the decision table: a condition grid plus an action.
type Rule struct {
	ID     int
	Conds  []Cond
	Action string
}

// Table is a set of rules over named dimensions.
type Table struct {
	Dims  []Dim
	Rules []Rule
}

func (t Table) String() string {
	var b strings.Builder
	w := tabwriter.NewWriter(&b, 0, 4, 2, ' ', 0)
	header := make([]string, 0, len(t.Dims)+2)
	header = append(header, "ID")
	for _, d := range t.Dims {
		header = append(header, d.Name)
	}
	header = append(header, "Action")
	fmt.Fprintln(w, strings.Join(header, "\t"))
	for _, r := range t.Rules {
		row := make([]string, 0, len(t.Dims)+2)
		row = append(row, fmt.Sprintf("R%d", r.ID))
		for _, c := range r.Conds {
			row = append(row, c.String())
		}
		row = append(row, r.Action)
		fmt.Fprintln(w, strings.Join(row, "\t"))
	}
	w.Flush()
	return b.String()
}

// Conflict describes two overlapping rules that produce different actions.
type Conflict struct {
	A, B   Rule
	Region []Cond // intersection of the two rules' condition grids
}

// FormatRegion renders a conflict region with dimension names.
func FormatRegion(dims []Dim, region []Cond) string {
	parts := make([]string, len(region))
	for i, c := range region {
		parts[i] = dims[i].Name + "=" + c.String()
	}
	return strings.Join(parts, ", ")
}

// DetectConflicts returns every pair of rules whose condition grids overlap
// while their actions differ.
func DetectConflicts(rules []Rule) []Conflict {
	var out []Conflict
	for i := 0; i < len(rules); i++ {
		for j := i + 1; j < len(rules); j++ {
			if rules[i].Action == rules[j].Action {
				continue
			}
			if region, ok := intersectRules(rules[i], rules[j]); ok {
				out = append(out, Conflict{A: rules[i], B: rules[j], Region: region})
			}
		}
	}
	return out
}

func intersectRules(a, b Rule) ([]Cond, bool) {
	region := make([]Cond, len(a.Conds))
	for k := range a.Conds {
		c := a.Conds[k].Intersect(b.Conds[k])
		if c == nil {
			return nil, false
		}
		region[k] = c
	}
	return region, true
}

// Compress reduces a rule set by (1) dropping rules subsumed by a broader
// rule with the same action and (2) merging same-action rules that differ in
// exactly one dimension into a wider (possibly wildcard) condition. It
// iterates to a fixpoint and returns rules ordered by ID.
func Compress(rules []Rule) []Rule {
	out := make([]Rule, len(rules))
	copy(out, rules)
	for changed := true; changed; {
		changed = false
		// Pass 1: remove rules subsumed by a same-action rule.
		for i := 0; i < len(out) && !changed; i++ {
			for j := 0; j < len(out) && !changed; j++ {
				if i == j || out[i].Action != out[j].Action {
					continue
				}
				cij, cji := coversRule(out[i], out[j]), coversRule(out[j], out[i])
				if cij && (!cji || i < j) {
					out = append(out[:j], out[j+1:]...)
					changed = true
				}
			}
		}
		if changed {
			continue
		}
		// Pass 2: merge same-action rules differing in exactly one dimension.
		for i := 0; i < len(out) && !changed; i++ {
			for j := i + 1; j < len(out) && !changed; j++ {
				if out[i].Action != out[j].Action {
					continue
				}
				if merged, ok := mergeRules(out[i], out[j]); ok {
					out[i] = merged
					out = append(out[:j], out[j+1:]...)
					changed = true
				}
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

func coversRule(a, b Rule) bool {
	for k := range a.Conds {
		if !a.Conds[k].Covers(b.Conds[k]) {
			return false
		}
	}
	return true
}

func mergeRules(a, b Rule) (Rule, bool) {
	diff := -1
	for k := range a.Conds {
		if !a.Conds[k].Equal(b.Conds[k]) {
			if diff >= 0 {
				return Rule{}, false // differ in more than one dimension
			}
			diff = k
		}
	}
	if diff < 0 {
		return Rule{}, false // identical grids; handled by subsumption
	}
	u, ok := a.Conds[diff].Union(b.Conds[diff])
	if !ok {
		return Rule{}, false
	}
	merged := Rule{ID: a.ID, Action: a.Action, Conds: make([]Cond, len(a.Conds))}
	copy(merged.Conds, a.Conds)
	merged.Conds[diff] = u
	return merged, true
}
