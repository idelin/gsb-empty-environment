package resolve

import (
	"fmt"
	"math"
	"sort"
	"strings"
)

// Result is the outcome of Solve. Exactly one of Locked or Reason is
// meaningful: when the requests are satisfiable, Locked maps every resolved
// package to its selected version and Reason is empty; otherwise Locked is
// nil and Reason explains why no solution exists.
type Result struct {
	Locked map[string]Version
	Reason string
}

// OK reports whether the requests were satisfiable.
func (r Result) OK() bool { return r.Reason == "" }

// String renders the result as a deterministic lock file ("name version"
// lines, sorted by name) or as the unsatisfiability reason.
func (r Result) String() string {
	if !r.OK() {
		return "unsatisfiable: " + r.Reason
	}
	names := make([]string, 0, len(r.Locked))
	for name := range r.Locked {
		names = append(names, name)
	}
	sort.Strings(names)
	var b strings.Builder
	for _, name := range names {
		fmt.Fprintf(&b, "%s %s\n", name, r.Locked[name])
	}
	return b.String()
}

// Solve resolves requests against m. Request order defines the priority of
// the primary objective: among all feasible assignments it first maximizes
// the requested packages' version vector lexicographically (requests[0] is
// the most significant component), then minimizes the total number of
// resolved packages.
func Solve(m *Manifest, requests []string) (Result, error) {
	if err := m.Validate(); err != nil {
		return Result{}, err
	}
	s := &solver{m: m, failDepth: -1}
	st := newState()
	for _, r := range requests {
		p, ok := m.Packages[r]
		if !ok {
			return Result{Reason: fmt.Sprintf("requested package %q is not in the manifest", r)}, nil
		}
		if len(p.Versions) == 0 {
			return Result{Reason: fmt.Sprintf("requested package %q has no available versions", r)}, nil
		}
		st.cons[r] = constraint{set: true, max: maxVersion, maxSrc: "request", minSrc: "request"}
	}

	// Phase 1: fix the lexicographically maximal feasible request vector.
	fixed, ok := s.fixRequests(st, requests, 0)
	if !ok {
		reason := s.failMsg
		if reason == "" {
			reason = "no feasible assignment"
		}
		return Result{Reason: reason}, nil
	}

	// Phase 2: with the request vector fixed, minimize the package count.
	best := s.minimize(fixed)
	return Result{Locked: best.assigned}, nil
}

// maxVersion acts as "+infinity" for unconstrained upper bounds.
var maxVersion = Version{Major: math.MaxInt32, Minor: math.MaxInt32, Patch: math.MaxInt32}

// constraint is the intersection of all dependency ranges accumulated on a
// package. A package present in cons is required by the current partial
// assignment.
type constraint struct {
	min, max       Version
	minSrc, maxSrc string
	set            bool
}

func (c constraint) String() string {
	return fmt.Sprintf("[%s, %s]", c.min, c.max)
}

// state is an immutable search node: assigned versions plus the accumulated
// dependency constraints.
type state struct {
	assigned map[string]Version
	cons     map[string]constraint
}

func newState() *state {
	return &state{
		assigned: make(map[string]Version),
		cons:     make(map[string]constraint),
	}
}

func (st *state) clone() *state {
	ns := &state{
		assigned: make(map[string]Version, len(st.assigned)+1),
		cons:     make(map[string]constraint, len(st.cons)+4),
	}
	for k, v := range st.assigned {
		ns.assigned[k] = v
	}
	for k, v := range st.cons {
		ns.cons[k] = v
	}
	return ns
}

type solver struct {
	m *Manifest
	// Deepest failure seen, used to explain unsatisfiability.
	failDepth int
	failMsg   string
}

func (s *solver) noteFailure(depth int, msg string) {
	if depth > s.failDepth {
		s.failDepth, s.failMsg = depth, msg
	}
}

