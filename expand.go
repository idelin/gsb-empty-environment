package spaneval

// slot is one expected span instance after expanding the rule graph
// against concrete trace attributes.
type slot struct {
	name     string  // accepted span name ("" for choice slots)
	choice   bool    // exactly one branch is expected
	optional bool    // absence costs nothing
	children []*slot // expected children of a matched span
	branches []*slot // choice alternatives
}

// expandRule instantiates a rule into repeat-count slots. Conditions are
// evaluated here, so a node whose condition is false yields no slots.
func expandRule(r *Rule, attrs map[string]string) []*slot {
	if !condOK(r.When, attrs) {
		return nil
	}
	if r.Kind == "choice" {
		s := &slot{choice: true, optional: r.MinN() == 0}
		for _, b := range r.Children {
			if !condOK(b.When, attrs) {
				continue
			}
			s.branches = append(s.branches, expandOne(b, attrs, false))
		}
		return []*slot{s}
	}
	out := make([]*slot, 0, r.MaxN())
	for i := 0; i < r.MaxN(); i++ {
		out = append(out, expandOne(r, attrs, i >= r.MinN()))
	}
	return out
}

func expandOne(r *Rule, attrs map[string]string, optional bool) *slot {
	s := &slot{name: r.Name, optional: optional}
	for _, c := range r.Children {
		s.children = append(s.children, expandRule(c, attrs)...)
	}
	return s
}

// requiredSize is the number of required slots in the subtree, 0 for
// optional slots (their absence is not a miss).
func requiredSize(s *slot) int {
	if s.optional {
		return 0
	}
	return requiredSizeForced(s)
}

// requiredSizeForced counts the subtree as if s itself were required.
func requiredSizeForced(s *slot) int {
	n := 1
	if s.choice {
		n += minBranchSize(s)
	}
	for _, c := range s.children {
		n += requiredSize(c)
	}
	return n
}

// minBranchSize is the cheapest branch of a choice: a missing choice costs
// the smallest required branch, a present extra branch is a duplicate.
func minBranchSize(s *slot) int {
	best := -1
	for _, b := range s.branches {
		if r := requiredSizeForced(b); best < 0 || r < best {
			best = r
		}
	}
	if best < 0 {
		return 0
	}
	return best
}
