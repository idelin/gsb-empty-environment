package typeset

import "sort"

// maxFixpointRounds bounds the monotone fixpoint iteration. Pointer sets
// only grow and every round that changes state adds at least one
// (token, target) pair, so a finite program always converges first.
const maxFixpointRounds = 1_000_000

type flowEngine struct {
	analyzer *Analyzer
	facts    []Fact

	vars  map[string]map[PointerTarget]struct{}
	cells map[Cell]map[PointerTarget]struct{}

	// representative keeps one concrete path per (allocation, key) so
	// origins report a real traversal rather than a decoded key.
	representative map[string]map[subKey][]PathStep
}

// Analyze runs the finite abstract interpretation over the facts and
// resolves every virtual call.
func (a *Analyzer) Analyze(facts []Fact) (*Solution, error) {
	e := &flowEngine{
		analyzer:       a,
		facts:          facts,
		vars:           make(map[string]map[PointerTarget]struct{}),
		cells:          make(map[Cell]map[PointerTarget]struct{}),
		representative: make(map[string]map[subKey][]PathStep),
	}
	if err := e.validate(); err != nil {
		return nil, err
	}
	if err := e.solve(); err != nil {
		return nil, err
	}

	sol := &Solution{
		Vars:  make(map[string][]PointerTarget),
		Cells: make(map[Cell][]PointerTarget),
	}
	for v, set := range e.vars {
		sol.Vars[v] = sortTargets(setValues(set))
	}
	for cell, set := range e.cells {
		sol.Cells[cell] = sortTargets(setValues(set))
	}
	for _, fact := range facts {
		if call, ok := fact.(CallFact); ok {
			sol.Calls = append(sol.Calls, e.resolveCall(call))
		}
	}
	return sol, nil
}

func (e *flowEngine) validate() error {
	for _, fact := range e.facts {
		switch f := fact.(type) {
		case AllocFact:
			if f.AllocPoint == "" || f.Dst == "" {
				return programError("alloc fact missing allocation point or destination")
			}
			if _, ok := e.analyzer.classes[f.Class]; !ok {
				return programError("alloc %q references unknown class %q", f.AllocPoint, f.Class)
			}
		case CopyFact:
			if f.Src == "" || f.Dst == "" {
				return programError("copy fact missing source or destination")
			}
		case CastFact:
			if f.Src == "" || f.Dst == "" {
				return programError("cast fact missing source or destination")
			}
			if err := e.validatePath(f.Path); err != nil {
				return err
			}
		case FieldStoreFact:
			if f.Base == "" || f.FieldName == "" || f.Src == "" {
				return programError("field store fact missing base, field or source")
			}
		case FieldLoadFact:
			if f.Base == "" || f.FieldName == "" || f.Dst == "" {
				return programError("field load fact missing base, field or destination")
			}
		case CallFact:
			if f.CallSite == "" || f.Recv == "" || f.Method == "" {
				return programError("call fact missing call site, receiver or method")
			}
		default:
			return programError("unknown fact type %T", fact)
		}
	}
	return nil
}

func (e *flowEngine) validatePath(path []PathStep) error {
	for i, step := range path {
		c, ok := e.analyzer.classes[step.From]
		if !ok {
			return programError("cast path step %d starts at unknown class %q", i, step.From)
		}
		found := false
		for _, b := range c.DirectBases {
			if b.Name == step.To && b.Virtual == step.Virtual {
				found = true
				break
			}
		}
		if !found {
			return programError("cast path step %s -> %s (virtual=%v) is not an inheritance edge",
				step.From, step.To, step.Virtual)
		}
	}
	return nil
}

