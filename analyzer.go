package main

import (
	"fmt"
	"sort"
)

const (
	StatusEffective         = "effective"
	StatusPartiallyShadowed = "partially-shadowed"
	StatusFullyShadowed     = "fully-shadowed"
	StatusEmpty             = "empty"
)

type RuleReport struct {
	Index     int      `json:"index"`
	Status    string   `json:"status"`
	Witness   *Request `json:"witness,omitempty"`
	CoveredBy []int    `json:"coveredBy,omitempty"`
}

type compiledRule struct {
	effect   string
	subjects map[string]bool
	actions  map[string]bool
	pat      pathPattern
}

func (r *compiledRule) matches(req Request) bool {
	return r.subjects[req.Subject] && r.actions[req.Action] && r.pat.matches(req.Path)
}

func compileRules(cfg *Config) ([]compiledRule, error) {
	out := make([]compiledRule, len(cfg.Rules))
	for i, spec := range cfg.Rules {
		subs, err := expandNames(spec.Subjects, cfg.Subjects, "subject", i)
		if err != nil {
			return nil, err
		}
		acts, err := expandNames(spec.Actions, cfg.Actions, "action", i)
		if err != nil {
			return nil, err
		}
		pat, err := parsePattern(spec.Path)
		if err != nil {
			return nil, fmt.Errorf("rule %d: bad path pattern: %v", i, err)
		}
		out[i] = compiledRule{effect: spec.Effect, subjects: subs, actions: acts, pat: pat}
	}
	return out, nil
}

func expandNames(list, domain []string, what string, ruleIdx int) (map[string]bool, error) {
	known := map[string]bool{}
	for _, d := range domain {
		known[d] = true
	}
	set := map[string]bool{}
	for _, n := range list {
		if n == "*" {
			for _, d := range domain {
				set[d] = true
			}
			continue
		}
		if !known[n] {
			return nil, fmt.Errorf("rule %d: unknown %s %q", ruleIdx, what, n)
		}
		set[n] = true
	}
	return set, nil
}

func Analyze(cfg *Config) ([]RuleReport, error) {
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	reqs, err := enumerateRequests(cfg)
	if err != nil {
		return nil, err
	}
	rules, err := compileRules(cfg)
	if err != nil {
		return nil, err
	}

	matches := make([]*bitset, len(rules))
	for i := range rules {
		bs := newBitset(len(reqs))
		for j, req := range reqs {
			if rules[i].matches(req) {
				bs.set(j)
			}
		}
		matches[i] = bs
	}

	reports := make([]RuleReport, 0, len(rules))
	prior := newBitset(len(reqs))
	var priorSets []candSet
	for i := range rules {
		m := matches[i]
		rep := RuleReport{Index: i}
		switch {
		case m.isZero():
			rep.Status = StatusEmpty
			rep.CoveredBy = []int{}
		case m.andNot(prior).isZero():
			rep.Status = StatusFullyShadowed
			rep.CoveredBy = minCover(m, priorSets)
		default:
			w := reqs[m.andNot(prior).first()]
			rep.Witness = &w
			if m.and(prior).isZero() {
				rep.Status = StatusEffective
			} else {
				rep.Status = StatusPartiallyShadowed
			}
		}
		prior.or(m)
		priorSets = append(priorSets, candSet{bits: m, rule: i})
		reports = append(reports, rep)
	}
	return reports, nil
}

type candSet struct {
	bits *bitset
	rule int
}

type coverCand struct {
	bits *bitset
	rule int
	size int
}

// minCover returns a minimum-cardinality set of earlier rule indices whose
// match sets jointly cover universe. Deterministic: candidates are ordered by
// descending coverage size, ties broken by rule index.
func minCover(universe *bitset, prior []candSet) []int {
	if universe.isZero() {
		return []int{}
	}
	seen := map[string]bool{}
	var cs []coverCand
	for _, p := range prior {
		inter := p.bits.and(universe)
		if inter.isZero() {
			continue
		}
		k := inter.key()
		if seen[k] {
			continue
		}
		seen[k] = true
		cs = append(cs, coverCand{bits: inter, rule: p.rule, size: inter.count()})
	}
	sort.Slice(cs, func(i, j int) bool {
		if cs[i].size != cs[j].size {
			return cs[i].size > cs[j].size
		}
		return cs[i].rule < cs[j].rule
	})

	best := greedyCover(universe, cs)
	if best == nil {
		return nil
	}

	var dfs func(uncovered *bitset, chosen []int)
	dfs = func(uncovered *bitset, chosen []int) {
		if len(chosen) >= len(best) {
			return
		}
		if uncovered.isZero() {
			best = append([]int(nil), chosen...)
			return
		}
		maxSize := 0
		for _, c := range cs {
			if c.size > maxSize {
				maxSize = c.size
			}
		}
		rem := uncovered.count()
		if len(chosen)+(rem+maxSize-1)/maxSize >= len(best) {
			return
		}
		e := uncovered.first()
		for _, c := range cs {
			if !c.bits.get(e) {
				continue
			}
			dfs(uncovered.andNot(c.bits), append(chosen, c.rule))
		}
	}
	dfs(universe.clone(), nil)

	sort.Ints(best)
	return best
}

func greedyCover(universe *bitset, cs []coverCand) []int {
	uncovered := universe.clone()
	var chosen []int
	for !uncovered.isZero() {
		bestGain, bestIdx := 0, -1
		for i, c := range cs {
			gain := c.bits.and(uncovered).count()
			if gain > bestGain {
				bestGain, bestIdx = gain, i
			}
		}
		if bestIdx < 0 {
			return nil
		}
		chosen = append(chosen, cs[bestIdx].rule)
		uncovered = uncovered.andNot(cs[bestIdx].bits)
	}
	return chosen
}