// assign returns a new state with pkg fixed at v and its dependencies merged
// into the constraints, or a human-readable failure message.
func (s *solver) assign(st *state, pkg string, v Version) (*state, string) {
	if c, ok := st.cons[pkg]; ok && c.set {
		if v.Compare(c.min) < 0 || v.Compare(c.max) > 0 {
			return nil, fmt.Sprintf("%s@%s is outside the required range %s (from %s)",
				pkg, v, c, c.maxSrc)
		}
	}
	for _, cf := range s.m.Conflicts {
		other, ok := conflictPartner(cf, pkg, v)
		if !ok {
			continue
		}
		if w, assigned := st.assigned[other.name]; assigned && w == other.version {
			return nil, fmt.Sprintf("%s@%s conflicts with %s@%s", pkg, v, other.name, w)
		}
	}

	ns := st.clone()
	ns.assigned[pkg] = v
	for _, d := range s.m.Packages[pkg].version(v).Deps {
		src := fmt.Sprintf("%s@%s", pkg, v)
		c := ns.cons[d.Name]
		if !c.set {
			c = constraint{set: true, min: d.Min, max: d.Max, minSrc: src, maxSrc: src}
		} else {
			if d.Min.Compare(c.min) > 0 {
				c.min, c.minSrc = d.Min, src
			}
			if d.Max.Compare(c.max) < 0 {
				c.max, c.maxSrc = d.Max, src
			}
		}
		if c.min.Compare(c.max) > 0 {
			return nil, fmt.Sprintf("dependencies on %q disagree: %s requires >= %s but %s requires <= %s",
				d.Name, c.minSrc, c.min, c.maxSrc, c.max)
		}
		if w, done := ns.assigned[d.Name]; done && (w.Compare(c.min) < 0 || w.Compare(c.max) > 0) {
			return nil, fmt.Sprintf("%s requires %q in [%s, %s], but it is already locked at %s",
				src, d.Name, c.min, c.max, w)
		}
		ns.cons[d.Name] = c
	}
	return ns, ""
}

type conflictEnd struct {
	name    string
	version Version
}

// conflictPartner returns the other end of cf if (pkg, v) matches one side.
func conflictPartner(cf Conflict, pkg string, v Version) (conflictEnd, bool) {
	switch {
	case cf.PackageA == pkg && cf.VersionA == v:
		return conflictEnd{cf.PackageB, cf.VersionB}, true
	case cf.PackageB == pkg && cf.VersionB == v:
		return conflictEnd{cf.PackageA, cf.VersionA}, true
	}
	return conflictEnd{}, false
}

// candidates lists the versions of pkg still allowed by the accumulated
// range and by conflicts with already assigned packages, newest first.
func (s *solver) candidates(st *state, pkg string) []Version {
	p, ok := s.m.Packages[pkg]
	if !ok {
		return nil
	}
	c := st.cons[pkg]
	var out []Version
	for i := len(p.Versions) - 1; i >= 0; i-- {
		v := p.Versions[i].Version
		if c.set && (v.Compare(c.min) < 0 || v.Compare(c.max) > 0) {
			continue
		}
		blocked := false
		for _, cf := range s.m.Conflicts {
			other, hit := conflictPartner(cf, pkg, v)
			if !hit {
				continue
			}
			if w, assigned := st.assigned[other.name]; assigned && w == other.version {
				blocked = true
				break
			}
		}
		if !blocked {
			out = append(out, v)
		}
	}
	return out
}

// pickUnassigned selects the required-but-unassigned package with the fewest
// remaining candidates (ties broken by name). It returns "" when every
// required package is assigned.
func (s *solver) pickUnassigned(st *state) (string, []Version) {
	best := ""
	var bestCands []Version
	for _, pkg := range sortedKeys(st.cons) {
		if _, done := st.assigned[pkg]; done {
			continue
		}
		cands := s.candidates(st, pkg)
		if best == "" || len(cands) < len(bestCands) || (len(cands) == len(bestCands) && pkg < best) {
			best, bestCands = pkg, cands
		}
	}
	return best, bestCands
}

