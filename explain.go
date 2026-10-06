package explain

import (
	"errors"
	"fmt"
	"sort"
	"sync"
)

// MaxVars is the maximum number of finite-domain variables supported.
const MaxVars = 12

// maxEnum caps the total number of assignments ever enumerated in one
// sufficiency check, keeping exhaustive verification cheap.
const maxEnum = 1 << 22

// Problem describes one explanation request.
type Problem struct {
	// Rule is the boolean rule tree.
	Rule Node
	// Domains gives the finite value domain of every variable. At most
	// MaxVars entries; every domain must be non-empty. Variables referenced
	// by the rule but absent here are unknown inputs.
	Domains map[string][]int64
	// Version identifies the rule version. Explanations are cached per
	// version; a new version never reuses a stale explanation.
	Version string
}

// Explanation is the result: the smallest set of variables that must stay
// fixed at their current values to guarantee Outcome.
type Explanation struct {
	Version string
	// Fixed holds the minimal known-variable set, each mapped to its
	// current value.
	Fixed Assignment
	// Vars lists the fixed variable names, sorted.
	Vars []string
	// Outcome is the guaranteed conclusion and error behavior.
	Outcome Outcome
}

// validate checks the problem shape and the enumeration budget.
func validate(p Problem, cur Assignment) ([]string, error) {
	if p.Rule == nil {
		return nil, errors.New("explain: nil rule")
	}
	if len(p.Domains) > MaxVars {
		return nil, fmt.Errorf("explain: %d variables exceed the limit of %d", len(p.Domains), MaxVars)
	}
	total := 1
	names := make([]string, 0, len(p.Domains))
	for name, dom := range p.Domains {
		if len(dom) == 0 {
			return nil, fmt.Errorf("explain: empty domain for %q", name)
		}
		if _, ok := cur[name]; !ok {
			return nil, fmt.Errorf("explain: current assignment missing %q", name)
		}
		total *= len(dom)
		if total > maxEnum {
			return nil, fmt.Errorf("explain: domain product %d exceeds enumeration budget", total)
		}
		names = append(names, name)
	}
	sort.Strings(names)
	return names, nil
}

// enumerate walks every assignment in which the variables of fixed keep
// their current values and all other variables range over their full
// domains. It stops early, returning false, when fn rejects an assignment.
func enumerate(names []string, p Problem, cur Assignment, fixed map[string]bool, fn func(Assignment) bool) bool {
	a := make(Assignment, len(names))
	var walk func(i int) bool
	walk = func(i int) bool {
		if i == len(names) {
			return fn(a)
		}
		name := names[i]
		if fixed[name] {
			a[name] = cur[name]
			return walk(i + 1)
		}
		for _, v := range p.Domains[name] {
			a[name] = v
			if !walk(i + 1) {
				return false
			}
		}
		return true
	}
	return walk(0)
}

// sufficient reports, by exhaustive enumeration, whether pinning the given
// variables to their current values forces the baseline outcome.
func sufficient(names []string, p Problem, cur Assignment, fixedVars []string, base Outcome) bool {
	fixed := make(map[string]bool, len(fixedVars))
	for _, v := range fixedVars {
		fixed[v] = true
	}
	return enumerate(names, p, cur, fixed, func(a Assignment) bool {
		return Eval(p.Rule, a).same(base)
	})
}

// combinations calls fn for every size-k subset of names (already sorted) in
// lexicographic order, stopping early when fn returns true.
func combinations(names []string, k int, fn func([]string) bool) bool {
	idx := make([]int, k)
	for i := range idx {
		idx[i] = i
	}
	for {
		combo := make([]string, k)
		for i, j := range idx {
			combo[i] = names[j]
		}
		if fn(combo) {
			return true
		}
		i := k - 1
		for i >= 0 && idx[i] == len(names)-k+i {
			i--
		}
		if i < 0 {
			return false
		}
		idx[i]++
		for j := i + 1; j < k; j++ {
			idx[j] = idx[j-1] + 1
		}
	}
}

