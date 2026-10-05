package main

import (
	"sort"
)

// origin records who imposed a constraint on a package.
type origin struct {
	from string // "" means a root requirement
	con  Constraint
}

// state is a partial resolution.
type state struct {
	assign map[string]*Candidate
	cons   map[string][]origin
}

// objective is compared lexicographically: first the number of packages whose
// pinned (version, source) changed vs the lock, then total download bytes
// (new + changed packages).
type objective struct {
	changes int
	bytes   int64
}

func lessObjective(a, b objective) bool {
	if a.changes != b.changes {
		return a.changes < b.changes
	}
	return a.bytes < b.bytes
}

type solver struct {
	reg   *Registry
	lock  map[string]LockEntry
	roots map[string]Constraint

	best    map[string]*Candidate
	bestObj objective
	found   bool
}

// Solve finds an exact optimal resolution: lexicographically minimal
// (changed pins, download bytes). Returns ok=false if unsatisfiable.
func Solve(reg *Registry, lock map[string]LockEntry, rootReqs map[string]string) (map[string]*Candidate, objective, bool) {
	s := &solver{reg: reg, lock: lock, roots: map[string]Constraint{}}
	st := &state{assign: map[string]*Candidate{}, cons: map[string][]origin{}}
	names := make([]string, 0, len(rootReqs))
	for n := range rootReqs {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		con, err := ParseConstraint(rootReqs[n])
		if err != nil {
			return nil, objective{}, false
		}
		s.roots[n] = con
		st.cons[n] = append(st.cons[n], origin{from: "", con: con})
	}
	s.search(st)
	return s.best, s.bestObj, s.found
}

func (s *solver) search(st *state) {
	name := s.pickUnassigned(st)
	if name == "" {
		obj := s.objectiveOf(st.assign)
		if !s.found || lessObjective(obj, s.bestObj) {
			s.found = true
			s.bestObj = obj
			s.best = map[string]*Candidate{}
			for n, c := range st.assign {
				s.best[n] = c
			}
		}
		return
	}
	for _, cand := range s.candidatesFor(name, st.cons[name]) {
		added, ok := apply(st, name, cand)
		if ok {
			// Admissible bound: assigned packages only ever grow, so the
			// objective accumulated so far is a lower bound.
			if !s.found || lessObjective(s.objectiveOf(st.assign), s.bestObj) {
				s.search(st)
			}
		}
		undo(st, name, added)
	}
}

// pickUnassigned returns the constrained-but-unassigned package with the
// fewest feasible candidates (ties broken by name) for fail-fast search.
func (s *solver) pickUnassigned(st *state) string {
	best := ""
	bestCount := -1
	names := make([]string, 0, len(st.cons))
	for n := range st.cons {
		if st.assign[n] == nil {
			names = append(names, n)
		}
	}
	sort.Strings(names)
	for _, n := range names {
		count := len(candidatesFromRegistry(s.reg, n, st.cons[n]))
		if bestCount == -1 || count < bestCount {
			best, bestCount = n, count
			if count == 0 {
				break
			}
		}
	}
	return best
}

// candidatesFromRegistry lists candidates of name satisfying all constraints.
func candidatesFromRegistry(reg *Registry, name string, cons []origin) []*Candidate {
	var out []*Candidate
	for _, c := range reg.byName[name] {
		ok := true
		for _, o := range cons {
			if !o.con.Allows(c.parsed) {
				ok = false
				break
			}
		}
		if ok {
			out = append(out, c)
		}
	}
	return out
}

// candidatesFor orders feasible candidates: the locked pin first (to find
// low-change incumbents early), then by ascending download size.
func (s *solver) candidatesFor(name string, cons []origin) []*Candidate {
	cands := candidatesFromRegistry(s.reg, name, cons)
	locked, hasLock := s.lock[name]
	sort.SliceStable(cands, func(i, j int) bool {
		a, b := cands[i], cands[j]
		ai := hasLock && a.Version == locked.Version && a.Source == locked.Source
		bi := hasLock && b.Version == locked.Version && b.Source == locked.Source
		if ai != bi {
			return ai
		}
		if a.Size != b.Size {
			return a.Size < b.Size
		}
		if a.parsed.Nums != b.parsed.Nums {
			return a.parsed.Less(b.parsed)
		}
		return a.Source < b.Source
	})
	return cands
}

// apply assigns cand to name and propagates its dependency constraints.
// It returns the origins it appended (for undo) and whether the partial
// state is still consistent.
func apply(st *state, name string, cand *Candidate) (map[string]int, bool) {
	st.assign[name] = cand
	added := map[string]int{}
	for _, d := range cand.deps {
		st.cons[d.name] = append(st.cons[d.name], origin{from: name, con: d.con})
		added[d.name]++
		if other := st.assign[d.name]; other != nil && !d.con.Allows(other.parsed) {
			return added, false
		}
	}
	return added, true
}

func undo(st *state, name string, added map[string]int) {
	for n, k := range added {
		st.cons[n] = st.cons[n][:len(st.cons[n])-k]
		if len(st.cons[n]) == 0 {
			delete(st.cons, n)
		}
	}
	delete(st.assign, name)
}

// objectiveOf scores an assignment against the lock.
func (s *solver) objectiveOf(assign map[string]*Candidate) objective {
	var obj objective
	for n, c := range assign {
		locked, ok := s.lock[n]
		if !ok || locked.Version != c.Version || locked.Source != c.Source {
			obj.bytes += c.Size
			if ok {
				obj.changes++
			}
		}
	}
	return obj
}
