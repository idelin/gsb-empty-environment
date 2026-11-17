package netsec

// CoveredRect is one rectangle of the diff cover together with the
// rules that explain it.
type CoveredRect struct {
	// Rect is a non-empty rectangle of newly allowed points.
	Rect Rect `json:"rect"`
	// GrantedBy names the new-policy Allow rules that hit Rect.
	GrantedBy []string `json:"grantedBy"`
	// PreviouslyDeniedBy names the old-policy Deny rules that hit
	// Rect. Empty means the rectangle was rejected by default
	// (no old Allow matched) rather than by an explicit Deny.
	PreviouslyDeniedBy []string `json:"previouslyDeniedBy,omitempty"`
}

// Result is the outcome of Diff.
type Result struct {
	// Covered is the completed part of the diff: pairwise disjoint
	// rectangles whose union contains only newly allowed points.
	Covered []CoveredRect `json:"covered"`
	// Remainder is the not-yet-covered domain, present only when the
	// shard budget was exceeded. Its union with Covered covers every
	// newly allowed point; re-run Diff on each rectangle (see Clip)
	// to finish the job.
	Remainder []Rect `json:"remainder,omitempty"`
	// Complete is true when Covered is the full diff and Remainder
	// is empty.
	Complete bool `json:"complete"`
}

// Diff computes the semantic diff between oldPol and newPol over dom:
// the set of points allowed by newPol and rejected by oldPol,
// expressed as a non-overlapping rectangle cover with hit rules
// attached.
//
// budget bounds the number of rectangles accumulated in Covered and
// Remainder; zero means unlimited. When the budget is exceeded, Diff
// stops early and returns the not-yet-covered remainder domain in
// Result.Remainder with Complete set to false. Covered, Remainder
// and the true diff always satisfy: Covered ⊆ diff ⊆ Covered ∪
// Remainder, and all rectangles across Covered and Remainder are
// pairwise disjoint.
func Diff(newPol, oldPol *Policy, dom Domain, budget int) Result {
	oldCover, ok := allowedCover(oldPol, dom, budget)
	if !ok {
		return Result{Remainder: []Rect{FullRect()}}
	}

	// Subtractors: new-policy denies plus everything the old policy
	// allowed.
	var subs []Rect
	for _, r := range newPol.rulesByEffect(Deny) {
		subs = append(subs, r.When)
	}
	subs = append(subs, oldCover...)

	res := Result{Complete: true}
	overBudget := func(extra int) bool {
		return budget > 0 && len(res.Covered)+len(res.Remainder)+extra > budget
	}
	// flush moves rectangles into Remainder, keeping the whole
	// result pairwise disjoint.
	flush := func(rs []Rect) {
		rs = dom.subtractAll(rs, coveredRects(res.Covered))
		rs = dom.subtractAll(rs, res.Remainder)
		res.Remainder = append(res.Remainder, rs...)
	}

	allows := newPol.rulesByEffect(Allow)
	for i, rule := range allows {
		if overBudget(1) {
			flush(ruleRects(allows[i:]))
			res.Complete = false
			return res
		}
		// Drop points already covered by earlier Allow rules.
		pieces := dom.subtractAll([]Rect{rule.When}, coveredRects(res.Covered))
		exceeded := false
		for _, s := range subs {
			pieces = dom.subtractAll(pieces, []Rect{s})
			if overBudget(len(pieces)) {
				exceeded = true
				break
			}
		}
		if exceeded {
			flush(pieces)
			flush(ruleRects(allows[i+1:]))
			res.Complete = false
			return res
		}
		for _, piece := range pieces {
			res.Covered = append(res.Covered, CoveredRect{
				Rect:               piece,
				GrantedBy:          hitRules(newPol, Allow, piece),
				PreviouslyDeniedBy: hitRules(oldPol, Deny, piece),
			})
		}
	}
	return res
}

// allowedCover returns a disjoint rectangle cover of the set of
// points allowed by p, or false when the cover exceeds budget.
func allowedCover(p *Policy, dom Domain, budget int) ([]Rect, bool) {
	var denies []Rect
	for _, r := range p.rulesByEffect(Deny) {
		denies = append(denies, r.When)
	}
	var cover []Rect
	for _, rule := range p.rulesByEffect(Allow) {
		pieces := dom.subtractAll([]Rect{rule.When}, denies)
		pieces = dom.subtractAll(pieces, cover)
		cover = append(cover, pieces...)
		if budget > 0 && len(cover) > budget {
			return nil, false
		}
	}
	return cover, true
}

// hitRules names the rules of p with the given effect whose
// condition intersects r.
func hitRules(p *Policy, e Effect, r Rect) []string {
	var names []string
	for _, rule := range p.rulesByEffect(e) {
		if rule.When.Intersects(r) {
			names = append(names, rule.Name)
		}
	}
	return names
}

func coveredRects(c []CoveredRect) []Rect {
	out := make([]Rect, len(c))
	for i, cr := range c {
		out[i] = cr.Rect
	}
	return out
}

func ruleRects(rules []Rule) []Rect {
	out := make([]Rect, len(rules))
	for i, r := range rules {
		out[i] = r.When
	}
	return out
}