// search finds the minimum-cardinality sufficient set; ties are broken by
// lexicographic order of the sorted variable names.
func search(names []string, p Problem, cur Assignment, base Outcome) []string {
	for size := 0; size <= len(names); size++ {
		var hit []string
		if combinations(names, size, func(combo []string) bool {
			if sufficient(names, p, cur, combo, base) {
				hit = combo
				return true
			}
			return false
		}) {
			return hit
		}
	}
	return append([]string(nil), names...)
}

// shrink greedily drops variables (largest names first, keeping the result
// lexicographically small) from a sufficient set, re-verifying each removal
// by exhaustive enumeration. Used when domains contract after caching.
func shrink(names []string, p Problem, cur Assignment, fixed []string, base Outcome) []string {
	keep := append([]string(nil), fixed...)
	sort.Strings(keep)
	for i := len(keep) - 1; i >= 0; i-- {
		cand := append(append([]string(nil), keep[:i]...), keep[i+1:]...)
		if sufficient(names, p, cur, cand, base) {
			keep = cand
		}
	}
	return keep
}

func build(p Problem, cur Assignment, vars []string, base Outcome) Explanation {
	fixed := make(Assignment, len(vars))
	for _, v := range vars {
		fixed[v] = cur[v]
	}
	sorted := append([]string(nil), vars...)
	sort.Strings(sorted)
	return Explanation{Version: p.Version, Fixed: fixed, Vars: sorted, Outcome: base}
}

// Explain computes the minimal explanation from scratch.
func Explain(p Problem, cur Assignment) (Explanation, error) {
	names, err := validate(p, cur)
	if err != nil {
		return Explanation{}, err
	}
	base := Eval(p.Rule, cur)
	vars := search(names, p, cur, base)
	return build(p, cur, vars, base), nil
}

// Verify re-checks an explanation by exhaustive enumeration: sufficiency
// reports whether fixing expl.Fixed guarantees expl.Outcome under the given
// domains; minimal reports whether no proper subset of expl.Fixed is also
// sufficient.
func Verify(p Problem, cur Assignment, expl Explanation) (suff bool, minimal bool, err error) {
	names, err := validate(p, cur)
	if err != nil {
		return false, false, err
	}
	vars := append([]string(nil), expl.Vars...)
	sort.Strings(vars)
	if !sufficient(names, p, cur, vars, expl.Outcome) {
		return false, false, nil
	}
	minimal = true
	for size := 0; size < len(vars) && minimal; size++ {
		combinations(vars, size, func(combo []string) bool {
			if sufficient(names, p, cur, combo, expl.Outcome) {
				minimal = false
				return true
			}
			return false
		})
	}
	return true, minimal, nil
}

// Explainer caches explanations bound to the rule version. A cached
// explanation is reused only after exhaustive re-verification against the
// current domains, and is re-shortened when those domains have contracted.
type Explainer struct {
	mu    sync.Mutex
	cache map[string]Explanation
}

func NewExplainer() *Explainer {
	return &Explainer{cache: make(map[string]Explanation)}
}

// Explain returns the explanation for the problem, using and refreshing the
// version-bound cache.
func (e *Explainer) Explain(p Problem, cur Assignment) (Explanation, error) {
	names, err := validate(p, cur)
	if err != nil {
		return Explanation{}, err
	}
	base := Eval(p.Rule, cur)

	e.mu.Lock()
	defer e.mu.Unlock()

	if cached, ok := e.cache[p.Version]; ok {
		if sufficient(names, p, cur, cached.Vars, base) {
			vars := shrink(names, p, cur, cached.Vars, base)
			expl := build(p, cur, vars, base)
			e.cache[p.Version] = expl
			return expl, nil
		}
	}
	vars := search(names, p, cur, base)
	expl := build(p, cur, vars, base)
	e.cache[p.Version] = expl
	return expl, nil
}

// Invalidate drops the cached explanation for a rule version.
func (e *Explainer) Invalidate(version string) {
	e.mu.Lock()
	delete(e.cache, version)
	e.mu.Unlock()
}
