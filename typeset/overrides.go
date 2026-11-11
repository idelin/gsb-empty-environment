package typeset

import "sort"

// OverrideOutcome is the resolution of one virtual slot: a subobject inside a
// most-derived object plus a method name.
type OverrideOutcome struct {
	// HasSlot reports whether the method is a virtual function reachable
	// from this subobject's static class.
	HasSlot bool

	// Ambiguous reports the "no unique final overrider" condition: the
	// contenders are declared on classes that are not ancestors of one
	// another along the subobject DAG rooted at the most-derived object.
	Ambiguous bool

	// Targets holds the final overriders. An unambiguous slot has exactly
	// one entry; an ambiguous slot lists every contender.
	Targets []MethodDecl
}

// OverrideTable stores the final-overrider outcome for every virtual slot in
// one most-derived object.
type OverrideTable struct {
	MostDerived string

	subs *SubobjectTable

	analyzerRef *Analyzer

	// cache memoizes per-subobject resolutions.
	cache map[subKey]map[string]OverrideOutcome

	// virtualMethods is the universe of names declared virtual anywhere.
	virtualMethods map[string]bool
}

// OverrideTableFor builds (and memoizes) the final-overrider table for
// objects of mostDerived.
func (a *Analyzer) OverrideTableFor(mostDerived string) (*OverrideTable, error) {
	if t, ok := a.overrides[mostDerived]; ok {
		return t, nil
	}
	subs, err := a.SubobjectTableFor(mostDerived)
	if err != nil {
		return nil, err
	}

	virtualMethods := make(map[string]bool)
	for _, name := range a.order {
		for _, m := range a.classes[name].Methods {
			if m.Virtual {
				virtualMethods[m.Name] = true
			}
		}
	}

	t := &OverrideTable{
		MostDerived:    mostDerived,
		subs:           subs,
		cache:          make(map[subKey]map[string]OverrideOutcome),
		virtualMethods: virtualMethods,
		analyzerRef:    a,
	}
	a.overrides[mostDerived] = t
	return t, nil
}

// Resolve returns the final-overrider outcome of method on the subobject
// identified by key inside this most-derived object.
func (t *OverrideTable) Resolve(key subKey, method string) OverrideOutcome {
	if methods, ok := t.cache[key]; ok {
		if outcome, ok := methods[method]; ok {
			return outcome
		}
	}
	outcome := t.analyzerRef.resolve(t, key, method)
	methods, ok := t.cache[key]
	if !ok {
		methods = make(map[string]OverrideOutcome)
		t.cache[key] = methods
	}
	methods[method] = outcome
	return outcome
}

// CallableMethods returns the virtual method names that have an unambiguous
// final overrider on the given subobject. The random-program generator uses
// it to emit only executions with well-defined dispatch.
func (t *OverrideTable) CallableMethods(key subKey) []string {
	var out []string
	for method := range t.virtualMethods {
		outcome := t.Resolve(key, method)
		if outcome.HasSlot && !outcome.Ambiguous {
			out = append(out, method)
		}
	}
	sort.Strings(out)
	return out
}

func (a *Analyzer) resolve(table *OverrideTable, key subKey, method string) OverrideOutcome {
	if !table.virtualMethods[method] {
		return OverrideOutcome{}
	}
	staticClass, ok := table.subs.Subobjects[key]
	if !ok {
		return OverrideOutcome{}
	}

	// A virtual slot exists when the static class or one of its ancestors
	// declares the method virtual.
	slotExists := false
	for anc := range a.ancestors[staticClass] {
		if decl, ok := a.methodDecl(anc, method); ok && decl.Virtual {
			slotExists = true
			break
		}
	}
	if !slotExists {
		return OverrideOutcome{}
	}

	// Candidate declarations live on subobjects that are either X itself or
	// ancestors/descendants of X inside this most-derived object's subobject
	// DAG. Subobject ancestry (not just class ancestry) keeps repeated
	// non-virtual occurrences independent.
	candidates := a.collectCandidates(table, key, method)
	if len(candidates) == 0 {
		return OverrideOutcome{HasSlot: true}
	}

	// Shadowing: a candidate whose declaring class is a strict base of
	// another candidate's declaring class is overridden by the descendant.
	kept := make([]MethodDecl, 0, len(candidates))
	for i, decl := range candidates {
		shadowed := false
		for j, other := range candidates {
			if i == j {
				continue
			}
			if decl.Class != other.Class && a.IsBaseOf(decl.Class, other.Class) {
				shadowed = true
				break
			}
		}
		if !shadowed {
			kept = append(kept, decl)
		}
	}

	sort.Slice(kept, func(i, j int) bool {
		if kept[i].Class != kept[j].Class {
			return kept[i].Class < kept[j].Class
		}
		return kept[i].Method < kept[j].Method
	})

	if len(kept) == 1 {
		return OverrideOutcome{HasSlot: true, Targets: kept}
	}
	return OverrideOutcome{HasSlot: true, Ambiguous: true, Targets: kept}
}

// collectCandidates gathers every same-name method declaration on subobjects
// connected to key in the subobject DAG.
func (a *Analyzer) collectCandidates(table *OverrideTable, key subKey, method string) []MethodDecl {
	related := make(map[subKey]bool)
	related[key] = true

	// Ancestors of key (subobjects that contain key), including key itself.
	frontier := []subKey{key}
	for len(frontier) > 0 {
		cur := frontier[len(frontier)-1]
		frontier = frontier[:len(frontier)-1]
		for _, parent := range table.subs.Parents[cur] {
			if !related[parent] {
				related[parent] = true
				frontier = append(frontier, parent)
			}
		}
	}

	// Descendants of key (subobjects nested inside key).
	frontier = []subKey{key}
	for len(frontier) > 0 {
		cur := frontier[len(frontier)-1]
		frontier = frontier[:len(frontier)-1]
		for _, child := range table.subs.Children[cur] {
			if !related[child] {
				related[child] = true
				frontier = append(frontier, child)
			}
		}
	}

	seen := make(map[string]bool)
	var out []MethodDecl
	for sub := range related {
		className := table.subs.Subobjects[sub]
		if decl, ok := a.methodDecl(className, method); ok {
			if !seen[decl.Class] {
				seen[decl.Class] = true
				out = append(out, decl)
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Class < out[j].Class })
	return out
}

// HasVirtualSlot reports whether method names a virtual function reachable
// from className (the class itself or one of its ancestors).
func (a *Analyzer) HasVirtualSlot(className, method string) bool {
	for anc := range a.ancestors[className] {
		if decl, ok := a.methodDecl(anc, method); ok && decl.Virtual {
			return true
		}
	}
	return false
}

// VirtualMethods returns the names declared virtual anywhere in the graph.
func (a *Analyzer) VirtualMethods() []string {
	seen := make(map[string]bool)
	for _, name := range a.order {
		for _, m := range a.classes[name].Methods {
			if m.Virtual {
				seen[m.Name] = true
			}
		}
	}
	out := make([]string, 0, len(seen))
	for m := range seen {
		out = append(out, m)
	}
	sort.Strings(out)
	return out
}
