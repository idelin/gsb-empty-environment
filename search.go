package featureneg

// selection is a set of selected feature names.
type selection map[string]bool

func (s selection) clone() selection {
	out := make(selection, len(s))
	for k, v := range s {
		out[k] = v
	}
	return out
}

// score ranks selections lexicographically: preference weight, then
// total chosen versions.
type score struct {
	pref uint64
	vers uint64
}

func (s score) better(o score) bool {
	if s.pref != o.pref {
		return s.pref > o.pref
	}
	return s.vers > o.vers
}

// solver searches the optional-feature space globally. A per-feature
// local intersection cannot find the optimum because dependencies,
// conflicts and version conditions couple the choices.
type solver struct {
	ctx       *negCtx
	optional  []string
	base      selection
	budget    int
	nodes     int
	exhausted bool
	haveBest  bool
	best      score
	bestSel   selection
}

func newSolver(ctx *negCtx, required []string, budget int) *solver {
	base := selection{}
	for _, name := range required {
		base[name] = true
	}
	return &solver{ctx: ctx, optional: ctx.optional, base: base, budget: budget}
}

// run returns the best feasible selection. If the budget is exhausted
// the result is the best found so far and s.exhausted is true.
func (s *solver) run() (selection, bool) {
	s.search(0, s.base.clone())
	return s.bestSel, s.haveBest
}

func (s *solver) search(i int, sel selection) {
	if s.exhausted {
		return
	}
	s.nodes++
	if s.nodes > s.budget {
		s.exhausted = true
		return
	}
	if i == len(s.optional) {
		closed, ok := s.ctx.close(sel)
		if !ok {
			return
		}
		sc := s.ctx.scoreOf(closed)
		if !s.haveBest || sc.better(s.best) {
			s.haveBest = true
			s.best = sc
			s.bestSel = closed
		}
		return
	}
	name := s.optional[i]
	// Try inclusion first: with preference-sorted weights this tends to
	// find strong candidates early.
	with := sel.clone()
	with[name] = true
	s.search(i+1, with)
	s.search(i+1, sel)
}