func (e *flowEngine) solve() error {
	// Seed allocations first; remaining facts iterate to a fixpoint.
	for _, fact := range e.facts {
		if alloc, ok := fact.(AllocFact); ok {
			e.applyAlloc(alloc)
		}
	}

	for rounds := 0; rounds < maxFixpointRounds; rounds++ {
		changed := false
		for _, fact := range e.facts {
			switch f := fact.(type) {
			case AllocFact:
				changed = e.applyAlloc(f) || changed
			case CopyFact:
				changed = e.addSet(f.Dst, e.varValues(f.Src)) || changed
			case CastFact:
				changed = e.applyCast(f) || changed
			case FieldStoreFact:
				e.applyStore(f)
			case FieldLoadFact:
				changed = e.applyLoad(f) || changed
			case CallFact:
				// Calls do not mutate pointer state.
			}
		}
		if !changed {
			return nil
		}
	}
	return programError("fixpoint did not converge within %d rounds", maxFixpointRounds)
}

func (e *flowEngine) ensureVar(name string) map[PointerTarget]struct{} {
	set, ok := e.vars[name]
	if !ok {
		set = make(map[PointerTarget]struct{})
		e.vars[name] = set
	}
	return set
}

func (e *flowEngine) varValues(name string) []PointerTarget {
	return setValues(e.vars[name])
}

func (e *flowEngine) addSet(dst string, targets []PointerTarget) bool {
	set := e.ensureVar(dst)
	before := len(set)
	for _, t := range targets {
		set[t] = struct{}{}
	}
	return len(set) > before
}

func (e *flowEngine) applyAlloc(f AllocFact) bool {
	root := rootKey(f.Class)
	target := PointerTarget{
		allocPoint:  f.AllocPoint,
		mostDerived: f.Class,
		key:         root,
	}
	set := e.ensureVar(f.Dst)
	if _, ok := set[target]; ok {
		return false
	}
	set[target] = struct{}{}

	rep, ok := e.representative[f.AllocPoint]
	if !ok {
		rep = make(map[subKey][]PathStep)
		e.representative[f.AllocPoint] = rep
	}
	rep[root] = nil
	return true
}

func (e *flowEngine) applyCast(f CastFact) bool {
	changed := false
	for _, t := range e.varValues(f.Src) {
		nextKey := appendKey(t.key, f.Path)
		next := PointerTarget{
			allocPoint:  t.allocPoint,
			mostDerived: t.mostDerived,
			key:         nextKey,
		}
		set := e.ensureVar(f.Dst)
		if _, ok := set[next]; !ok {
			set[next] = struct{}{}
			changed = true
		}
		rep := e.representative[t.allocPoint]
		if _, ok := rep[nextKey]; !ok {
			prev := rep[t.key]
			nextPath := append(append([]PathStep(nil), prev...), f.Path...)
			rep[nextKey] = nextPath
		}
	}
	return changed
}

// appendKey applies a concrete cast path to a canonical subobject key.
// canonicalKey handles virtual-edge sharing; non-virtual edges extend the
// path so repeated occurrences stay distinct.
func appendKey(start subKey, path []PathStep) subKey {
	// Reconstruct a representative concrete path while preserving each
	// edge's virtual flag, then re-canonicalize. The decoded start keeps the
	// shared/non-shared prefix; appended edges carry their own flags.
	startNames := splitKey(start)
	shared := len(start) > 0 && start[0] == 'V'
	steps := make([]PathStep, 0, len(startNames)+len(path)-1)
	for i := 1; i < len(startNames); i++ {
		steps = append(steps, PathStep{
			From:    startNames[i-1],
			To:      startNames[i],
			Virtual: shared,
		})
	}
	steps = append(steps, path...)
	return canonicalKey(steps)
}

func (e *flowEngine) applyStore(f FieldStoreFact) {
	for _, base := range e.varValues(f.Base) {
		cell := Cell{target: base, fieldName: f.FieldName}
		set, ok := e.cells[cell]
		if !ok {
			set = make(map[PointerTarget]struct{})
			e.cells[cell] = set
		}
		for _, t := range e.varValues(f.Src) {
			set[t] = struct{}{}
		}
	}
}

func (e *flowEngine) applyLoad(f FieldLoadFact) bool {
	changed := false
	for _, base := range e.varValues(f.Base) {
		cell := Cell{target: base, fieldName: f.FieldName}
		changed = e.addSet(f.Dst, setValues(e.cells[cell])) || changed
	}
	return changed
}