// extendable reports whether the partial state can be completed into a full
// solution. Failures are recorded for the unsatisfiability report.
func (s *solver) extendable(st *state) bool {
	pkg, cands := s.pickUnassigned(st)
	if pkg == "" {
		return true
	}
	if len(cands) == 0 {
		s.noteFailure(len(st.assigned), s.noCandidateReason(st, pkg))
		return false
	}
	for _, v := range cands {
		ns, fail := s.assign(st, pkg, v)
		if fail != "" {
			s.noteFailure(len(st.assigned), fail)
			continue
		}
		if s.extendable(ns) {
			return true
		}
	}
	return false
}

func (s *solver) noCandidateReason(st *state, pkg string) string {
	c := st.cons[pkg]
	p, ok := s.m.Packages[pkg]
	if !ok {
		return fmt.Sprintf("package %q required by %s is not in the manifest", pkg, c.maxSrc)
	}
	// If some version is in range, the package is only blocked by conflicts;
	// report the first one.
	for _, pv := range p.Versions {
		v := pv.Version
		if c.set && (v.Compare(c.min) < 0 || v.Compare(c.max) > 0) {
			continue
		}
		for _, cf := range s.m.Conflicts {
			other, hit := conflictPartner(cf, pkg, v)
			if !hit {
				continue
			}
			if w, assigned := st.assigned[other.name]; assigned && w == other.version {
				return fmt.Sprintf("%s@%s conflicts with %s@%s", pkg, v, other.name, w)
			}
		}
	}
	if c.set {
		return fmt.Sprintf("no version of %q satisfies the required range %s (from %s)", pkg, c, c.maxSrc)
	}
	return fmt.Sprintf("no version of %q is usable: every version conflicts with an assigned package", pkg)
}

// fixRequests assigns the requested packages in request order, trying newer
// versions first, so the first feasible leaf yields the lexicographically
// maximal request vector.
func (s *solver) fixRequests(st *state, requests []string, i int) (*state, bool) {
	if i == len(requests) {
		if s.extendable(st) {
			return st, true
		}
		return nil, false
	}
	pkg := requests[i]
	if _, done := st.assigned[pkg]; done { // duplicate request
		return s.fixRequests(st, requests, i+1)
	}
	for _, v := range s.candidates(st, pkg) {
		ns, fail := s.assign(st, pkg, v)
		if fail != "" {
			s.noteFailure(len(st.assigned), fail)
			continue
		}
		if res, ok := s.fixRequests(ns, requests, i+1); ok {
			return res, true
		}
	}
	return nil, false
}

// minimize completes st (whose requested packages are already fixed) with as
// few packages as possible, using branch and bound.
func (s *solver) minimize(st *state) *state {
	var best *state
	bestCount := -1

	var search func(st *state)
	search = func(st *state) {
		pkg, cands := s.pickUnassigned(st)
		if pkg == "" {
			if bestCount < 0 || len(st.assigned) < bestCount {
				best, bestCount = st, len(st.assigned)
			}
			return
		}
		remaining := 0
		for name := range st.cons {
			if _, done := st.assigned[name]; !done {
				remaining++
			}
		}
		if bestCount >= 0 && len(st.assigned)+remaining >= bestCount {
			return
		}
		// Try versions with fewer dependencies first to find a tight bound
		// early; newest first among equals for determinism.
		ordered := make([]Version, len(cands))
		copy(ordered, cands)
		sort.SliceStable(ordered, func(i, j int) bool {
			di := len(s.m.Packages[pkg].version(ordered[i]).Deps)
			dj := len(s.m.Packages[pkg].version(ordered[j]).Deps)
			if di != dj {
				return di < dj
			}
			return ordered[i].Compare(ordered[j]) > 0
		})
		for _, v := range ordered {
			if ns, fail := s.assign(st, pkg, v); fail == "" {
				search(ns)
			}
		}
	}
	search(st)
	return best
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