func (e *flowEngine) resolveCall(f CallFact) CallResult {
	result := CallResult{CallSite: f.CallSite, Method: f.Method}
	targets := e.varValues(f.Recv)
	if len(targets) == 0 {
		result.EmptyRecv = true
		return result
	}

	type targetKey struct {
		alloc string
		key   subKey
		decl  MethodDecl
	}
	seenTarget := make(map[targetKey]bool)
	seenAmbiguity := make(map[string]bool)

	for _, t := range sortTargets(targets) {
		table, err := e.analyzer.OverrideTableFor(t.mostDerived)
		if err != nil {
			continue
		}
		outcome := table.Resolve(t.key, f.Method)
		origin := e.originOf(t)

		if !outcome.HasSlot {
			if !containsOrigin(result.NoSlot, origin) {
				result.NoSlot = append(result.NoSlot, origin)
			}
			continue
		}
		if outcome.Ambiguous {
			ambKey := origin.AllocPoint + "|" + t.key.String() + "|" + f.Method
			if !seenAmbiguity[ambKey] {
				seenAmbiguity[ambKey] = true
				result.Ambiguities = append(result.Ambiguities, Ambiguity{
					Origin:     origin,
					Contenders: append([]MethodDecl(nil), outcome.Targets...),
				})
			}
			continue
		}

		for _, decl := range outcome.Targets {
			key := targetKey{alloc: origin.AllocPoint, key: t.key, decl: decl}
			if seenTarget[key] {
				continue
			}
			seenTarget[key] = true
			result.Targets = append(result.Targets, ResolvedTarget{
				Decl:   decl,
				subKey: t.key,
				Origin: origin,
			})
		}
	}

	sort.SliceStable(result.Targets, func(i, j int) bool {
		ti, tj := result.Targets[i], result.Targets[j]
		if ti.Decl.Class != tj.Decl.Class {
			return ti.Decl.Class < tj.Decl.Class
		}
		if ti.Origin.AllocPoint != tj.Origin.AllocPoint {
			return ti.Origin.AllocPoint < tj.Origin.AllocPoint
		}
		return ti.Origin.MostDerived < tj.Origin.MostDerived
	})
	sort.SliceStable(result.Ambiguities, func(i, j int) bool {
		return result.Ambiguities[i].Origin.AllocPoint < result.Ambiguities[j].Origin.AllocPoint
	})
	sort.SliceStable(result.NoSlot, func(i, j int) bool {
		return result.NoSlot[i].AllocPoint < result.NoSlot[j].AllocPoint
	})
	return result
}

func (e *flowEngine) originOf(t PointerTarget) Origin {
	path := decodeKey(t.key)
	if rep, ok := e.representative[t.allocPoint]; ok {
		if concrete, ok := rep[t.key]; ok && concrete != nil {
			path = append([]PathStep(nil), concrete...)
		}
	}
	return Origin{
		AllocPoint:  t.allocPoint,
		MostDerived: t.mostDerived,
		Path:        path,
	}
}

func containsOrigin(haystack []Origin, needle Origin) bool {
	for _, o := range haystack {
		if o.AllocPoint == needle.AllocPoint &&
			o.MostDerived == needle.MostDerived &&
			pathsEqual(o.Path, needle.Path) {
			return true
		}
	}
	return false
}

func pathsEqual(a, b []PathStep) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func setValues(set map[PointerTarget]struct{}) []PointerTarget {
	out := make([]PointerTarget, 0, len(set))
	for t := range set {
		out = append(out, t)
	}
	return sortTargets(out)
}

func sortTargets(in []PointerTarget) []PointerTarget {
	sort.Slice(in, func(i, j int) bool {
		if in[i].allocPoint != in[j].allocPoint {
			return in[i].allocPoint < in[j].allocPoint
		}
		if in[i].mostDerived != in[j].mostDerived {
			return in[i].mostDerived < in[j].mostDerived
		}
		return in[i].key.String() < in[j].key.String()
	})
	return in
}
